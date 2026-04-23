// Entropy-Based Adaptive Scorer Weights
//
// A delegation scorer that calls sub-scorers (e.g. PPC, queue, KV) via
// plugin.Handle and blends their outputs with entropy-adaptive weights.
//
// In "adaptive" mode each scorer's weight is scaled by its EMA-smoothed
// normalized Shannon entropy: concentrated scorers (one endpoint dominates)
// get suppressed; spread scorers (scores distributed) get amplified.
//
// In "control" mode the base weights are used unchanged (plain weighted sum).
//
// Math:
//   adaptive_weight[i] = base_weight[i] × entropy(scorer_i)^(k/2)
//   entropy = normalized Shannon entropy of score distribution across endpoints
//   EMA smoothing: alpha = 2/(k+1)
//
// Config parameters (JSON):
//   scorerNames: list of sub-scorer plugin names (must already be instantiated)
//   baseWeights: list of float64 weights, same order as scorerNames
//   mode:        "adaptive" (default) or "control"
package scorer

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"sync"

	"github.com/go-logr/logr"
	"sigs.k8s.io/controller-runtime/pkg/log"
	logutil "sigs.k8s.io/gateway-api-inference-extension/pkg/common/observability/logging"
	"sigs.k8s.io/gateway-api-inference-extension/pkg/epp/framework/interface/plugin"
	"sigs.k8s.io/gateway-api-inference-extension/pkg/epp/framework/interface/scheduling"
)

const (
	// AdaptiveRoutingScorerType is the plugin type string used in EndpointPickerConfig.
	AdaptiveRoutingScorerType = "adaptive-routing-scorer"
)

// adaptiveRoutingParameters maps the JSON config block in EndpointPickerConfig.
type adaptiveRoutingParameters struct {
	ScorerNames []string  `json:"scorerNames"`
	BaseWeights []float64 `json:"baseWeights"`
	Mode        string    `json:"mode"`
}

// compile-time type assertion
var _ scheduling.Scorer = &AdaptiveRoutingScorer{}

// AdaptiveRoutingScorer delegates scoring to sub-scorers and blends their
// outputs using entropy-adaptive weights.
type AdaptiveRoutingScorer struct {
	typedName   plugin.TypedName
	scorers     []scheduling.Scorer
	baseWeights []float64
	mode        string // "adaptive" or "control"

	mu          sync.Mutex
	emaSpread   []float64 // per-scorer EMA of normalized entropy
	initialized bool
}

// AdaptiveRoutingScorerFactory is the factory function registered with the plugin registry.
func AdaptiveRoutingScorerFactory(name string, rawParameters json.RawMessage, handle plugin.Handle) (plugin.Plugin, error) {
	logger := log.Log.WithName(AdaptiveRoutingScorerType)

	if rawParameters == nil {
		return nil, fmt.Errorf("parameters are required for '%s'", AdaptiveRoutingScorerType)
	}

	var params adaptiveRoutingParameters
	if err := json.Unmarshal(rawParameters, &params); err != nil {
		return nil, fmt.Errorf("failed to parse parameters for '%s': %w", AdaptiveRoutingScorerType, err)
	}

	if len(params.ScorerNames) == 0 {
		return nil, fmt.Errorf("'%s': scorerNames must not be empty", AdaptiveRoutingScorerType)
	}
	if len(params.BaseWeights) != len(params.ScorerNames) {
		return nil, fmt.Errorf("'%s': baseWeights length (%d) must match scorerNames length (%d)",
			AdaptiveRoutingScorerType, len(params.BaseWeights), len(params.ScorerNames))
	}
	for i, w := range params.BaseWeights {
		if w < 0 {
			return nil, fmt.Errorf("'%s': baseWeights[%d] must be non-negative, got %f",
				AdaptiveRoutingScorerType, i, w)
		}
	}

	mode := params.Mode
	if mode == "" {
		mode = "adaptive"
	}
	if mode != "adaptive" && mode != "control" {
		return nil, fmt.Errorf("'%s': mode must be 'adaptive' or 'control', got '%s'",
			AdaptiveRoutingScorerType, mode)
	}

	// Look up sub-scorers by name from the plugin handle.
	scorers := make([]scheduling.Scorer, len(params.ScorerNames))
	for i, scorerName := range params.ScorerNames {
		s, err := plugin.PluginByType[scheduling.Scorer](handle, scorerName)
		if err != nil {
			return nil, fmt.Errorf("'%s': sub-scorer '%s' not found: %w",
				AdaptiveRoutingScorerType, scorerName, err)
		}
		scorers[i] = s
	}

	logger.V(logutil.TRACE).Info("created adaptive routing scorer",
		"scorerNames", params.ScorerNames,
		"baseWeights", params.BaseWeights,
		"mode", mode,
	)

	return &AdaptiveRoutingScorer{
		typedName:   plugin.TypedName{Type: AdaptiveRoutingScorerType, Name: name},
		scorers:     scorers,
		baseWeights: params.BaseWeights,
		mode:        mode,
	}, nil
}

// TypedName satisfies plugin.Plugin.
func (s *AdaptiveRoutingScorer) TypedName() plugin.TypedName { return s.typedName }

// Category satisfies scheduling.Scorer. Balance reflects that adaptive weighting
// dynamically trades off between affinity and distribution depending on entropy.
func (s *AdaptiveRoutingScorer) Category() scheduling.ScorerCategory { return scheduling.Balance }

// Score satisfies scheduling.Scorer. It calls each sub-scorer, then blends
// their outputs using either adaptive-entropy or control (fixed) weights.
func (s *AdaptiveRoutingScorer) Score(ctx context.Context, cycleState *scheduling.CycleState, request *scheduling.LLMRequest, endpoints []scheduling.Endpoint) map[scheduling.Endpoint]float64 {
	logger := log.FromContext(ctx).WithName(s.typedName.String())
	traceLogger := logger.V(logutil.TRACE)

	if len(endpoints) == 0 {
		traceLogger.Info("no endpoints to score")
		return map[scheduling.Endpoint]float64{}
	}

	n := len(s.scorers)

	// Collect sub-scorer outputs.
	subScores := make([]map[scheduling.Endpoint]float64, n)
	for i, scorer := range s.scorers {
		subScores[i] = scorer.Score(ctx, cycleState, request, endpoints)
	}

	if s.mode == "control" {
		return s.controlBlend(subScores, endpoints, traceLogger)
	}
	return s.adaptiveBlend(subScores, endpoints, traceLogger, logger)
}

// controlBlend computes a plain weighted sum using base weights (no adaptation).
func (s *AdaptiveRoutingScorer) controlBlend(subScores []map[scheduling.Endpoint]float64, endpoints []scheduling.Endpoint, traceLogger logr.Logger) map[scheduling.Endpoint]float64 {
	totalW := 0.0
	for _, w := range s.baseWeights {
		totalW += w
	}
	if totalW < 1e-12 {
		totalW = 1.0
	}

	scores := make(map[scheduling.Endpoint]float64, len(endpoints))
	for _, ep := range endpoints {
		var sum float64
		for i, ss := range subScores {
			sum += clamp01Adaptive(ss[ep]) * s.baseWeights[i] / totalW
		}
		scores[ep] = sum
	}

	traceLogger.Info("control blend applied", "numEndpoints", len(endpoints))
	return scores
}

// adaptiveBlend computes entropy-adaptive weighted scores.
func (s *AdaptiveRoutingScorer) adaptiveBlend(subScores []map[scheduling.Endpoint]float64, endpoints []scheduling.Endpoint, traceLogger logr.Logger, debugLogger logr.Logger) map[scheduling.Endpoint]float64 {
	n := len(s.scorers)
	k := float64(len(endpoints))

	// EMA smoothing factor derived from cluster size.
	alpha := 2.0 / (k + 1.0)

	s.mu.Lock()
	defer s.mu.Unlock()

	// Initialize EMA to neutral spread (1 - 1/k).
	if !s.initialized {
		s.emaSpread = make([]float64, n)
		for i := range s.emaSpread {
			s.emaSpread[i] = 1.0 - 1.0/k
		}
		s.initialized = true
		traceLogger.Info("EMA initialized", "k", k, "initialSpread", s.emaSpread[0])
	}

	// Step 1: Compute normalized Shannon entropy per scorer and update EMA.
	maxEntropy := math.Log(k) // log(k) = entropy of uniform distribution
	for i := 0; i < n; i++ {
		total := 0.0
		for _, ep := range endpoints {
			total += clamp01Adaptive(subScores[i][ep])
		}

		var normEntropy float64
		if total < 1e-12 || maxEntropy < 1e-12 {
			// All zeros or single endpoint → uninformative, use neutral value.
			normEntropy = 0.5
			traceLogger.Info("scorer all-zero or single endpoint, using neutral entropy",
				"scorer", s.scorers[i].TypedName().String(), "total", total)
		} else {
			var H float64
			for _, ep := range endpoints {
				share := clamp01Adaptive(subScores[i][ep]) / total
				if share > 1e-12 {
					H -= share * math.Log(share)
				}
			}
			normEntropy = H / maxEntropy
			if normEntropy > 1 {
				normEntropy = 1
			}
		}

		s.emaSpread[i] = (1-alpha)*s.emaSpread[i] + alpha*normEntropy
		traceLogger.Info("entropy update",
			"scorer", s.scorers[i].TypedName().String(),
			"normEntropy", normEntropy,
			"emaSpread", s.emaSpread[i])
	}

	// Step 2: Compute adaptive weights = base_weight × entropy^(k/2), renormalized.
	exponent := k / 2
	adaptiveWeights := make([]float64, n)
	for i := 0; i < n; i++ {
		adaptiveWeights[i] = s.baseWeights[i] * math.Pow(s.emaSpread[i], exponent)
	}

	totalW := 0.0
	for _, w := range adaptiveWeights {
		totalW += w
	}
	if totalW < 1e-12 {
		// All spreads zero → fall back to base weights.
		debugLogger.V(logutil.DEBUG).Info("adaptive weights all zero, falling back to base weights")
		copy(adaptiveWeights, s.baseWeights)
		totalW = 0
		for _, w := range adaptiveWeights {
			totalW += w
		}
	}
	for i := range adaptiveWeights {
		adaptiveWeights[i] /= totalW
	}

	traceLogger.Info("adaptive weights computed",
		"exponent", exponent,
		"weights", adaptiveWeights)

	// Step 3: Weighted sum of sub-scorer outputs.
	scores := make(map[scheduling.Endpoint]float64, len(endpoints))
	for _, ep := range endpoints {
		var sum float64
		for i := 0; i < n; i++ {
			sum += clamp01Adaptive(subScores[i][ep]) * adaptiveWeights[i]
		}
		scores[ep] = sum
	}

	return scores
}

// clamp01Adaptive clamps a float64 to [0, 1].
func clamp01Adaptive(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}
