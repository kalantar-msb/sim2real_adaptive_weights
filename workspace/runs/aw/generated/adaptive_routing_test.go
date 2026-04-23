package scorer

import (
	"context"
	"encoding/json"
	"math"
	"testing"

	"k8s.io/apimachinery/pkg/types"
	fwkdl "sigs.k8s.io/gateway-api-inference-extension/pkg/epp/framework/interface/datalayer"
	"sigs.k8s.io/gateway-api-inference-extension/pkg/epp/framework/interface/plugin"
	"sigs.k8s.io/gateway-api-inference-extension/pkg/epp/framework/interface/scheduling"
)

// ---- test helpers ----

// mockEndpoint implements scheduling.Endpoint for testing.
type mockEndpoint struct {
	name    string
	metrics *fwkdl.Metrics
	store   map[string]fwkdl.Cloneable
}

func newMockEndpoint(name string, kvPercent float64, queueSize int) *mockEndpoint {
	return &mockEndpoint{
		name: name,
		metrics: &fwkdl.Metrics{
			KVCacheUsagePercent: kvPercent,
			WaitingQueueSize:    queueSize,
		},
		store: make(map[string]fwkdl.Cloneable),
	}
}

func (m *mockEndpoint) GetMetadata() *fwkdl.EndpointMetadata { return &fwkdl.EndpointMetadata{} }
func (m *mockEndpoint) GetMetrics() *fwkdl.Metrics           { return m.metrics }
func (m *mockEndpoint) String() string                        { return m.name }
func (m *mockEndpoint) Get(key string) (fwkdl.Cloneable, bool) {
	v, ok := m.store[key]
	return v, ok
}
func (m *mockEndpoint) Put(key string, val fwkdl.Cloneable) { m.store[key] = val }
func (m *mockEndpoint) Keys() []string {
	keys := make([]string, 0, len(m.store))
	for k := range m.store {
		keys = append(keys, k)
	}
	return keys
}

// stubScorer is a scorer that returns fixed scores.
type stubScorer struct {
	typedName plugin.TypedName
	scores    map[scheduling.Endpoint]float64
}

func newStubScorer(name string, scores map[scheduling.Endpoint]float64) *stubScorer {
	return &stubScorer{
		typedName: plugin.TypedName{Type: "stub-scorer", Name: name},
		scores:    scores,
	}
}

func (s *stubScorer) TypedName() plugin.TypedName               { return s.typedName }
func (s *stubScorer) Category() scheduling.ScorerCategory       { return scheduling.Distribution }
func (s *stubScorer) Score(_ context.Context, _ *scheduling.CycleState, _ *scheduling.LLMRequest, endpoints []scheduling.Endpoint) map[scheduling.Endpoint]float64 {
	result := make(map[scheduling.Endpoint]float64, len(endpoints))
	for _, ep := range endpoints {
		if score, ok := s.scores[ep]; ok {
			result[ep] = score
		}
	}
	return result
}

// mockHandle implements plugin.Handle for factory testing.
type mockHandle struct {
	plugins map[string]plugin.Plugin
}

func newMockHandle() *mockHandle {
	return &mockHandle{plugins: make(map[string]plugin.Plugin)}
}

func (h *mockHandle) Context() context.Context            { return context.Background() }
func (h *mockHandle) Plugin(name string) plugin.Plugin    { return h.plugins[name] }
func (h *mockHandle) AddPlugin(name string, p plugin.Plugin) { h.plugins[name] = p }
func (h *mockHandle) GetAllPlugins() []plugin.Plugin {
	result := make([]plugin.Plugin, 0, len(h.plugins))
	for _, p := range h.plugins {
		result = append(result, p)
	}
	return result
}
func (h *mockHandle) GetAllPluginsWithNames() map[string]plugin.Plugin { return h.plugins }
func (h *mockHandle) PodList() []types.NamespacedName { return nil }

// ---- Factory Tests ----

func TestAdaptiveRoutingScorerFactory_ValidConfig(t *testing.T) {
	handle := newMockHandle()
	handle.AddPlugin("ppc", newStubScorer("ppc", nil))
	handle.AddPlugin("qd", newStubScorer("qd", nil))
	handle.AddPlugin("kv", newStubScorer("kv", nil))

	params := adaptiveRoutingParameters{
		ScorerNames: []string{"ppc", "qd", "kv"},
		BaseWeights: []float64{3.0, 2.0, 2.0},
		Mode:        "adaptive",
	}
	raw, _ := json.Marshal(params)

	p, err := AdaptiveRoutingScorerFactory("test-scorer", raw, handle)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	scorer, ok := p.(*AdaptiveRoutingScorer)
	if !ok {
		t.Fatal("factory did not return *AdaptiveRoutingScorer")
	}
	if scorer.TypedName().Type != AdaptiveRoutingScorerType {
		t.Errorf("expected type %q, got %q", AdaptiveRoutingScorerType, scorer.TypedName().Type)
	}
	if scorer.TypedName().Name != "test-scorer" {
		t.Errorf("expected name 'test-scorer', got %q", scorer.TypedName().Name)
	}
	if scorer.mode != "adaptive" {
		t.Errorf("expected mode 'adaptive', got %q", scorer.mode)
	}
	if len(scorer.scorers) != 3 {
		t.Errorf("expected 3 sub-scorers, got %d", len(scorer.scorers))
	}
}

func TestAdaptiveRoutingScorerFactory_DefaultMode(t *testing.T) {
	handle := newMockHandle()
	handle.AddPlugin("s1", newStubScorer("s1", nil))

	params := adaptiveRoutingParameters{
		ScorerNames: []string{"s1"},
		BaseWeights: []float64{1.0},
	}
	raw, _ := json.Marshal(params)

	p, err := AdaptiveRoutingScorerFactory("test", raw, handle)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	scorer := p.(*AdaptiveRoutingScorer)
	if scorer.mode != "adaptive" {
		t.Errorf("expected default mode 'adaptive', got %q", scorer.mode)
	}
}

func TestAdaptiveRoutingScorerFactory_ControlMode(t *testing.T) {
	handle := newMockHandle()
	handle.AddPlugin("s1", newStubScorer("s1", nil))

	params := adaptiveRoutingParameters{
		ScorerNames: []string{"s1"},
		BaseWeights: []float64{1.0},
		Mode:        "control",
	}
	raw, _ := json.Marshal(params)

	p, err := AdaptiveRoutingScorerFactory("test", raw, handle)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	scorer := p.(*AdaptiveRoutingScorer)
	if scorer.mode != "control" {
		t.Errorf("expected mode 'control', got %q", scorer.mode)
	}
}

func TestAdaptiveRoutingScorerFactory_NilParams(t *testing.T) {
	handle := newMockHandle()
	_, err := AdaptiveRoutingScorerFactory("test", nil, handle)
	if err == nil {
		t.Fatal("expected error for nil parameters")
	}
}

func TestAdaptiveRoutingScorerFactory_EmptyScorerNames(t *testing.T) {
	handle := newMockHandle()
	params := adaptiveRoutingParameters{
		ScorerNames: []string{},
		BaseWeights: []float64{},
	}
	raw, _ := json.Marshal(params)
	_, err := AdaptiveRoutingScorerFactory("test", raw, handle)
	if err == nil {
		t.Fatal("expected error for empty scorerNames")
	}
}

func TestAdaptiveRoutingScorerFactory_MismatchedLengths(t *testing.T) {
	handle := newMockHandle()
	handle.AddPlugin("s1", newStubScorer("s1", nil))

	params := adaptiveRoutingParameters{
		ScorerNames: []string{"s1"},
		BaseWeights: []float64{1.0, 2.0},
	}
	raw, _ := json.Marshal(params)
	_, err := AdaptiveRoutingScorerFactory("test", raw, handle)
	if err == nil {
		t.Fatal("expected error for mismatched lengths")
	}
}

func TestAdaptiveRoutingScorerFactory_InvalidMode(t *testing.T) {
	handle := newMockHandle()
	handle.AddPlugin("s1", newStubScorer("s1", nil))

	params := adaptiveRoutingParameters{
		ScorerNames: []string{"s1"},
		BaseWeights: []float64{1.0},
		Mode:        "invalid",
	}
	raw, _ := json.Marshal(params)
	_, err := AdaptiveRoutingScorerFactory("test", raw, handle)
	if err == nil {
		t.Fatal("expected error for invalid mode")
	}
}

func TestAdaptiveRoutingScorerFactory_MissingScorerPlugin(t *testing.T) {
	handle := newMockHandle()
	// "s1" not registered in handle

	params := adaptiveRoutingParameters{
		ScorerNames: []string{"s1"},
		BaseWeights: []float64{1.0},
	}
	raw, _ := json.Marshal(params)
	_, err := AdaptiveRoutingScorerFactory("test", raw, handle)
	if err == nil {
		t.Fatal("expected error for missing sub-scorer plugin")
	}
}

func TestAdaptiveRoutingScorerFactory_NegativeWeight(t *testing.T) {
	handle := newMockHandle()
	handle.AddPlugin("s1", newStubScorer("s1", nil))

	params := adaptiveRoutingParameters{
		ScorerNames: []string{"s1"},
		BaseWeights: []float64{-1.0},
	}
	raw, _ := json.Marshal(params)
	_, err := AdaptiveRoutingScorerFactory("test", raw, handle)
	if err == nil {
		t.Fatal("expected error for negative weight")
	}
}

// ---- Score() Tests ----

func TestScore_EmptyEndpoints(t *testing.T) {
	s := &AdaptiveRoutingScorer{
		typedName:   plugin.TypedName{Type: AdaptiveRoutingScorerType, Name: "test"},
		scorers:     []scheduling.Scorer{newStubScorer("s1", nil)},
		baseWeights: []float64{1.0},
		mode:        "adaptive",
	}

	scores := s.Score(context.Background(), &scheduling.CycleState{}, nil, nil)
	if len(scores) != 0 {
		t.Errorf("expected empty scores, got %d", len(scores))
	}
}

func TestScore_ControlMode_SingleScorer(t *testing.T) {
	ep1 := newMockEndpoint("ep1", 0.5, 10)
	ep2 := newMockEndpoint("ep2", 0.3, 5)
	endpoints := []scheduling.Endpoint{ep1, ep2}

	stubScores := map[scheduling.Endpoint]float64{ep1: 0.8, ep2: 0.2}
	stub := newStubScorer("s1", stubScores)

	s := &AdaptiveRoutingScorer{
		typedName:   plugin.TypedName{Type: AdaptiveRoutingScorerType, Name: "test"},
		scorers:     []scheduling.Scorer{stub},
		baseWeights: []float64{1.0},
		mode:        "control",
	}

	scores := s.Score(context.Background(), &scheduling.CycleState{}, nil, endpoints)
	if len(scores) != 2 {
		t.Fatalf("expected 2 scores, got %d", len(scores))
	}
	// Single scorer, weight 1.0 → scores pass through
	assertClose(t, scores[ep1], 0.8, 1e-9, "ep1")
	assertClose(t, scores[ep2], 0.2, 1e-9, "ep2")
}

func TestScore_ControlMode_MultipleScorers(t *testing.T) {
	ep1 := newMockEndpoint("ep1", 0.5, 10)
	ep2 := newMockEndpoint("ep2", 0.3, 5)
	endpoints := []scheduling.Endpoint{ep1, ep2}

	s1Scores := map[scheduling.Endpoint]float64{ep1: 1.0, ep2: 0.0}
	s2Scores := map[scheduling.Endpoint]float64{ep1: 0.0, ep2: 1.0}

	s := &AdaptiveRoutingScorer{
		typedName:   plugin.TypedName{Type: AdaptiveRoutingScorerType, Name: "test"},
		scorers:     []scheduling.Scorer{newStubScorer("s1", s1Scores), newStubScorer("s2", s2Scores)},
		baseWeights: []float64{3.0, 2.0},
		mode:        "control",
	}

	scores := s.Score(context.Background(), &scheduling.CycleState{}, nil, endpoints)
	// ep1: (1.0 * 3/5) + (0.0 * 2/5) = 0.6
	// ep2: (0.0 * 3/5) + (1.0 * 2/5) = 0.4
	assertClose(t, scores[ep1], 0.6, 1e-9, "ep1")
	assertClose(t, scores[ep2], 0.4, 1e-9, "ep2")
}

func TestScore_AdaptiveMode_UniformScores(t *testing.T) {
	ep1 := newMockEndpoint("ep1", 0.5, 10)
	ep2 := newMockEndpoint("ep2", 0.3, 5)
	ep3 := newMockEndpoint("ep3", 0.7, 3)
	endpoints := []scheduling.Endpoint{ep1, ep2, ep3}

	// All scorers return uniform scores → entropy=1 → weights preserve base ratio.
	uniform := map[scheduling.Endpoint]float64{ep1: 0.5, ep2: 0.5, ep3: 0.5}

	s := &AdaptiveRoutingScorer{
		typedName:   plugin.TypedName{Type: AdaptiveRoutingScorerType, Name: "test"},
		scorers:     []scheduling.Scorer{newStubScorer("s1", uniform), newStubScorer("s2", uniform)},
		baseWeights: []float64{3.0, 2.0},
		mode:        "adaptive",
	}

	scores := s.Score(context.Background(), &scheduling.CycleState{}, nil, endpoints)
	// Uniform → entropy=1.0. EMA starts near (1-1/3)=0.667, then updated with alpha=2/(3+1)=0.5.
	// After update: ema = 0.5*0.667 + 0.5*1.0 = 0.833...
	// All endpoints should have the same score.
	if len(scores) != 3 {
		t.Fatalf("expected 3 scores, got %d", len(scores))
	}
	assertClose(t, scores[ep1], scores[ep2], 1e-9, "ep1 vs ep2")
	assertClose(t, scores[ep2], scores[ep3], 1e-9, "ep2 vs ep3")
}

func TestScore_AdaptiveMode_ConcentratedScorer(t *testing.T) {
	ep1 := newMockEndpoint("ep1", 0.5, 10)
	ep2 := newMockEndpoint("ep2", 0.3, 5)
	endpoints := []scheduling.Endpoint{ep1, ep2}

	// s1: concentrated on ep1 (low entropy → weight suppressed)
	// s2: spread evenly (high entropy → weight preserved)
	s1Scores := map[scheduling.Endpoint]float64{ep1: 1.0, ep2: 0.0}
	s2Scores := map[scheduling.Endpoint]float64{ep1: 0.5, ep2: 0.5}

	s := &AdaptiveRoutingScorer{
		typedName:   plugin.TypedName{Type: AdaptiveRoutingScorerType, Name: "test"},
		scorers:     []scheduling.Scorer{newStubScorer("s1", s1Scores), newStubScorer("s2", s2Scores)},
		baseWeights: []float64{3.0, 2.0},
		mode:        "adaptive",
	}

	scores := s.Score(context.Background(), &scheduling.CycleState{}, nil, endpoints)

	// s1 has zero entropy → ema for s1 drops → s1 weight suppressed
	// s2 has max entropy → ema for s2 stays high → s2 weight preserved
	// This should push scores closer together compared to control mode.
	controlScorer := &AdaptiveRoutingScorer{
		typedName:   plugin.TypedName{Type: AdaptiveRoutingScorerType, Name: "test-control"},
		scorers:     []scheduling.Scorer{newStubScorer("s1-ctrl", s1Scores), newStubScorer("s2-ctrl", s2Scores)},
		baseWeights: []float64{3.0, 2.0},
		mode:        "control",
	}
	controlScores := controlScorer.Score(context.Background(), &scheduling.CycleState{}, nil, endpoints)

	// In adaptive mode, the spread between ep1 and ep2 should be smaller.
	adaptiveSpread := scores[ep1] - scores[ep2]
	controlSpread := controlScores[ep1] - controlScores[ep2]
	if adaptiveSpread >= controlSpread {
		t.Errorf("expected adaptive spread (%f) < control spread (%f)", adaptiveSpread, controlSpread)
	}
}

func TestScore_AdaptiveMode_AllZeroScores(t *testing.T) {
	ep1 := newMockEndpoint("ep1", 0.5, 10)
	ep2 := newMockEndpoint("ep2", 0.3, 5)
	endpoints := []scheduling.Endpoint{ep1, ep2}

	// All scorers return zero → should fall back gracefully.
	zeroScores := map[scheduling.Endpoint]float64{ep1: 0.0, ep2: 0.0}
	s := &AdaptiveRoutingScorer{
		typedName:   plugin.TypedName{Type: AdaptiveRoutingScorerType, Name: "test"},
		scorers:     []scheduling.Scorer{newStubScorer("s1", zeroScores), newStubScorer("s2", zeroScores)},
		baseWeights: []float64{3.0, 2.0},
		mode:        "adaptive",
	}

	scores := s.Score(context.Background(), &scheduling.CycleState{}, nil, endpoints)
	// All zero input → all zero output. Should not panic.
	if len(scores) != 2 {
		t.Fatalf("expected 2 scores, got %d", len(scores))
	}
	assertClose(t, scores[ep1], 0.0, 1e-9, "ep1")
	assertClose(t, scores[ep2], 0.0, 1e-9, "ep2")
}

func TestScore_AdaptiveMode_EMAUpdate(t *testing.T) {
	ep1 := newMockEndpoint("ep1", 0.5, 10)
	ep2 := newMockEndpoint("ep2", 0.3, 5)
	endpoints := []scheduling.Endpoint{ep1, ep2}

	// First call: concentrated on ep1
	concentrated := map[scheduling.Endpoint]float64{ep1: 1.0, ep2: 0.0}
	s := &AdaptiveRoutingScorer{
		typedName:   plugin.TypedName{Type: AdaptiveRoutingScorerType, Name: "test"},
		scorers:     []scheduling.Scorer{newStubScorer("s1", concentrated)},
		baseWeights: []float64{1.0},
		mode:        "adaptive",
	}

	// Call Score() once to initialize EMA.
	s.Score(context.Background(), &scheduling.CycleState{}, nil, endpoints)

	// Verify EMA was initialized and updated.
	s.mu.Lock()
	if !s.initialized {
		t.Fatal("expected initialized=true after first Score()")
	}
	ema1 := s.emaSpread[0]
	s.mu.Unlock()

	// Call Score() again.
	s.Score(context.Background(), &scheduling.CycleState{}, nil, endpoints)

	s.mu.Lock()
	ema2 := s.emaSpread[0]
	s.mu.Unlock()

	// Concentrated scorer has entropy=0. EMA should decrease towards 0.
	if ema2 >= ema1 {
		t.Errorf("expected EMA to decrease with concentrated scores: ema1=%f, ema2=%f", ema1, ema2)
	}
}

func TestScore_AdaptiveMode_EMAConvergesForUniform(t *testing.T) {
	ep1 := newMockEndpoint("ep1", 0.5, 10)
	ep2 := newMockEndpoint("ep2", 0.3, 5)
	ep3 := newMockEndpoint("ep3", 0.7, 3)
	ep4 := newMockEndpoint("ep4", 0.2, 8)
	endpoints := []scheduling.Endpoint{ep1, ep2, ep3, ep4}

	uniform := map[scheduling.Endpoint]float64{ep1: 0.5, ep2: 0.5, ep3: 0.5, ep4: 0.5}
	s := &AdaptiveRoutingScorer{
		typedName:   plugin.TypedName{Type: AdaptiveRoutingScorerType, Name: "test"},
		scorers:     []scheduling.Scorer{newStubScorer("s1", uniform)},
		baseWeights: []float64{1.0},
		mode:        "adaptive",
	}

	// Run 50 times to let EMA converge.
	for i := 0; i < 50; i++ {
		s.Score(context.Background(), &scheduling.CycleState{}, nil, endpoints)
	}

	s.mu.Lock()
	ema := s.emaSpread[0]
	s.mu.Unlock()

	// Uniform scores → entropy=1.0. EMA should converge to 1.0.
	assertClose(t, ema, 1.0, 0.01, "EMA for uniform scores")
}

func TestScore_Category(t *testing.T) {
	s := &AdaptiveRoutingScorer{}
	if s.Category() != scheduling.Balance {
		t.Errorf("expected category Balance, got %v", s.Category())
	}
}

func TestClamp01Adaptive(t *testing.T) {
	tests := []struct {
		input    float64
		expected float64
	}{
		{-0.5, 0.0},
		{0.0, 0.0},
		{0.5, 0.5},
		{1.0, 1.0},
		{1.5, 1.0},
	}
	for _, tc := range tests {
		result := clamp01Adaptive(tc.input)
		if math.Abs(result-tc.expected) > 1e-12 {
			t.Errorf("clamp01Adaptive(%f) = %f, want %f", tc.input, result, tc.expected)
		}
	}
}

// ---- helpers ----

func assertClose(t *testing.T, got, want, tol float64, label string) {
	t.Helper()
	if math.Abs(got-want) > tol {
		t.Errorf("%s: got %f, want %f (tolerance %e)", label, got, want, tol)
	}
}
