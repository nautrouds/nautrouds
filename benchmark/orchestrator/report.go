package main

import (
	"bufio"
	"fmt"
	"io"
	"regexp"
	"strconv"
	"strings"
)

type BenchResult struct {
	Proxy      string
	Category   string
	Tier       string
	Iterations int64
	NsPerOp    float64
	P50Ms      float64
	P99Ms      float64
}

var benchLineRe = regexp.MustCompile(`^Benchmark(\S+)\s+(\d+)\s+([\d.]+)\s*ns/op\s+([\d.]+)\s*p50-ms\s+([\d.]+)\s*p99-ms`)

func parseBenchOutput(output, tier string) []BenchResult {
	var results []BenchResult
	scanner := bufio.NewScanner(strings.NewReader(output))
	for scanner.Scan() {
		m := benchLineRe.FindStringSubmatch(scanner.Text())
		if m == nil {
			continue
		}
		category, proxy := classifyBenchmark(m[1])
		if category == "" {
			continue
		}
		iters, _ := strconv.ParseInt(m[2], 10, 64)
		ns, _ := strconv.ParseFloat(m[3], 64)
		p50, _ := strconv.ParseFloat(m[4], 64)
		p99, _ := strconv.ParseFloat(m[5], 64)
		results = append(results, BenchResult{
			Proxy:      proxy,
			Category:   category,
			Tier:       tier,
			Iterations: iters,
			NsPerOp:    ns,
			P50Ms:      p50,
			P99Ms:      p99,
		})
	}
	return results
}

func classifyBenchmark(name string) (category, proxy string) {
	switch {
	case strings.HasSuffix(name, "TLSNautroudsProxy"):
		return "tls_via_nautrouds", strings.TrimSuffix(name, "TLSNautroudsProxy")
	case strings.HasSuffix(name, "TLSDirectProxy"):
		return "tls_direct", strings.TrimSuffix(name, "TLSDirectProxy")
	case strings.HasSuffix(name, "Builtin"):
		return "uds_builtin", strings.TrimSuffix(name, "Builtin")
	case strings.HasSuffix(name, "Proxy"):
		return "uds_proxy", strings.TrimSuffix(name, "Proxy")
	default:
		return "", ""
	}
}

type ReportMeta struct {
	Time      string
	GitBranch string
	GitCommit string
	HostCPU   string
	Status    string
}

type reportCategory struct {
	key     string
	title   string
	proxies []string
}

var reportCategories = []reportCategory{
	{"uds_builtin", "UDS Builtin (proxy responds directly, no backend involved)", []string{"Nautrouds", "Caddy", "Nginx"}},
	{"uds_proxy", "UDS Proxy (proxy forwards to its own backend)", []string{"Nautrouds", "Caddy", "Nginx"}},
	{"tls_via_nautrouds", "TLS Edge -> Nautrouds -> backend (edge proxy terminates TLS, then hands off to Nautrouds over UDS)", []string{"Caddy", "Nginx"}},
	{"tls_direct", "TLS Edge -> backend (edge proxy terminates TLS and forwards over UDS directly, bypassing Nautrouds)", []string{"Caddy", "Nginx"}},
}

var reportTiers = []string{"1", "half", "all"}

func renderReport(w io.Writer, results []BenchResult, meta ReportMeta) {
	fmt.Fprintf(w, "# UDS Proxy Benchmark Report (staged)\n\n")
	fmt.Fprintf(w, "- Time: %s\n", meta.Time)
	fmt.Fprintf(w, "- Git: `%s` @ `%s`\n", meta.GitBranch, meta.GitCommit)
	if meta.HostCPU != "" {
		fmt.Fprintf(w, "- Host CPU: %s\n", meta.HostCPU)
	}
	fmt.Fprintf(w, "- Server cores per tier: 1 / half / all of `SERVER_CPUSET` in docker-compose.yml, one proxy stack up at a time\n")
	fmt.Fprintf(w, "- Test result: **%s**\n\n", meta.Status)

	for _, cat := range reportCategories {
		var rows []BenchResult
		for _, r := range results {
			if r.Category == cat.key {
				rows = append(rows, r)
			}
		}
		if len(rows) == 0 {
			continue
		}
		fmt.Fprintf(w, "## %s\n\n", cat.title)
		fmt.Fprintf(w, "| Proxy | Server Cores | iterations | ns/op | req/s (approx) | p50 (ms) | p99 (ms) |\n")
		fmt.Fprintf(w, "|---|---|---|---|---|---|---|\n")
		for _, tier := range reportTiers {
			for _, proxy := range cat.proxies {
				for _, r := range rows {
					if r.Tier != tier || r.Proxy != proxy {
						continue
					}
					reqps := 0.0
					if r.NsPerOp > 0 {
						reqps = 1e9 / r.NsPerOp
					}
					fmt.Fprintf(w, "| %s | %s | %d | %.0f | %.0f | %.4f | %.4f |\n",
						proxy, tier, r.Iterations, r.NsPerOp, reqps, r.P50Ms, r.P99Ms)
				}
			}
		}
		fmt.Fprintln(w)
	}
}
