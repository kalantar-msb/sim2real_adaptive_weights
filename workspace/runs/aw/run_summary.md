**Run Summary: `aw`**
Generated: 2026-04-23T14:04:03.542546+00:00 | Scenario: adaptive-weights

**Algorithm**
- Source: `algorithms/algorithm_adaptive.go`
- Description: Entropy-based adaptive scorer weights plugin that delegates to PPC/queue/KV sub-scorers

**Translation**
- Plugin type: `adaptive-routing-scorer`
- Files created: `pkg/plugins/scorer/adaptive_routing.go`, `pkg/plugins/scorer/adaptive_routing_test.go`
- Files modified: `pkg/plugins/register.go`
- Review: approved after 1 rounds

**Packages**

- `/Users/kalantar/projects/go.workspace/src/github.com/kalantar-msb/sim2real_adaptive_weights/workspace/runs/aw/cluster/experiment/pipelinerun-experiment.yaml` (sequential)

**Workloads**

- w1_prefix_to_load_shift (x1)
- w2_stationary (x1)
- w3_multi_prefix_contention (x1)

**Checklist**
- [x] Translation complete
- [x] Assembly complete
- [x] validate-assembly passed

**Verdict: READY TO DEPLOY**
