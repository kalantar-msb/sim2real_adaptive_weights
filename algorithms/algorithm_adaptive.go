// Entropy-Based Adaptive Scorer Weights
//
// Core algorithm extracted from BLIS simulation for transfer to llm-d.
// No BLIS dependencies — pure Go with standard library only.
//
// Problem: llm-d routes with static weights (e.g. 3:2:2 for prefix:queue:kv).
// When a scorer concentrates traffic on one endpoint (e.g. prefix cache hotspot),
// the fixed weight can't adapt, causing queue buildup that wipes out cache savings.
//
// Solution: dynamically scale each scorer's weight by how evenly it spreads scores
// across endpoints. Concentrated scorers get suppressed; spread scorers get amplified.
// Zero tuned parameters — everything derives from cluster size k.
//
// Math:
//   adaptive_weight[i] = base_weight[i] × entropy(scorer_i)^(k/2)
//   entropy = normalized Shannon entropy of score distribution across endpoints
//   EMA smoothing: alpha = 2/(k+1)
//
// Results (BLIS simulation, Qwen3-14B, 4×H100, 40 QPS):
//   W1 (prefix → cold shift):    -17.6% E2E mean, -27.4% E2E p90
//   W2 (stationary):             -2.3% E2E mean (no regression)
//   W3 (prefix hotspot flip):    -28.3% E2E mean, -33.6% E2E p90

package adaptiverouting

import "math"

// ScorerOutput holds one scorer's [0,1] scores across all endpoints.
type ScorerOutput struct {
	Scores map[string]float64 // endpoint ID → score in [0,1]
}

// AdaptiveWeights maintains EMA state across requests.
type AdaptiveWeights struct {
	BaseWeights []float64 // original scorer weights (e.g. [3,2,2] normalized to [3/7, 2/7, 2/7])
	EMASpread   []float64 // smoothed per-scorer normalized entropy
	Initialized bool
}

// Blend computes adaptive-weighted final scores for each endpoint.
//
// scorerOutputs: one ScorerOutput per scorer, same order as BaseWeights.
// endpointIDs: list of all endpoint IDs (determines k).
//
// Returns: endpoint ID → final blended score.
func (aw *AdaptiveWeights) Blend(scorerOutputs []ScorerOutput, endpointIDs []string) map[string]float64 {
	n := len(aw.BaseWeights)       // number of scorers
	k := float64(len(endpointIDs)) // number of endpoints

	// EMA smoothing factor derived from cluster size.
	// Larger clusters → more stable signal → less smoothing needed.
	alpha := 2.0 / (k + 1.0)

	// Initialize EMA to neutral spread (1 - 1/k).
	if !aw.Initialized {
		aw.EMASpread = make([]float64, n)
		for i := range aw.EMASpread {
			aw.EMASpread[i] = 1.0 - 1.0/k
		}
		aw.Initialized = true
	}

	// --- Step 1: Compute normalized Shannon entropy per scorer ---
	//
	// H = -Σ share × log(share), normalized by log(k) to [0,1].
	// 0 = fully concentrated (one endpoint gets all score).
	// 1 = perfectly uniform (all endpoints equal).
	//
	// Entropy captures how evenly a scorer distributes its signal.
	// A concentrated scorer (one endpoint dominates) has low entropy → weight suppressed.
	// A spread scorer (scores distributed) has high entropy → weight preserved.
	maxEntropy := math.Log(k) // log(k) = entropy of uniform distribution
	for i := 0; i < n; i++ {
		total := 0.0
		for _, id := range endpointIDs {
			total += clamp01(scorerOutputs[i].Scores[id])
		}

		var normEntropy float64
		if total < 1e-12 || maxEntropy < 1e-12 {
			normEntropy = 0.5 // all zeros → uninformative, neutral
		} else {
			var H float64
			for _, id := range endpointIDs {
				share := clamp01(scorerOutputs[i].Scores[id]) / total
				if share > 1e-12 {
					H -= share * math.Log(share)
				}
			}
			normEntropy = H / maxEntropy
			if normEntropy > 1 {
				normEntropy = 1
			}
		}

		// Update EMA — smoothed entropy tracks scorer spread over recent requests.
		aw.EMASpread[i] = (1-alpha)*aw.EMASpread[i] + alpha*normEntropy
	}

	// --- Step 2: Compute adaptive weights ---
	//
	// adaptive_weight[i] = base_weight[i] × entropy^(k/2)
	//
	// The exponent k/2 is derived from cluster size, not tuned:
	//   k=2 → exponent=1 (linear suppression)
	//   k=4 → exponent=2 (quadratic suppression)
	//   k=8 → exponent=4 (aggressive suppression)
	//
	// This balances suppression: strong enough to downweight concentrated scorers,
	// mild enough to preserve useful concentrated signals in stationary workloads.
	exponent := k / 2
	adaptiveWeights := make([]float64, n)
	for i := 0; i < n; i++ {
		adaptiveWeights[i] = aw.BaseWeights[i] * math.Pow(aw.EMASpread[i], exponent)
	}

	// Renormalize weights to sum to 1.
	totalW := 0.0
	for i := 0; i < n; i++ {
		totalW += adaptiveWeights[i]
	}
	if totalW < 1e-12 {
		// All spreads zero — fall back to base weights.
		copy(adaptiveWeights, aw.BaseWeights)
		totalW = 0
		for _, w := range adaptiveWeights {
			totalW += w
		}
	}
	for i := 0; i < n; i++ {
		adaptiveWeights[i] /= totalW
	}

	// --- Step 3: Weighted sum of scorer outputs ---
	scores := make(map[string]float64, len(endpointIDs))
	for i := 0; i < n; i++ {
		for _, id := range endpointIDs {
			scores[id] += clamp01(scorerOutputs[i].Scores[id]) * adaptiveWeights[i]
		}
	}

	return scores
}

func clamp01(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}
