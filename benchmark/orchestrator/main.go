package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

func main() {
	composeFile := flag.String("compose-file", "docker-compose.yml", "path to docker-compose.yml")
	resultsDir := flag.String("results-dir", "results", "directory to write reports and logs into")
	serverCores := flag.Int("server-cores", 6, "number of cores in the server-side cpuset pool (tiers: 1 / half / all)")
	clientCore := flag.Int("client-core", -1, "core index the client is pinned to (default: right after the server pool)")
	benchtime := flag.String("benchtime", "5s", "value passed to go test -benchtime")
	healthyTimeout := flag.Duration("healthy-timeout", 90*time.Second, "how long to wait for a stage's containers to become healthy")
	stagesFlag := flag.String("stages", "", "comma-separated subset of stage profiles to run (default: all)")
	flag.Parse()

	if *clientCore < 0 {
		*clientCore = *serverCores
	}

	selected := selectStages(*stagesFlag)
	tiers := buildTiers(*serverCores)

	if err := os.MkdirAll(*resultsDir, 0o755); err != nil {
		log.Fatalf("create results dir: %v", err)
	}

	timestamp := time.Now().Format("20060102-150405")

	log.Printf("==> building images once up front")
	if err := composeBuild(*composeFile, os.Environ(), buildableServices...); err != nil {
		log.Fatalf("docker compose build: %v", err)
	}

	var allResults []BenchResult
	var hostCPU string
	status := "PASS"

	total := len(selected) * len(tiers)
	passCount, failCount := 0, 0
	runStart := time.Now()
	n := 0

	for _, stage := range selected {
		for _, tier := range tiers {
			n++
			iterStart := time.Now()
			log.Printf("==> [%d/%d] stage=%s tier=%s (SERVER_CPUSET=%s CLIENT_CPUSET=%d)", n, total, stage.Profile, tier.Name, tier.ServerCPUSet, *clientCore)

			env := append(os.Environ(),
				"SERVER_CPUSET="+tier.ServerCPUSet,
				fmt.Sprintf("CLIENT_CPUSET=%d", *clientCore),
			)

			output, err := runStage(*composeFile, stage, tier, env, *benchtime, *healthyTimeout)
			elapsed := time.Since(iterStart).Round(time.Second)

			if err != nil {
				log.Printf("==> [%d/%d] stage=%s tier=%s FAILED after %s: %v", n, total, stage.Profile, tier.Name, elapsed, err)
				failCount++
				status = "FAIL"
				continue
			}

			if hostCPU == "" {
				hostCPU = extractHostCPU(output)
			}
			allResults = append(allResults, parseBenchOutput(output, tier.Name)...)
			passCount++
			log.Printf("==> [%d/%d] stage=%s tier=%s done in %s", n, total, stage.Profile, tier.Name, elapsed)
		}
	}

	log.Printf("==> all stages done in %s (%d passed, %d failed)", time.Since(runStart).Round(time.Second), passCount, failCount)

	meta := ReportMeta{
		Time:      time.Now().Format("2006-01-02 15:04:05 MST"),
		GitBranch: gitInfo(*composeFile, "rev-parse", "--abbrev-ref", "HEAD"),
		GitCommit: gitInfo(*composeFile, "rev-parse", "--short", "HEAD"),
		HostCPU:   hostCPU,
		Status:    status,
	}

	reportPath := filepath.Join(*resultsDir, timestamp+".md")
	reportFile, err := os.Create(reportPath)
	if err != nil {
		log.Fatalf("create report file: %v", err)
	}
	renderReport(reportFile, allResults, meta)
	reportFile.Close()

	latestPath := filepath.Join(*resultsDir, "latest.md")
	if data, err := os.ReadFile(reportPath); err == nil {
		os.WriteFile(latestPath, data, 0o644)
	}

	log.Printf("==> report written to %s (also updated %s)", reportPath, latestPath)
	if status != "PASS" {
		os.Exit(1)
	}
}

func runStage(composeFile string, stage Stage, tier Tier, env []string, benchtime string, healthyTimeout time.Duration) (string, error) {
	defer func() {
		if err := composeDown(composeFile, stage.Profile, env); err != nil {
			log.Printf("stage=%s tier=%s: docker compose down failed: %v", stage.Profile, tier.Name, err)
		}
	}()

	if err := composeUp(composeFile, stage.Profile, env); err != nil {
		return "", fmt.Errorf("docker compose up: %w", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), healthyTimeout)
	defer cancel()
	if err := waitHealthy(ctx, composeFile, stage.Profile, healthyTimeout); err != nil {
		return "", err
	}

	output, err := composeRunClient(composeFile, stage.Profile, env, stage.BenchRegex, benchtime)
	if err != nil {
		return output, fmt.Errorf("go test: %w", err)
	}
	return output, nil
}

func selectStages(filter string) []Stage {
	if filter == "" {
		return stages
	}
	want := make(map[string]bool)
	for _, name := range strings.Split(filter, ",") {
		want[strings.TrimSpace(name)] = true
	}
	var selected []Stage
	for _, s := range stages {
		if want[s.Profile] {
			selected = append(selected, s)
		}
	}
	return selected
}

func extractHostCPU(output string) string {
	for _, line := range strings.Split(output, "\n") {
		if strings.HasPrefix(line, "cpu: ") {
			return strings.TrimSpace(strings.TrimPrefix(line, "cpu: "))
		}
	}
	return ""
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
