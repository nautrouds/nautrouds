package loadbalance

import (
	"math/rand/v2"
	"nautrouds/internal/core/registry/forwarder"
	"sort"
	"sync/atomic"
)

type loadReporter interface {
	InFlightWeight() int64
}

func SortByLoad[T loadReporter](items []T) []T {
	result := make([]T, len(items))
	copy(result, items)
	sort.SliceStable(result, func(i, j int) bool {
		return result[i].InFlightWeight() < result[j].InFlightWeight()
	})
	return result
}

type LeastInFlight struct {
	forwarders []*forwarder.Forwarder
	cursor     atomic.Uint32
}

func NewLeastInFlight(forwarders []*forwarder.Forwarder) *LeastInFlight {
	return &LeastInFlight{forwarders: forwarders}
}

func (li *LeastInFlight) Get() []*forwarder.Forwarder {
	n := len(li.forwarders)
	if n == 0 {
		return nil
	}

	idx := li.cursor.Add(1)
	start := int(idx) % n
	rotated := make([]*forwarder.Forwarder, 0, n)
	rotated = append(rotated, li.forwarders[start:]...)
	rotated = append(rotated, li.forwarders[:start]...)

	return SortByLoad(rotated)
}

func (li *LeastInFlight) GetP2C() []*forwarder.Forwarder {
	n := len(li.forwarders)
	if n == 0 {
		return nil
	}
	if n == 1 {
		return []*forwarder.Forwarder{li.forwarders[0]}
	}

	i := rand.IntN(n)
	j := rand.IntN(n - 1)
	if j >= i {
		j++
	}
	if li.forwarders[j].InFlightWeight() < li.forwarders[i].InFlightWeight() {
		i, j = j, i
	}

	result := make([]*forwarder.Forwarder, 0, n)
	result = append(result, li.forwarders[i], li.forwarders[j])
	for idx, fwd := range li.forwarders {
		if idx != i && idx != j {
			result = append(result, fwd)
		}
	}
	return result
}
