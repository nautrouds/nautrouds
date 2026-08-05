package loadbalance

type Strategy uint8

const (
	StrategyRoundRobin Strategy = iota
	StrategyLeastInFlight
	StrategyP2C
)
