**Run Summary: `awc`**
Generated: 2026-04-23T16:35:14.981562+00:00 | Scenario: adaptive-weights

**Algorithm**
- Source: `algorithms/algorithm_control.go`
- Description: Entropy-based adaptive scorer weight delegation plugin supporting both adaptive and control modes

**Translation**
- Plugin type: `adaptive-routing-scorer`
- Files created: `pkg/plugins/scorer/adaptive_routing.go`, `pkg/plugins/scorer/adaptive_routing_test.go`
- Files modified: `pkg/plugins/register.go`
- Review: 1/1 after 2 rounds

**Packages**

- `/Users/kalantar/projects/go.workspace/src/github.com/kalantar-msb/sim2real_adaptive_weights/workspace/runs/awc/cluster/experiment/pipelinerun-experiment.yaml` (sequential)

**Workloads**

- w1_prefix_to_load_shift (x1)
- w2_stationary (x1)
- w3_multi_prefix_contention (x1)

**Checklist**
- [x] Translation complete
- [x] Assembly complete
- [x] validate-assembly passed

**Verdict: READY TO DEPLOY**
