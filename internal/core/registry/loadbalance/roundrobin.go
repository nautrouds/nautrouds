package loadbalance

import (
	"nautrouds/internal/core/registry/forwarder"
	"sync/atomic"
)

type RoundRobin struct {
	forwarders []*forwarder.Forwarder
	cursor     atomic.Uint32
}

func NewRoundRobin(forwarders []*forwarder.Forwarder) *RoundRobin {
	n := len(forwarders)
	if n == 0 {
		return &RoundRobin{}
	}

	weights := make([]int, n)
	total := 0
	for i, fwd := range forwarders {
		weights[i] = int(fwd.Weight())
		total += weights[i]
	}

	current := make([]int, n)
	expanded := make([]*forwarder.Forwarder, 0, total)
	for range total {
		best := -1
		for i := range n {
			current[i] += weights[i]
			if best == -1 || current[i] > current[best] {
				best = i
			}
		}
		expanded = append(expanded, forwarders[best])
		current[best] -= total
	}

	return &RoundRobin{forwarders: expanded}
}

func (rr *RoundRobin) Get() []*forwarder.Forwarder {
	n := len(rr.forwarders)
	if n == 0 {
		return nil
	}

	idx := rr.cursor.Add(1)
	start := int(idx) % n
	result := make([]*forwarder.Forwarder, 0, n)
	result = append(result, rr.forwarders[start:]...)
	result = append(result, rr.forwarders[:start]...)
	return result
}
