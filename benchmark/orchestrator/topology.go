package main

import (
	"bufio"
	"fmt"
	"slices"
	"strconv"
	"strings"
)

type PhysicalCore struct {
	Package int
	CoreID  int
	Threads []int
}

type CPUAllocation struct {
	Proxy   []PhysicalCore
	Backend []PhysicalCore
	Client  []PhysicalCore
	UseSMT  bool
}

type Tier struct {
	Name     string
	Cores    int
	CPUSet   string
	CPUCount int
}

const topologyScript = `grep -m1 "model name" /proc/cpuinfo
for d in /sys/devices/system/cpu/cpu[0-9]*; do
  [ -f "$d/online" ] && [ "$(cat "$d/online")" = "0" ] && continue
  echo "${d##*/cpu} $(cat "$d/topology/physical_package_id") $(cat "$d/topology/core_id")"
done`

func detectTopology(composeFile string) ([]PhysicalCore, string, error) {
	out, err := composeRunOneOff(composeFile, "client", []string{"CLIENT_CPUSET=0"}, "sh", "-c", topologyScript)
	if err != nil {
		return nil, "", fmt.Errorf("read cpu topology: %w", err)
	}
	cores, err := parseTopology(out)
	return cores, parseCPUModel(out), err
}

func parseCPUModel(out string) string {
	for _, line := range strings.Split(out, "\n") {
		if name, value, ok := strings.Cut(line, ":"); ok && strings.TrimSpace(name) == "model name" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func parseTopology(out string) ([]PhysicalCore, error) {
	type key struct{ pkg, core int }
	cores := make(map[key]*PhysicalCore)
	var order []key

	scanner := bufio.NewScanner(strings.NewReader(out))
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) != 3 {
			continue
		}
		var nums [3]int
		valid := true
		for i, f := range fields {
			n, err := strconv.Atoi(f)
			if err != nil {
				valid = false
				break
			}
			nums[i] = n
		}
		if !valid {
			continue
		}
		k := key{nums[1], nums[2]}
		if _, ok := cores[k]; !ok {
			cores[k] = &PhysicalCore{Package: k.pkg, CoreID: k.core}
			order = append(order, k)
		}
		cores[k].Threads = append(cores[k].Threads, nums[0])
	}
	if len(order) == 0 {
		return nil, fmt.Errorf("no cpu topology parsed from %q", out)
	}

	result := make([]PhysicalCore, 0, len(order))
	for _, k := range order {
		c := *cores[k]
		slices.Sort(c.Threads)
		result = append(result, c)
	}
	slices.SortFunc(result, func(a, b PhysicalCore) int { return a.Threads[0] - b.Threads[0] })
	return result, nil
}

func allocateCores(cores []PhysicalCore, proxyCores, backendCores, clientCores int, useSMT bool) (CPUAllocation, error) {
	if backendCores < 1 || clientCores < 1 {
		return CPUAllocation{}, fmt.Errorf("backend and client need at least one physical core each")
	}
	available := len(cores) - backendCores - clientCores
	if proxyCores <= 0 {
		proxyCores = available
	}
	if proxyCores < 1 || proxyCores > available {
		return CPUAllocation{}, fmt.Errorf("need %d proxy + %d backend + %d client physical cores, host has %d",
			proxyCores, backendCores, clientCores, len(cores))
	}
	return CPUAllocation{
		Proxy:   cores[:proxyCores],
		Backend: cores[proxyCores : proxyCores+backendCores],
		Client:  cores[proxyCores+backendCores : proxyCores+backendCores+clientCores],
		UseSMT:  useSMT,
	}, nil
}

func (a CPUAllocation) logicalCPUs(cores []PhysicalCore) []int {
	var cpus []int
	for _, c := range cores {
		if a.UseSMT {
			cpus = append(cpus, c.Threads...)
		} else {
			cpus = append(cpus, c.Threads[0])
		}
	}
	return cpus
}

func (a CPUAllocation) cpuset(cores []PhysicalCore) string {
	cpus := a.logicalCPUs(cores)
	parts := make([]string, len(cpus))
	for i, c := range cpus {
		parts[i] = strconv.Itoa(c)
	}
	return strings.Join(parts, ",")
}

func (a CPUAllocation) tiers() []Tier {
	n := len(a.Proxy)
	counts := slices.Compact([]int{1, (n + 1) / 2, n})
	tiers := make([]Tier, 0, len(counts))
	for _, count := range counts {
		cores := a.Proxy[:count]
		tiers = append(tiers, Tier{
			Name:     fmt.Sprintf("%dc", count),
			Cores:    count,
			CPUSet:   a.cpuset(cores),
			CPUCount: len(a.logicalCPUs(cores)),
		})
	}
	return tiers
}

func (a CPUAllocation) describeSiblings() string {
	var parts []string
	for _, c := range append(append(append([]PhysicalCore{}, a.Proxy...), a.Backend...), a.Client...) {
		threads := make([]string, len(c.Threads))
		for i, t := range c.Threads {
			threads[i] = strconv.Itoa(t)
		}
		parts = append(parts, "{"+strings.Join(threads, ",")+"}")
	}
	return strings.Join(parts, " ")
}
