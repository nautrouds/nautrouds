package main

import "fmt"

type Stage struct {
	Profile    string
	BenchRegex string
}

var stages = []Stage{
	{"nautrouds-solo", "^BenchmarkNautroudsBuiltin$"},
	{"nautrouds-proxy", "^BenchmarkNautroudsProxy$"},
	{"caddy-solo", "^BenchmarkCaddyBuiltin$"},
	{"caddy-proxy", "^(BenchmarkCaddyProxy|BenchmarkCaddyTLSDirectProxy)$"},
	{"nginx-solo", "^BenchmarkNginxBuiltin$"},
	{"nginx-proxy", "^(BenchmarkNginxProxy|BenchmarkNginxTLSDirectProxy)$"},
	{"caddy-tls", "^BenchmarkCaddyTLSNautroudsProxy$"},
	{"nginx-tls", "^BenchmarkNginxTLSNautroudsProxy$"},
}

var buildableServices = []string{
	"nautrouds", "backend-nautrouds", "backend-caddy", "backend-nginx", "client",
}

type Tier struct {
	Name         string
	ServerCPUSet string
}

func buildTiers(serverCores int) []Tier {
	return []Tier{
		{"1", cpusetRange(1)},
		{"half", cpusetRange(serverCores / 2)},
		{"all", cpusetRange(serverCores)},
	}
}

func cpusetRange(cores int) string {
	if cores <= 1 {
		return "0"
	}
	return fmt.Sprintf("0-%d", cores-1)
}
