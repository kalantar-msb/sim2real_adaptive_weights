# Sim2Real Bundle: Entropy-Based Adaptive Scorer Weights

Replaces llm-d's fixed-weight 3:2:2 scoring with **parameter-free adaptive weights** driven by per-scorer entropy. Scorers that concentrate traffic get suppressed; scorers that spread load get amplified. Zero tuned parameters — everything derives from cluster size.

BLIS simulation results (Qwen3-14B, 4×H100, 40 QPS):

| Workload | Description | E2E mean | E2E p90 | E2E p99 |
|----------|-------------|----------|---------|---------|
| W1 | Prefix → cold shift | **-17.6%** | -27.4% | -30.8% |
| W2 | Stationary (sanity check) | -2.3% | -2.5% | -3.4% |
| W3 | Prefix hotspot flip | **-28.3%** | -33.6% | -36.4% |

## Algorithm

Each scorer produces [0,1] scores per endpoint. The algorithm measures how concentrated each scorer's scores are using **normalized Shannon entropy**:

```
For each scorer i:
  H[i] = -Σ (share × log(share))     where share = score[ep] / total_scores
  entropy[i] = H[i] / log(k)          normalized to [0,1], k = endpoint count

EMA smoothing:
  ema_entropy[i] = (1 - α) × ema_entropy[i] + α × entropy[i]
  α = 2 / (k + 1)                     derived from cluster size

Adaptive weight:
  weight[i] = base_weight[i] × ema_entropy[i]^(k/2)
  (renormalize to sum to 1)

Final score:
  score[ep] = Σ weight[i] × scorer_i[ep]
```

**Why it works**: When prefix scorer gives [0.9, 0.0, 0.0, 0.1], entropy is low → weight suppressed → queue/KV scorers dominate → traffic spreads. When prefix scorer gives [0.4, 0.3, 0.2, 0.1], entropy is high → weight preserved → cache locality maintained.

**No tuned parameters**: α derives from k (cluster size), exponent derives from k, EMA initialization derives from k. Works on any cluster size, any scorer order, any number of scorers.

## Bundle Structure

```
sim2real_bundle_adaptive_weights/
├── README.md                              # This file
├── config.md                              # EPP configs (baseline + treatment) + vLLM args
├── algorithms/
│   ├── algorithm_adaptive.go              # Adaptive algorithm — entropy-based weight blending
│   └── algorithm_control.go               # Control — static 3:2:2 via same delegation path
└── workloads/
    ├── w1_prefix_to_load_shift.yaml       # Phase 1: 70% prefix, Phase 2: 100% cold
    ├── w2_stationary.yaml                 # Steady 60% prefix + 40% cold (sanity check)
    └── w3_multi_prefix_contention.yaml    # Phase 1: 85% prefix-A, Phase 2: 85% prefix-B
```

## Two Treatments

Both treatments use the **same deployment path** — a single `scheduling.Scorer` plugin that delegates to PPC/queue/KV sub-scorers via `plugin.Handle`. The only difference is the blending logic inside `Score()`:

| Treatment | Algorithm | Blending Logic |
|-----------|-----------|----------------|
| **Control** | `algorithm_control.go` | Static `clamp(score) × weight` — replicates GAIE's `runScorerPlugins()` exactly |
| **Adaptive** | `algorithm_adaptive.go` | Entropy-based adaptive weights — suppresses concentrated scorers |

**Why a control?** If control ≡ baseline, the delegation plumbing is validated. Any delta between adaptive and baseline is purely from the algorithm, not from the delegation mechanism.

The EPP config, factory, registration, and deployment steps are identical for both — only the `Score()` body differs.

## Transfer to llm-d: Step-by-Step

Both algorithms are pure Go with no dependencies. To deploy either in llm-d, wrap it in a `scheduling.Scorer` plugin that delegates to existing scorers. Here's exactly how.

### Step 1: Create a new scorer plugin

Create `llm-d-inference-scheduler/pkg/plugins/scorer/adaptive_routing.go`.

Use `kv-cache-utilization-scorer` as your structural template — it's the simplest scorer at 83 lines:
```
gateway-api-inference-extension/pkg/epp/framework/plugins/scheduling/scorer/kvcacheutilization/kvcache_utilization.go
```

Your scorer needs to implement `scheduling.Scorer`:
```go
type Scorer interface {
    plugin.Plugin                           // TypedName() TypedName
    Category() ScorerCategory               // return scheduling.Balance
    Score(ctx context.Context, cycleState *CycleState, request *InferenceRequest,
        pods []Endpoint) map[Endpoint]float64
}
```

### Step 2: Delegate to existing scorers via plugin.Handle

In your factory, retrieve PPC/queue/KV scorers from Handle — same pattern as `disagg_profile_handler.go:143`:

```go
func Factory(name string, raw json.RawMessage, handle plugin.Handle) (plugin.Plugin, error) {
    // Parse config for sub-scorer names (e.g. "ppc", "qd", "kv")
    // Retrieve each sub-scorer:
    ppcScorer, err := plugin.PluginByType[scheduling.Scorer](handle, "ppc")
    qdScorer, err  := plugin.PluginByType[scheduling.Scorer](handle, "qd")
    kvScorer, err  := plugin.PluginByType[scheduling.Scorer](handle, "kv")
    // Store as fields on your scorer struct
}
```

`PluginByType[P]` is the generic helper at `handle.go:112-124` — it calls `handle.Plugin(name)` and type-asserts.

### Step 3: Implement Score() using the algorithm

In your `Score()` method:
1. Call each sub-scorer's `Score()` to get raw [0,1] scores per endpoint
2. Feed those scores into the blending logic from the algorithm file
3. Return the blended scores

**For control** (`algorithm_control.go`): static `clamp(score) × weight` — replicates GAIE's `runScorerPlugins()`.

**For adaptive** (`algorithm_adaptive.go`): entropy-based adaptive blend.

```go
func (s *Scorer) Score(ctx, cycleState, request, endpoints) map[Endpoint]float64 {
    prefixScores := s.ppcScorer.Score(ctx, cycleState, request, endpoints)
    queueScores  := s.qdScorer.Score(ctx, cycleState, request, endpoints)
    kvScores     := s.kvScorer.Score(ctx, cycleState, request, endpoints)
    // Apply blending logic (control: static weights, adaptive: entropy-based)
    // Return final scores
}
```

**PPC fallback**: PPC's `Score()` works even if `PrepareRequestData` hasn't run — it falls back to `getScores()` directly (`precise_prefix_cache.go:474`). So calling it from your scorer works regardless.

### Step 4: Register the plugin

In `gateway-api-inference-extension/cmd/epp/runner/runner.go`, add alongside existing registrations:

```go
plugin.Register("adaptive-routing-scorer", scorer.AdaptiveRoutingScorerFactory)
```

Existing registrations follow the same pattern:
```go
plugin.Register("kv-cache-utilization-scorer", kvcacheutilization.KvCacheUtilizationScorerFactory)
plugin.Register("queue-scorer", queuedepth.QueueScorerFactory)
```

### Step 5: Deploy EPP config

Use the **Treatment** config from `config.md`. The key difference from baseline:
- PPC, queue, KV scorers are instantiated as plugins (they keep their configs)
- They are NOT in the scheduling profile — only `adaptive-routing-scorer` is (weight 1.0)
- The adaptive scorer calls sub-scorers' `Score()` via Handle

### Step 6: Verify

Run the three workloads from `workloads/` against both baseline and treatment EPP configs. Compare E2E mean, TTFT, and load distribution across instances.

## How Scorer Delegation Works

The adaptive scorer **does not** reimplement prefix/queue/KV scoring. It delegates to existing scorers via `plugin.Handle` — the same mechanism used by `disagg_profile_handler` in production.

```
Factory phase (once at startup):
  handle.Plugin("ppc") → type-assert to scheduling.Scorer → store as sub-scorer
  handle.Plugin("qd")  → type-assert to scheduling.Scorer → store as sub-scorer
  handle.Plugin("kv")  → type-assert to scheduling.Scorer → store as sub-scorer

Score phase (per request):
  adaptive.Score(ctx, cycleState, request, endpoints)
    → ppc.Score(ctx, cycleState, request, endpoints)   → prefix scores [0,1]
    → qd.Score(ctx, cycleState, request, endpoints)    → queue scores [0,1]
    → kv.Score(ctx, cycleState, request, endpoints)    → KV scores [0,1]
    → entropy-based adaptive weight blending
    → return final scores [0,1]
```

`PluginByType[scheduling.Scorer](handle, name)` is the type-safe generic helper at `handle.go:112-124`.


## Source References

Paths are relative to each repo root. GitHub repos:
- GAIE: `sigs.k8s.io/gateway-api-inference-extension`
- llm-d: `github.com/llm-d/llm-d-inference-scheduler`

### GAIE (gateway-api-inference-extension)

| What | File | Key Lines |
|------|------|-----------|
| **Plugin interface** | `pkg/epp/framework/interface/plugin/plugins.go` | `Plugin` interface: `TypedName() TypedName` |
| **Scorer interface** | `pkg/epp/framework/interface/scheduling/plugins.go:68-72` | `Score(ctx, *CycleState, *InferenceRequest, []Endpoint) map[Endpoint]float64` |
| **ScorerCategory** | same file :28-38 | `Affinity`, `Distribution`, `Balance` |
| **plugin.Handle** | `pkg/epp/framework/interface/plugin/handle.go:27-35` | `Handle` embeds `HandlePlugins` |
| **HandlePlugins** | same file :38-50 | `Plugin(name) Plugin`, `AddPlugin(name, plugin)` |
| **PluginByType[P]** | same file :112-124 | Generic type-safe retrieval: `PluginByType[scheduling.Scorer](handle, "ppc")` |
| **Plugin registry** | `pkg/epp/framework/interface/plugin/registry.go:28-33` | `Register(pluginType, factory)`, `Registry map[string]FactoryFunc` |
| **Factory signature** | same file :25 | `func(name string, parameters json.RawMessage, handle Handle) (Plugin, error)` |
| **KV utilization scorer** | `pkg/epp/framework/plugins/scheduling/scorer/kvcacheutilization/kvcache_utilization.go` | Simplest scorer template (83 lines). Score = `1 - endpoint.GetMetrics().KVCacheUsagePercent` |
| **Queue scorer** | `pkg/epp/framework/plugins/scheduling/scorer/queuedepth/queue.go` | Min-max normalized `WaitingQueueSize`. Score = `(max - qd) / (max - min)` |
| **configloader Scorer assert** | `pkg/epp/config/loader/configloader.go:242` | `if scorer, ok := plugin.(framework.Scorer); ok { ... }` |

### llm-d (llm-d-inference-scheduler)

| What | File | Key Lines |
|------|------|-----------|
| **PPC scorer** | `pkg/plugins/scorer/precise_prefix_cache.go` | Full prefix cache scorer with kvcache.Indexer |
| **PPC Score() fallback** | same file :467-482 | Falls back to `getScores()` if PluginState missing |
| **PPC factory** | same file :120-150 | Parses indexerConfig, kvEventsConfig, tokenProcessorConfig |
| **disagg\_profile\_handler** | `pkg/plugins/profile/disagg_profile_handler.go:143-151` | Production example of `handle.Plugin(name)` + type-assert |

### Endpoint Metrics (what sub-scorers read)

The sub-scorers read these from `endpoint.GetMetrics()`:

| Field | Type | Used By |
|-------|------|---------|
| `KVCacheUsagePercent` | `float64` (0-1) | KV utilization scorer |
| `WaitingQueueSize` | `int` | Queue scorer |
| `RunningRequestsSize` | `int` | (not used by queue scorer, but available) |

Our adaptive scorer does NOT read these directly — it only reads sub-scorer outputs.

## Thread Safety

The `emaSpread` state persists across requests. EPP processes requests sequentially within a scheduling cycle — no concurrent access to a single scorer instance. No mutex needed. If GAIE ever moves to concurrent scheduling, wrap the EMA update in a `sync.Mutex`.
