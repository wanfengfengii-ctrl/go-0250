package service

import (
	"context"
	"sync"
)

// StaticInstrument is a controllable adapter that always reports success with a
// fixed payload. It is the default adapter wired into the running service; the
// scripted variant below is used to exercise fault sequences.
type StaticInstrument struct{}

// Call always returns an "ok" status.
func (StaticInstrument) Call(_ context.Context, _ string) (Result, error) {
	return Result{Status: "ok", Payload: "ok"}, nil
}

// ScriptedInstrument replays a fixed sequence of instrument outcomes, one per
// call. It is the in-process adapter through which tests (and the fault-injection
// harness) receive success, reject, disconnect, timeout and malformed sequences.
type ScriptedInstrument struct {
	mu      sync.Mutex
	results []Result
	next    int
	calls   int
}

// NewScriptedInstrument builds a scripted adapter from a sequence of results.
func NewScriptedInstrument(results []Result) *ScriptedInstrument {
	return &ScriptedInstrument{results: results}
}

// Enqueue appends a result to the script.
func (s *ScriptedInstrument) Enqueue(r Result) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.results = append(s.results, r)
}

// Calls returns the number of calls served so far.
func (s *ScriptedInstrument) Calls() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls
}

// Call pops the next scripted result, or returns "ok" once the script is
// exhausted (so a long-running test does not deadlock on a missing result).
func (s *ScriptedInstrument) Call(_ context.Context, _ string) (Result, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls++
	if s.next < len(s.results) {
		r := s.results[s.next]
		s.next++
		return r, nil
	}
	return Result{Status: "ok", Payload: "ok"}, nil
}
