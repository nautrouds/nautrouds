package registry

import (
	"nautrouds/internal/core/logs"
	"nautrouds/internal/core/registry/loadbalance"
	"os"
	"path/filepath"
	"strings"

	"go.uber.org/zap"
)

const strategyFileSuffix = ".strategy"

var strategyPriority = []struct {
	token    string
	strategy loadbalance.Strategy
}{
	{"least_in_flight", loadbalance.StrategyLeastInFlight},
	{"round_robin", loadbalance.StrategyRoundRobin},
}

func readStrategyAtCreation(baseDir, serviceName string) loadbalance.Strategy {
	dir := filepath.Join(baseDir, serviceName)
	for _, candidate := range strategyPriority {
		path := filepath.Join(dir, candidate.token+strategyFileSuffix)
		_, err := os.Stat(path)
		if err == nil {
			return candidate.strategy
		}
		if !os.IsNotExist(err) {
			logs.Out.Warn("Failed to stat strategy marker file",
				zap.String("service", serviceName), zap.String("path", path), zap.Error(err))
		}
	}
	return loadbalance.StrategyRoundRobin
}

func resolveMarkerStrategy(paths []string, serviceName string) loadbalance.Strategy {
	tokens := make(map[string]struct{}, len(paths))
	for _, path := range paths {
		tokens[strings.TrimSuffix(filepath.Base(path), strategyFileSuffix)] = struct{}{}
	}

	for _, candidate := range strategyPriority {
		if _, ok := tokens[candidate.token]; ok {
			return candidate.strategy
		}
	}

	if len(tokens) > 0 {
		logs.Out.Warn("No recognized strategy marker file for service", zap.String("service", serviceName))
	}
	return loadbalance.StrategyRoundRobin
}

type StrategyHandler struct {
	r *Registry
}

func NewStrategyHandler(r *Registry) *StrategyHandler {
	return &StrategyHandler{r: r}
}

func (*StrategyHandler) Extension() string { return strategyFileSuffix }

func (h *StrategyHandler) ApplyServiceScan(baseDir string, serviceName string, discovered []string) error {
	h.r.setStrategy(serviceName, resolveMarkerStrategy(discovered, serviceName))
	return nil
}

func (h *StrategyHandler) ApplyFullScan(baseDir string, byService map[string]map[string]struct{}) error {
	for _, serviceName := range h.r.serviceNames() {
		var paths []string
		for path := range byService[serviceName] {
			paths = append(paths, path)
		}
		h.r.setStrategy(serviceName, resolveMarkerStrategy(paths, serviceName))
	}
	return nil
}
