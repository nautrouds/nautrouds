package registry

import "sort"

type Strategy uint8

const (
	StrategyRoundRobin Strategy = iota
	StrategyLeastInFlight
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
