**Run Summary: `aw`**
Generated: 2026-04-24T15:22:44.631918+00:00 | Scenario: adaptive-weights

**Algorithm**
- Source: `algorithms/algorithm_adaptive.go`
- Description: Entropy-based adaptive scorer weights: delegation scorer calling PPC/queue/KV sub-scorers via plugin.Handle, blending with entropy-adaptive weights (adaptive mode) or static 3:2:2 weights (control mode)

**Translation**
- Plugin type: `adaptive-routing-scorer`
- Files created: `pkg/plugins/scorer/adaptive_routing.go`, `pkg/plugins/scorer/adaptive_routing_test.go`
- Files modified: `pkg/plugins/register.go`
- Review: 1/1 after 2 rounds

**Packages**

- `/Users/kalantar/projects/go.workspace/src/github.com/kalantar-msb/sim2real_adaptive_weights/workspace/runs/aw/cluster/wl-w1-prefix-to-load-shift-baseline/pipelinerun-w1-prefix-to-load-shift-baseline.yaml`
- `/Users/kalantar/projects/go.workspace/src/github.com/kalantar-msb/sim2real_adaptive_weights/workspace/runs/aw/cluster/wl-w1-prefix-to-load-shift-treatment/pipelinerun-w1-prefix-to-load-shift-treatment.yaml`
- `/Users/kalantar/projects/go.workspace/src/github.com/kalantar-msb/sim2real_adaptive_weights/workspace/runs/aw/cluster/wl-w2-stationary-baseline/pipelinerun-w2-stationary-baseline.yaml`
- `/Users/kalantar/projects/go.workspace/src/github.com/kalantar-msb/sim2real_adaptive_weights/workspace/runs/aw/cluster/wl-w2-stationary-treatment/pipelinerun-w2-stationary-treatment.yaml`
- `/Users/kalantar/projects/go.workspace/src/github.com/kalantar-msb/sim2real_adaptive_weights/workspace/runs/aw/cluster/wl-w3-multi-prefix-contention-baseline/pipelinerun-w3-multi-prefix-contention-baseline.yaml`
- `/Users/kalantar/projects/go.workspace/src/github.com/kalantar-msb/sim2real_adaptive_weights/workspace/runs/aw/cluster/wl-w3-multi-prefix-contention-treatment/pipelinerun-w3-multi-prefix-contention-treatment.yaml`

**Workloads**

- w1_prefix_to_load_shift (x1)
- w2_stationary (x1)
- w3_multi_prefix_contention (x1)

**Checklist**
- [x] Translation complete
- [x] Assembly complete
- [x] validate-assembly passed

**Verdict: READY TO DEPLOY**
