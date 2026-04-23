# Configuration

## Shared Infrastructure

Both baseline and treatment use identical infrastructure — same tokenizer, same profile handler, same picker. Only the scoring plugins differ.

## Baseline (Fixed 3:2:2 Weights)

```yaml
apiVersion: inference.networking.x-k8s.io/v1alpha1
kind: EndpointPickerConfig
plugins:
  - name: profile
    type: single-profile-handler
  - name: tokenizer
    type: tokenizer
    parameters:
      modelName: Qwen/Qwen3-14B
      udsTokenizerConfig:
        socketFile: /tmp/tokenizer/tokenizer-uds.socket
  - name: ppc
    type: precise-prefix-cache-scorer
    parameters:
      tokenProcessorConfig:
        blockSize: 64
      indexerConfig:
        speculativeIndexing: true
        tokenizersPoolConfig:
          modelName: Qwen/Qwen3-14B
          local: null
          hf: null
          uds:
            socketFile: /tmp/tokenizer/tokenizer-uds.socket
      kvEventsConfig:
        topicFilter: "kv@"
        concurrency: 4
        discoverPods: false
        zmqEndpoint: "tcp://*:5557"
  - name: kv
    type: kv-cache-utilization-scorer
  - name: qd
    type: queue-scorer
  - name: picker
    type: max-score-picker
schedulingProfiles:
  - name: default
    plugins:
      - pluginRef: ppc
        weight: 3.0
      - pluginRef: kv
        weight: 2.0
      - pluginRef: qd
        weight: 2.0
      - pluginRef: picker
```

Scorer weights: **3:2:2** — prefix gets 3/7 = 43%, load scorers get 4/7 = 57%. Fixed, cannot adapt.

## Treatment (Scorer Delegation — Control & Adaptive)

Both treatments use the **same EPP config**. The only difference is the blending logic inside `Score()`:
- **Control**: static `clamp(score) × weight` — replicates GAIE's `runScorerPlugins()` exactly
- **Adaptive**: entropy-based adaptive weights

Three weighted scorers replaced by one delegation scorer that calls PPC/queue/KV sub-scorers via `plugin.Handle`. PPC, queue, and KV are still instantiated as plugins (they need their configs for KV event subscription, etc.), but they're NOT listed in the scheduling profile.

```yaml
apiVersion: inference.networking.x-k8s.io/v1alpha1
kind: EndpointPickerConfig
plugins:
  - name: profile
    type: single-profile-handler
  - name: tokenizer
    type: tokenizer
    parameters:
      modelName: Qwen/Qwen3-14B
      udsTokenizerConfig:
        socketFile: /tmp/tokenizer/tokenizer-uds.socket
  # Sub-scorers — instantiated so the delegation scorer can call them via Handle
  - name: ppc
    type: precise-prefix-cache-scorer
    parameters:
      tokenProcessorConfig:
        blockSize: 64
      indexerConfig:
        speculativeIndexing: true
        tokenizersPoolConfig:
          modelName: Qwen/Qwen3-14B
          local: null
          hf: null
          uds:
            socketFile: /tmp/tokenizer/tokenizer-uds.socket
      kvEventsConfig:
        topicFilter: "kv@"
        concurrency: 4
        discoverPods: false
        zmqEndpoint: "tcp://*:5557"
  - name: kv
    type: kv-cache-utilization-scorer
  - name: qd
    type: queue-scorer
  # Delegation scorer — calls ppc/kv/qd, applies blending (control: static, adaptive: entropy-based)
  - name: delegation-scorer
    type: adaptive-routing-scorer          # same plugin type for both control and adaptive
    parameters:
      scorerNames: ["ppc", "qd", "kv"]
      baseWeights: [3.0, 2.0, 2.0]
      mode: "adaptive"                     # "control" for static 3:2:2, "adaptive" for entropy-based
  - name: picker
    type: max-score-picker
schedulingProfiles:
  - name: default
    plugins:
      - pluginRef: delegation-scorer
        weight: 1.0
      - pluginRef: picker
```

What changed from baseline:
- Three scorers and their 3:2:2 weights in the profile → one delegation scorer at weight 1.0
- PPC/queue/KV are instantiated but NOT in the profile — the delegation scorer calls their `Score()` directly
- Control mode: `clamp(score) × weight` — should produce identical results to baseline
- Adaptive mode: entropy-based adaptive blend — suppresses concentrated scorers

## vLLM Server Arguments

4 instances, Qwen/Qwen3-14B on H100-SXM-80GB, TP=1.

| Argument | Value |
|----------|-------|
| `--model` | `Qwen/Qwen3-14B` |
| `--tensor-parallel-size` | `1` |
| `--gpu-memory-utilization` | `0.75` |
| `--max-model-len` | `40960` |
| `--max-num-seqs` | `256` |
| `--max-num-batched-tokens` | `2048` |
| `--enable-prefix-caching` | (true required — algorithm depends on prefix cache) |
| `--block-size` | `64` |

