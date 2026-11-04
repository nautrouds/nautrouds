package main

import (
	"encoding/json"
	"fmt"
	"io"
	"math"
	"slices"
	"strings"
)

const (
	saturationThreshold = 0.85
	dispatchLagFloorUs  = 100
	dispatchLagShare    = 0.10
)

type ReportMeta struct {
	Time       string        `json:"time"`
	GitBranch  string        `json:"gitBranch"`
	GitCommit  string        `json:"gitCommit"`
	HostCPU    string        `json:"hostCpu"`
	Status     string        `json:"status"`
	Duration   string        `json:"duration"`
	Warmup     string        `json:"warmup"`
	Reps       int           `json:"reps"`
	Allocation CPUAllocation `json:"-"`
	Failures   []string      `json:"failures"`
}

type Aggregate struct {
	Target           string
	Proxy            string
	Category         string
	Tier             string
	Workload         string
	Reps             int
	RPS              float64
	RPSSpread        float64
	P50Us            float64
	P99Us            float64
	P999Us           float64
	ProxyCPUUsPerReq float64
	ProxyCoresUsed   float64
	ProxyMemMB       float64
	BackendCoresUsed float64
	ClientUtil       float64
	Flags            []string
}

var categoryOrder = []string{"direct", "uds_builtin", "uds_proxy", "tls_direct", "tls_via_nautrouds"}

var categoryLabels = map[string]string{
	"direct":            "client → backend",
	"uds_builtin":       "UDS builtin",
	"uds_proxy":         "UDS → backend",
	"tls_direct":        "TLS edge → backend",
	"tls_via_nautrouds": "TLS edge → Nautrouds → backend",
}

var proxyOrder = []string{"Direct", "Nautrouds", "Caddy", "Nginx"}

func measurementFlags(m Measurement) []string {
	var flags []string
	lg := m.Loadgen
	if lg.Errors+lg.Non2xx > 0 {
		flags = append(flags, fmt.Sprintf("errors(%d)", lg.Errors+lg.Non2xx))
	}
	if lg.ClientUtil >= saturationThreshold {
		flags = append(flags, "client-bound")
	}
	if m.ProxyCPUs > 0 && m.ProxyCPUSec/lg.WallSec/float64(m.ProxyCPUs) >= saturationThreshold {
		flags = append(flags, "proxy-bound")
	}
	if m.BackendCPUs > 0 && m.BackendCPUSec/lg.WallSec/float64(m.BackendCPUs) >= saturationThreshold {
		flags = append(flags, "backend-bound")
	}
	if lg.TargetRPS > 0 && lg.DispatchP99Us > max(dispatchLagFloorUs, dispatchLagShare*lg.P99Us) {
		flags = append(flags, "client-late")
	}
	if lg.TargetRPS > 0 && lg.RPS < 0.95*lg.TargetRPS {
		flags = append(flags, "rate-missed")
	}
	if m.ExpectBodySize >= 0 && math.Abs(lg.BodyBytesAvg-float64(m.ExpectBodySize)) > 0.5 {
		flags = append(flags, "body-mismatch")
	}
	return flags
}

func invalidFlags(m Measurement) []string {
	var invalid []string
	for _, flag := range measurementFlags(m) {
		if strings.HasPrefix(flag, "errors(") || flag == "body-mismatch" || flag == "client-late" {
			invalid = append(invalid, flag)
		}
	}
	return invalid
}

func median(values []float64) float64 {
	if len(values) == 0 {
		return 0
	}
	sorted := slices.Clone(values)
	slices.Sort(sorted)
	mid := len(sorted) / 2
	if len(sorted)%2 == 1 {
		return sorted[mid]
	}
	return (sorted[mid-1] + sorted[mid]) / 2
}

func aggregate(measurements []Measurement) []Aggregate {
	type key struct{ target, tier, workload string }
	groups := make(map[key][]Measurement)
	var order []key
	for _, m := range measurements {
		k := key{m.Target, m.Tier, m.Workload}
		if _, ok := groups[k]; !ok {
			order = append(order, k)
		}
		groups[k] = append(groups[k], m)
	}

	result := make([]Aggregate, 0, len(order))
	for _, k := range order {
		group := groups[k]
		collect := func(f func(Measurement) float64) []float64 {
			values := make([]float64, len(group))
			for i, m := range group {
				values[i] = f(m)
			}
			return values
		}
		rps := collect(func(m Measurement) float64 { return m.Loadgen.RPS })
		agg := Aggregate{
			Target:   group[0].Target,
			Proxy:    group[0].Proxy,
			Category: group[0].Category,
			Tier:     group[0].Tier,
			Workload: group[0].Workload,
			Reps:     len(group),
			RPS:      median(rps),
			P50Us:    median(collect(func(m Measurement) float64 { return m.Loadgen.P50Us })),
			P99Us:    median(collect(func(m Measurement) float64 { return m.Loadgen.P99Us })),
			P999Us:   median(collect(func(m Measurement) float64 { return m.Loadgen.P999Us })),
			ProxyCPUUsPerReq: median(collect(func(m Measurement) float64 {
				return perRequestUs(m.ProxyCPUSec, m.Loadgen.Requests)
			})),
			ProxyCoresUsed:   median(collect(func(m Measurement) float64 { return m.ProxyCPUSec / m.Loadgen.WallSec })),
			ProxyMemMB:       median(collect(func(m Measurement) float64 { return m.ProxyMemMB })),
			BackendCoresUsed: median(collect(func(m Measurement) float64 { return m.BackendCPUSec / m.Loadgen.WallSec })),
			ClientUtil:       median(collect(func(m Measurement) float64 { return m.Loadgen.ClientUtil })),
		}
		if agg.RPS > 0 {
			agg.RPSSpread = (slices.Max(rps) - slices.Min(rps)) / agg.RPS
		}
		for _, m := range group {
			for _, flag := range measurementFlags(m) {
				if !slices.Contains(agg.Flags, flag) {
					agg.Flags = append(agg.Flags, flag)
				}
			}
		}
		result = append(result, agg)
	}
	return result
}

func perRequestUs(cpuSec float64, requests int64) float64 {
	if requests == 0 {
		return 0
	}
	return cpuSec * 1e6 / float64(requests)
}

func renderReport(w io.Writer, measurements []Measurement, workloads []Workload, tiers []Tier, meta ReportMeta) {
	alloc := meta.Allocation
	fmt.Fprintf(w, "# UDS Proxy Benchmark Report\n\n")
	fmt.Fprintf(w, "- Time: %s\n", meta.Time)
	fmt.Fprintf(w, "- Git: `%s` @ `%s`\n", meta.GitBranch, meta.GitCommit)
	if meta.HostCPU != "" {
		fmt.Fprintf(w, "- Host CPU: %s\n", meta.HostCPU)
	}
	fmt.Fprintf(w, "- Measurement: %s per run after %s warmup, %d reps, values are medians across reps\n", meta.Duration, meta.Warmup, meta.Reps)
	fmt.Fprintf(w, "- Physical cores (logical siblings): %s\n", alloc.describeSiblings())
	fmt.Fprintf(w, "- SMT siblings used: %t\n", alloc.UseSMT)
	fmt.Fprintf(w, "- Proxy cpuset per tier: ")
	for i, t := range tiers {
		if i > 0 {
			fmt.Fprintf(w, ", ")
		}
		fmt.Fprintf(w, "`%s` = `%s`", t.Name, t.CPUSet)
	}
	fmt.Fprintf(w, "\n- Backend cpuset: `%s`, client cpuset: `%s`\n", alloc.cpuset(alloc.Backend), alloc.cpuset(alloc.Client))
	fmt.Fprintf(w, "- Test result: **%s**\n", meta.Status)
	for _, f := range meta.Failures {
		fmt.Fprintf(w, "  - %s\n", f)
	}
	fmt.Fprintf(w, "\nFlags: `proxy-bound` / `backend-bound` / `client-bound` = that side used ≥ %.0f%% of its cpuset, so the row is limited by it. "+
		"Rows not marked `proxy-bound` in a throughput workload do not show the proxy's ceiling; compare `proxy CPU µs/req` instead. "+
		"`rate-missed` = achieved < 95%% of the open-loop target. `client-late` = the open-loop dispatcher p99 lag exceeds both %d µs and %.0f%% of the measured p99 latency. "+
		"`body-mismatch` = response size differs from what the backend was asked to send. Rows with `errors`, `client-late` or `body-mismatch` fail the run.\n\n",
		saturationThreshold*100, dispatchLagFloorUs, dispatchLagShare*100)

	aggregates := aggregate(measurements)
	tierRank := map[string]int{"-": -1}
	for i, t := range tiers {
		tierRank[t.Name] = i
	}
	directP50 := make(map[string]float64)
	for _, a := range aggregates {
		if a.Category == "direct" {
			directP50[a.Workload] = a.P50Us
		}
	}

	for _, wl := range workloads {
		var rows []Aggregate
		for _, a := range aggregates {
			if a.Workload == wl.Name {
				rows = append(rows, a)
			}
		}
		if len(rows) == 0 {
			continue
		}
		slices.SortStableFunc(rows, func(a, b Aggregate) int {
			if c := slices.Index(categoryOrder, a.Category) - slices.Index(categoryOrder, b.Category); c != 0 {
				return c
			}
			if c := tierRank[a.Tier] - tierRank[b.Tier]; c != 0 {
				return c
			}
			return slices.Index(proxyOrder, a.Proxy) - slices.Index(proxyOrder, b.Proxy)
		})

		fmt.Fprintf(w, "## %s\n\n`%s`\n\n", wl.Title, wl.Name)
		fmt.Fprintf(w, "| Proxy | Path | Proxy cores | req/s | spread | p50 µs | p99 µs | p99.9 µs | Δp50 vs direct | proxy CPU µs/req | proxy cores used | proxy mem MiB | backend cores used | client util | flags |\n")
		fmt.Fprintf(w, "|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|\n")
		for _, a := range rows {
			delta := "-"
			if base, ok := directP50[a.Workload]; ok && a.Category != "direct" && a.Category != "uds_builtin" {
				delta = fmt.Sprintf("%+.0f", a.P50Us-base)
			}
			proxyCPU, proxyCores, proxyMem := "-", "-", "-"
			if a.Category != "direct" {
				proxyCPU = fmt.Sprintf("%.2f", a.ProxyCPUUsPerReq)
				proxyCores = fmt.Sprintf("%.2f", a.ProxyCoresUsed)
				proxyMem = fmt.Sprintf("%.1f", a.ProxyMemMB)
			}
			backendCores := "-"
			if a.Category != "uds_builtin" {
				backendCores = fmt.Sprintf("%.2f", a.BackendCoresUsed)
			}
			fmt.Fprintf(w, "| %s | %s | %s | %.0f | ±%.1f%% | %.0f | %.0f | %.0f | %s | %s | %s | %s | %s | %.0f%% | %s |\n",
				a.Proxy, categoryLabels[a.Category], a.Tier, a.RPS, a.RPSSpread*50, a.P50Us, a.P99Us, a.P999Us,
				delta, proxyCPU, proxyCores, proxyMem, backendCores, a.ClientUtil*100, strings.Join(a.Flags, ", "))
		}
		fmt.Fprintln(w)
	}
}

func writeRawJSON(w io.Writer, measurements []Measurement, meta ReportMeta) error {
	encoder := json.NewEncoder(w)
	encoder.SetIndent("", "  ")
	return encoder.Encode(struct {
		Meta         ReportMeta    `json:"meta"`
		Measurements []Measurement `json:"measurements"`
	}{meta, measurements})
}
