package loadbalance

import (
	"testing"

	"nautrouds/internal/core/registry/forwarder"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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
		require.Same(t, want, rr.Get()[0], "call %d", i)
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

	assert.Equal(t, cycles, counts[forwarders[0]])
	assert.Equal(t, cycles*3, counts[forwarders[1]])
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
		require.Same(t, w, rr.Get()[0], "call %d", i)
	}
}

func TestRoundRobin_WeightedGetContainsDuplicates(t *testing.T) {
	nodes := []string{"/svc/node-0@3.sock", "/svc/node-1@1.sock"}
	forwarders := []*forwarder.Forwarder{makeForwarder(nodes[0]), makeForwarder(nodes[1])}
	rr := NewRoundRobin(forwarders)

	result := rr.Get()
	require.Len(t, result, 4)

	counts := make(map[*forwarder.Forwarder]int)
	for _, fwd := range result {
		counts[fwd]++
	}
	assert.Equal(t, 3, counts[forwarders[0]])
	assert.Equal(t, 1, counts[forwarders[1]])
}

func TestRoundRobin_GetReturnsForwardersRotated(t *testing.T) {
	nodes := []string{"/svc/node-0.sock", "/svc/node-1.sock", "/svc/node-2.sock"}
	forwarders := []*forwarder.Forwarder{
		makeForwarder(nodes[0]),
		makeForwarder(nodes[1]),
		makeForwarder(nodes[2]),
	}
	rr := NewRoundRobin(forwarders)

	assert.ElementsMatch(t, forwarders, rr.Get())
}

func TestRoundRobin_GetEmptyReturnsNil(t *testing.T) {
	assert.Nil(t, NewRoundRobin(nil).Get())
}
