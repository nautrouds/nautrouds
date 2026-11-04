package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

type runConfig struct {
	composeFile    string
	measure        measureConfig
	reps           int
	healthyTimeout time.Duration
	workloads      []Workload
	alloc          CPUAllocation
}

type progress struct {
	done, total int
	start       time.Time
}

func (p *progress) step() string {
	p.done++
	eta := "?"
	if p.done > 1 {
		perRun := time.Since(p.start) / time.Duration(p.done-1)
		eta = (perRun * time.Duration(p.total-p.done+1)).Round(time.Second).String()
	}
	return fmt.Sprintf("[%d/%d, eta %s]", p.done, p.total, eta)
}

func main() {
	composeFile := flag.String("compose-file", "docker-compose.yml", "path to docker-compose.yml")
	resultsDir := flag.String("results-dir", "results", "directory to write reports into")
	proxyCores := flag.Int("proxy-cores", 0, "physical cores in the proxy pool (0 = every core not given to backend/client)")
	backendCores := flag.Int("backend-cores", 1, "physical cores dedicated to the backend")
	clientCores := flag.Int("client-cores", 2, "physical cores dedicated to the load generator")
	useSMT := flag.Bool("smt", false, "also hand out SMT siblings of each allocated physical core")
	duration := flag.Duration("duration", 5*time.Second, "measurement duration per run")
	warmup := flag.Duration("warmup", 2*time.Second, "warmup duration per run")
	reps := flag.Int("reps", 3, "repetitions per (target, tier, workload)")
	ratesFlag := flag.String("rates", "5000,20000", "comma-separated open-loop req/s targets")
	healthyTimeout := flag.Duration("healthy-timeout", 90*time.Second, "how long to wait for a stage's containers to become healthy")
	stagesFlag := flag.String("stages", "", "comma-separated subset of stage profiles to run (default: all)")
	workloadsFlag := flag.String("workloads", "", "comma-separated subset of workloads to run (default: all)")
	flag.Parse()

	rates, err := parseRates(*ratesFlag)
	if err != nil {
		log.Fatal(err)
	}
	selectedStages := filterByName(stages, *stagesFlag, func(s Stage) string { return s.Profile })
	selectedWorkloads := filterByName(buildWorkloads(rates), *workloadsFlag, func(w Workload) string { return w.Name })
	if len(selectedStages) == 0 || len(selectedWorkloads) == 0 {
		log.Fatal("no stages or workloads selected")
	}
	if err := os.MkdirAll(*resultsDir, 0o755); err != nil {
		log.Fatalf("create results dir: %v", err)
	}

	log.Printf("==> building images once up front")
	if err := composeBuild(*composeFile, buildableServices...); err != nil {
		log.Fatalf("docker compose build: %v", err)
	}

	cores, hostCPU, err := detectTopology(*composeFile)
	if err != nil {
		log.Fatal(err)
	}
	alloc, err := allocateCores(cores, *proxyCores, *backendCores, *clientCores, *useSMT)
	if err != nil {
		log.Fatal(err)
	}
	tiers := alloc.tiers()
	log.Printf("==> physical cores %s; proxy tiers %v, backend cpuset %s, client cpuset %s",
		alloc.describeSiblings(), tiers, alloc.cpuset(alloc.Backend), alloc.cpuset(alloc.Client))

	cfg := runConfig{
		composeFile:    *composeFile,
		measure:        measureConfig{duration: *duration, warmup: *warmup},
		reps:           *reps,
		healthyTimeout: *healthyTimeout,
		workloads:      selectedWorkloads,
		alloc:          alloc,
	}

	prog := &progress{total: countRuns(selectedStages, tiers, cfg), start: time.Now()}
	timestamp := time.Now().Format("20060102-150405")
	var measurements []Measurement
	var failures []string

	for _, stage := range selectedStages {
		for _, tier := range stageTiers(stage, tiers) {
			log.Printf("==> stage=%s tier=%s (proxy cpuset %s)", stage.Profile, tier.Name, tier.CPUSet)
			results, stageFailures := runStage(cfg, stage, tier, prog)
			measurements = append(measurements, results...)
			failures = append(failures, stageFailures...)
		}
	}

	status := "PASS"
	if len(failures) > 0 {
		status = "FAIL"
	}
	log.Printf("==> all stages done in %s (%d measurements, %d failures)", time.Since(prog.start).Round(time.Second), len(measurements), len(failures))

	meta := ReportMeta{
		Time:       time.Now().Format("2006-01-02 15:04:05 MST"),
		GitBranch:  gitInfo(*composeFile, "rev-parse", "--abbrev-ref", "HEAD"),
		GitCommit:  gitInfo(*composeFile, "rev-parse", "--short", "HEAD"),
		HostCPU:    hostCPU,
		Status:     status,
		Duration:   duration.String(),
		Warmup:     warmup.String(),
		Reps:       *reps,
		Allocation: alloc,
		Failures:   failures,
	}
	writeReports(*resultsDir, timestamp, measurements, selectedWorkloads, tiers, meta)

	if status != "PASS" {
		os.Exit(1)
	}
}

func stageTiers(stage Stage, tiers []Tier) []Tier {
	if stage.SweepTiers {
		return tiers
	}
	return []Tier{{Name: "-", CPUSet: tiers[len(tiers)-1].CPUSet}}
}

func countRuns(selected []Stage, tiers []Tier, cfg runConfig) int {
	total := 0
	for _, stage := range selected {
		perTier := 0
		for _, t := range stage.Targets {
			for _, w := range cfg.workloads {
				if w.appliesTo(t) {
					perTier++
				}
			}
		}
		total += perTier * len(stageTiers(stage, tiers)) * cfg.reps
	}
	return total
}

func stageEnv(cfg runConfig, tier Tier) []string {
	return append(os.Environ(),
		"PROXY_CPUSET="+tier.CPUSet,
		"PROXY_CPU_COUNT="+strconv.Itoa(max(tier.CPUCount, 1)),
		"BACKEND_CPUSET="+cfg.alloc.cpuset(cfg.alloc.Backend),
		"CLIENT_CPUSET="+cfg.alloc.cpuset(cfg.alloc.Client),
	)
}

func resolveContainers(cfg runConfig, stage Stage, env []string) (stageContainers, error) {
	containers := stageContainers{proxy: map[string]string{}, backend: map[string]string{}}
	var err error
	if containers.client, err = composeContainerID(cfg.composeFile, stage.Profile, env, "client"); err != nil {
		return containers, err
	}
	for _, service := range stage.ProxyServices {
		if containers.proxy[service], err = composeContainerID(cfg.composeFile, stage.Profile, env, service); err != nil {
			return containers, err
		}
	}
	for _, service := range stage.BackendServices {
		if containers.backend[service], err = composeContainerID(cfg.composeFile, stage.Profile, env, service); err != nil {
			return containers, err
		}
	}
	return containers, nil
}

func runStage(cfg runConfig, stage Stage, tier Tier, prog *progress) ([]Measurement, []string) {
	env := stageEnv(cfg, tier)
	label := fmt.Sprintf("stage=%s tier=%s", stage.Profile, tier.Name)
	defer func() {
		if err := composeDown(cfg.composeFile, stage.Profile, env); err != nil {
			log.Printf("%s: docker compose down failed: %v", label, err)
		}
	}()

	if err := composeUp(cfg.composeFile, stage.Profile, env); err != nil {
		return nil, []string{fmt.Sprintf("%s: docker compose up: %v", label, err)}
	}
	ctx, cancel := context.WithTimeout(context.Background(), cfg.healthyTimeout)
	defer cancel()
	if err := waitHealthy(ctx, cfg.composeFile, stage.Profile, env); err != nil {
		return nil, []string{fmt.Sprintf("%s: %v", label, err)}
	}
	containers, err := resolveContainers(cfg, stage, env)
	if err != nil {
		return nil, []string{fmt.Sprintf("%s: %v", label, err)}
	}
	if err := waitTargetsReady(ctx, containers.client, stage.Targets); err != nil {
		return nil, []string{fmt.Sprintf("%s: %v", label, err)}
	}

	proxyCPUs := 0
	if len(stage.ProxyServices) > 0 {
		proxyCPUs = tier.CPUCount
	}
	backendCPUs := 0
	if len(stage.BackendServices) > 0 {
		backendCPUs = len(cfg.alloc.logicalCPUs(cfg.alloc.Backend))
	}

	var measurements []Measurement
	var failures []string
	for rep := 1; rep <= cfg.reps; rep++ {
		for _, target := range stage.Targets {
			for _, workload := range cfg.workloads {
				if !workload.appliesTo(target) {
					continue
				}
				runLabel := fmt.Sprintf("%s target=%s workload=%s rep=%d", label, target.Name, workload.Name, rep)
				log.Printf("==> %s %s", prog.step(), runLabel)
				m, err := runMeasurement(containers, target, workload, cfg.measure)
				if err != nil {
					log.Printf("==> %s FAILED: %v", runLabel, err)
					failures = append(failures, fmt.Sprintf("%s: %v", runLabel, err))
					continue
				}
				m.Stage, m.Tier, m.Rep = stage.Profile, tier.Name, rep
				m.ProxyCPUs, m.BackendCPUs = proxyCPUs, backendCPUs
				log.Printf("    %.0f req/s, p50 %.0fµs, p99 %.0fµs, proxy %.2f CPU µs/req, client util %.0f%% %s",
					m.Loadgen.RPS, m.Loadgen.P50Us, m.Loadgen.P99Us, perRequestUs(m.ProxyCPUSec, m.Loadgen.Requests),
					m.Loadgen.ClientUtil*100, strings.Join(measurementFlags(m), ","))
				measurements = append(measurements, m)
				if invalid := invalidFlags(m); len(invalid) > 0 {
					failures = append(failures, fmt.Sprintf("%s: %s", runLabel, strings.Join(invalid, ",")))
				}
			}
		}
	}
	return measurements, failures
}

func writeReports(resultsDir, timestamp string, measurements []Measurement, workloads []Workload, tiers []Tier, meta ReportMeta) {
	reportPath := filepath.Join(resultsDir, timestamp+".md")
	reportFile, err := os.Create(reportPath)
	if err != nil {
		log.Fatalf("create report file: %v", err)
	}
	renderReport(reportFile, measurements, workloads, tiers, meta)
	reportFile.Close()

	rawPath := filepath.Join(resultsDir, timestamp+".json")
	rawFile, err := os.Create(rawPath)
	if err != nil {
		log.Fatalf("create raw results file: %v", err)
	}
	if err := writeRawJSON(rawFile, measurements, meta); err != nil {
		log.Printf("write raw results: %v", err)
	}
	rawFile.Close()

	latestPath := filepath.Join(resultsDir, "latest.md")
	if data, err := os.ReadFile(reportPath); err == nil {
		os.WriteFile(latestPath, data, 0o644)
	}
	log.Printf("==> report written to %s (raw data %s, also updated %s)", reportPath, rawPath, latestPath)
}

func gitInfo(composeFile string, args ...string) string {
	repoRoot := filepath.Join(filepath.Dir(composeFile), "..")
	cmdArgs := append([]string{"-C", repoRoot}, args...)
	out, err := exec.Command("git", cmdArgs...).Output()
	if err != nil {
		return "unknown"
	}
	return strings.TrimSpace(string(out))
}
