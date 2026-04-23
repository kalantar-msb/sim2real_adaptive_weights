// Control: Static 3:2:2 Weighted Scoring via Scorer Delegation
//
// This is the CONTROL algorithm for sim2real validation. It uses the exact same
// scorer delegation path as the adaptive algorithm (register one scorer that
// calls PPC/queue/KV sub-scorers via plugin.Handle), but applies static weights
// with no adaptation — replicating GAIE's runScorerPlugins() logic:
//
//   weightedScore[ep] += clamp(score, 0, 1) * weight
//
// See: gateway-api-inference-extension/pkg/epp/scheduling/scheduler_profile.go:160-168
//
// Purpose: If control ≡ baseline, the delegation plumbing is validated.
// Any delta between adaptive and baseline is purely from the algorithm.

package adaptiverouting

// StaticWeights applies fixed weights to sub-scorer outputs.
// Replicates GAIE's runScorerPlugins() accumulation exactly.
type StaticWeights struct {
	Weights []float64 // raw weights, e.g. [3.0, 2.0, 2.0] — NOT normalized (matches GAIE)
}

// Blend computes static-weighted final scores for each endpoint.
//
// GAIE logic (scheduler_profile.go:165-168):
//   for endpoint, score := range scores {
//       weightedScorePerEndpoint[endpoint] += enforceScoreRange(score) * scorer.Weight()
//   }
//
// Weights are raw (not normalized to sum to 1). This is fine because
// the picker uses argmax, which is scale-invariant.
func (sw *StaticWeights) Blend(scorerOutputs []ScorerOutput, endpointIDs []string) map[string]float64 {
	scores := make(map[string]float64, len(endpointIDs))
	for i, w := range sw.Weights {
		for _, id := range endpointIDs {
			s := scorerOutputs[i].Scores[id]
			// enforceScoreRange: clamp to [0,1]
			if s < 0 {
				s = 0
			}
			if s > 1 {
				s = 1
			}
			scores[id] += s * w
		}
	}
	return scores
}
