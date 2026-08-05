package loadbalance

import (
	"testing"

	"nautrouds/internal/core/registry/forwarder"
)

func makeForwarder(socketPath string) *forwarder.Forwarder {
	failCh := make(chan forwarder.FailureForwarder, 1)
	weight, _ := ParseNodeWeight(socketPath)
	return forwarder.New("svc", socketPath, weight, failCh)
}

func TestRoundRobin_EqualWeightsCycleInOrder(t *testing.T) {
	nodes := []string{"/svc/node-0.sock", "/svc/node-1.sock", "/svc/node-2.sock"}
	forwarders := []*forwarder.Forwarder{
		makeForwarder(nodes[0]),
		makeForwarder(nodes[1]),
		makeForwarder(nodes[2]),
	}
	rr := NewRoundRobin(forwarders)

	first := rr.Get()[0]
	firstIdx := 0
	for i, fwd := range forwarders {
		if fwd == first {
			firstIdx = i
		}
	}

	for i := 1; i < 9; i++ {
		want := forwarders[(firstIdx+i)%len(forwarders)]
		if got := rr.Get()[0]; got != want {
			t.Fatalf("call %d: Get()[0] = %v, want %v", i, got, want)
		}
	}
}

func TestRoundRobin_DistributesProportionallyToWeight(t *testing.T) {
	nodes := []string{"/svc/node-0@1.sock", "/svc/node-1@3.sock"}
	forwarders := []*forwarder.Forwarder{makeForwarder(nodes[0]), makeForwarder(nodes[1])}
	rr := NewRoundRobin(forwarders)

	counts := make(map[*forwarder.Forwarder]int)
	const cycles = 100
	for range cycles * 4 {
		counts[rr.Get()[0]]++
	}

	if counts[forwarders[0]] != cycles {
		t.Errorf("node-0 count = %d, want %d", counts[forwarders[0]], cycles)
	}
	if counts[forwarders[1]] != cycles*3 {
		t.Errorf("node-1 count = %d, want %d", counts[forwarders[1]], cycles*3)
	}
}

func TestRoundRobin_SmoothInterleaving(t *testing.T) {
	nodes := []string{"/svc/node-0@3.sock", "/svc/node-1@1.sock"}
	forwarders := []*forwarder.Forwarder{makeForwarder(nodes[0]), makeForwarder(nodes[1])}
	rr := NewRoundRobin(forwarders)

	want := []*forwarder.Forwarder{
		forwarders[0], forwarders[1], forwarders[0], forwarders[0],
		forwarders[0], forwarders[1], forwarders[0], forwarders[0],
	}
	for i, w := range want {
		if got := rr.Get()[0]; got != w {
			t.Fatalf("call %d: Get()[0] = %v, want %v", i, got, w)
		}
	}
}

func TestRoundRobin_WeightedGetContainsDuplicates(t *testing.T) {
	nodes := []string{"/svc/node-0@3.sock", "/svc/node-1@1.sock"}
	forwarders := []*forwarder.Forwarder{makeForwarder(nodes[0]), makeForwarder(nodes[1])}
	rr := NewRoundRobin(forwarders)

	result := rr.Get()
	if len(result) != 4 {
		t.Fatalf("len(result) = %d, want 4 (sum of weights)", len(result))
	}

	counts := make(map[*forwarder.Forwarder]int)
	for _, fwd := range result {
		counts[fwd]++
	}
	if counts[forwarders[0]] != 3 {
		t.Errorf("forwarders[0] appears %d times, want 3", counts[forwarders[0]])
	}
	if counts[forwarders[1]] != 1 {
		t.Errorf("forwarders[1] appears %d times, want 1", counts[forwarders[1]])
	}
}

func TestRoundRobin_GetReturnsForwardersRotated(t *testing.T) {
	nodes := []string{"/svc/node-0.sock", "/svc/node-1.sock", "/svc/node-2.sock"}
	forwarders := []*forwarder.Forwarder{
		makeForwarder(nodes[0]),
		makeForwarder(nodes[1]),
		makeForwarder(nodes[2]),
	}
	rr := NewRoundRobin(forwarders)

	result := rr.Get()
	if len(result) != len(forwarders) {
		t.Fatalf("len(result) = %d, want %d", len(result), len(forwarders))
	}

	seen := make(map[*forwarder.Forwarder]bool, len(forwarders))
	for _, fwd := range result {
		if seen[fwd] {
			t.Fatalf("forwarder %v returned more than once", fwd)
		}
		seen[fwd] = true
	}
	for _, fwd := range forwarders {
		if !seen[fwd] {
			t.Errorf("forwarder %v missing from result", fwd)
		}
	}
}

func TestRoundRobin_GetEmptyReturnsNil(t *testing.T) {
	rr := NewRoundRobin(nil)
	if got := rr.Get(); got != nil {
		t.Errorf("Get() on empty RoundRobin = %v, want nil", got)
	}
}
