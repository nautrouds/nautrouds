package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"
)

type LoadgenResult struct {
	Requests      int64   `json:"requests"`
	Errors        int64   `json:"errors"`
	Non2xx        int64   `json:"non2xx"`
	BodyBytesAvg  float64 `json:"bodyBytesAvg"`
	WallSec       float64 `json:"wallSec"`
	RPS           float64 `json:"rps"`
	TargetRPS     float64 `json:"targetRps"`
	MeanUs        float64 `json:"meanUs"`
	P50Us         float64 `json:"p50Us"`
	P90Us         float64 `json:"p90Us"`
	P99Us         float64 `json:"p99Us"`
	P999Us        float64 `json:"p999Us"`
	MaxUs         float64 `json:"maxUs"`
	ClientCPUSec  float64 `json:"clientCpuSec"`
	ClientCores   int     `json:"clientCores"`
	ClientUtil    float64 `json:"clientUtil"`
	DispatchP50Us float64 `json:"dispatchLagP50Us"`
	DispatchP99Us float64 `json:"dispatchLagP99Us"`
}

type Measurement struct {
	Stage          string             `json:"stage"`
	Target         string             `json:"target"`
	Proxy          string             `json:"proxy"`
	Category       string             `json:"category"`
	Tier           string             `json:"tier"`
	Workload       string             `json:"workload"`
	Rep            int                `json:"rep"`
	ExpectBodySize int                `json:"expectBodySize"`
	Loadgen        LoadgenResult      `json:"loadgen"`
	ProxyCPUs      int                `json:"proxyCpus"`
	BackendCPUs    int                `json:"backendCpus"`
	ProxyCPUSec    float64            `json:"proxyCpuSec"`
	BackendCPUSec  float64            `json:"backendCpuSec"`
	ProxyMemMB     float64            `json:"proxyMemMb"`
	BackendMemMB   float64            `json:"backendMemMb"`
	ContainerCPU   map[string]float64 `json:"containerCpuSec"`
}

type stageContainers struct {
	client  string
	proxy   map[string]string
	backend map[string]string
}

type measureConfig struct {
	duration time.Duration
	warmup   time.Duration
}

func loadgenArgs(t Target, w Workload, cfg measureConfig) []string {
	args := []string{
		"loadgen",
		"-target=" + t.Address,
		"-path=" + w.path(t),
		"-conns=" + strconv.Itoa(w.Conns),
		"-duration=" + cfg.duration.String(),
		"-warmup=" + cfg.warmup.String(),
		"-wait-stdin",
	}
	if w.Rate > 0 {
		args = append(args, "-rate="+strconv.FormatFloat(w.Rate, 'f', -1, 64))
	}
	if w.CloseEach {
		args = append(args, "-close")
	}
	if w.Method != "" {
		args = append(args, "-method="+w.Method)
	}
	if w.RequestBody > 0 {
		args = append(args, "-request-body="+strconv.Itoa(w.RequestBody))
	}
	return args
}

func waitTargetsReady(ctx context.Context, client string, targets []Target) error {
	for _, t := range targets {
		for {
			err := probeTarget(ctx, client, t)
			if err == nil {
				break
			}
			select {
			case <-ctx.Done():
				return fmt.Errorf("target %s never became ready: %w", t.Name, err)
			case <-time.After(500 * time.Millisecond):
			}
		}
	}
	return nil
}

func probeTarget(ctx context.Context, client string, t Target) error {
	out, err := exec.CommandContext(ctx, "docker", "exec", client,
		"loadgen", "-target="+t.Address, "-path="+t.BasePath, "-conns=1", "-warmup=0s", "-duration=200ms", "-timeout=2s").Output()
	if err != nil {
		return err
	}
	var result LoadgenResult
	for line := range strings.Lines(string(out)) {
		line = strings.TrimSpace(line)
		if encoded, ok := strings.CutPrefix(line, "RESULT "); ok {
			if err := json.Unmarshal([]byte(encoded), &result); err != nil {
				return err
			}
		}
	}
	expected := Workload{}.expectedBodySize(t)
	switch {
	case result.Requests == 0 || result.Errors+result.Non2xx > 0:
		return fmt.Errorf("%d ok, %d errors, %d non-2xx", result.Requests, result.Errors, result.Non2xx)
	case expected >= 0 && result.BodyBytesAvg != float64(expected):
		return fmt.Errorf("body %.0f bytes, want %d", result.BodyBytesAvg, expected)
	}
	return nil
}

func sumCPU(containers map[string]string, into map[string]float64, sign float64) (float64, error) {
	var total float64
	for service, id := range containers {
		seconds, err := readCPUSeconds(id)
		if err != nil {
			return 0, err
		}
		into[service] += sign * seconds
		total += seconds
	}
	return total, nil
}

func sumMemory(containers map[string]string) float64 {
	var total float64
	for _, id := range containers {
		mb, err := readMemoryMB(id)
		if err != nil {
			log.Printf("memory sample failed: %v", err)
			continue
		}
		total += mb
	}
	return total
}

func runMeasurement(containers stageContainers, t Target, w Workload, cfg measureConfig) (Measurement, error) {
	m := Measurement{Target: t.Name, Proxy: t.Proxy, Category: t.Category, Workload: w.Name, ExpectBodySize: w.expectedBodySize(t), ContainerCPU: map[string]float64{}}

	ctx, cancel := context.WithTimeout(context.Background(), cfg.warmup+cfg.duration+2*time.Minute)
	defer cancel()

	cmd := exec.CommandContext(ctx, "docker", append([]string{"exec", "-i", containers.client}, loadgenArgs(t, w, cfg)...)...)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return m, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return m, err
	}
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		return m, fmt.Errorf("start loadgen: %w", err)
	}

	var proxyBefore, backendBefore float64
	var memorySampler sync.WaitGroup
	var resultLine string
	var sampleErr error

	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 64<<10), 1<<20)
	for scanner.Scan() {
		line := scanner.Text()
		switch {
		case line == "WARM":
			if proxyBefore, sampleErr = sumCPU(containers.proxy, m.ContainerCPU, -1); sampleErr == nil {
				backendBefore, sampleErr = sumCPU(containers.backend, m.ContainerCPU, -1)
			}
			if _, err := io.WriteString(stdin, "\n"); err != nil {
				sampleErr = fmt.Errorf("signal loadgen: %w", err)
			}
			memorySampler.Go(func() {
				time.Sleep(cfg.duration / 2)
				m.ProxyMemMB = sumMemory(containers.proxy)
				m.BackendMemMB = sumMemory(containers.backend)
			})
		case strings.HasPrefix(line, "RESULT "):
			resultLine = strings.TrimPrefix(line, "RESULT ")
		default:
			fmt.Println(line)
		}
	}
	waitErr := cmd.Wait()
	memorySampler.Wait()

	if waitErr != nil {
		return m, fmt.Errorf("loadgen: %w", waitErr)
	}
	if sampleErr != nil {
		return m, sampleErr
	}
	if resultLine == "" {
		return m, fmt.Errorf("loadgen produced no result")
	}
	if err := json.Unmarshal([]byte(resultLine), &m.Loadgen); err != nil {
		return m, fmt.Errorf("decode loadgen result: %w", err)
	}

	proxyAfter, err := sumCPU(containers.proxy, m.ContainerCPU, 1)
	if err != nil {
		return m, err
	}
	backendAfter, err := sumCPU(containers.backend, m.ContainerCPU, 1)
	if err != nil {
		return m, err
	}
	m.ProxyCPUSec = proxyAfter - proxyBefore
	m.BackendCPUSec = backendAfter - backendBefore
	return m, nil
}
