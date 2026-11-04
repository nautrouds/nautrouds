package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

type composeService struct {
	Service string `json:"Service"`
	State   string `json:"State"`
	Health  string `json:"Health"`
}

func decodeComposeServices(out []byte) ([]composeService, error) {
	var services []composeService
	if err := json.Unmarshal(out, &services); err == nil {
		return services, nil
	}
	for _, line := range bytes.Split(bytes.TrimSpace(out), []byte("\n")) {
		line = bytes.TrimSpace(line)
		if len(line) == 0 {
			continue
		}
		var s composeService
		if err := json.Unmarshal(line, &s); err != nil {
			return nil, fmt.Errorf("decode compose ps line %q: %w", line, err)
		}
		services = append(services, s)
	}
	return services, nil
}

func composeCommand(composeFile, profile string, env []string, args ...string) *exec.Cmd {
	fullArgs := append([]string{"compose", "-f", composeFile, "--profile", profile}, args...)
	cmd := exec.Command("docker", fullArgs...)
	cmd.Env = env
	return cmd
}

func runStreaming(cmd *exec.Cmd) error {
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func composeBuild(composeFile string, services ...string) error {
	args := append([]string{"compose", "-f", composeFile, "build"}, services...)
	return runStreaming(exec.Command("docker", args...))
}

func composeUp(composeFile, profile string, env []string) error {
	return runStreaming(composeCommand(composeFile, profile, env, "up", "-d", "--force-recreate"))
}

func composeDown(composeFile, profile string, env []string) error {
	return runStreaming(composeCommand(composeFile, profile, env, "down", "-v", "--timeout", "5"))
}

func composeRunOneOff(composeFile, service string, extraEnv []string, command ...string) (string, error) {
	args := append([]string{"compose", "-f", composeFile, "run", "--rm", "--no-deps", "-T", service}, command...)
	cmd := exec.Command("docker", args...)
	cmd.Env = append(os.Environ(), extraEnv...)
	cmd.Stderr = os.Stderr
	out, err := cmd.Output()
	return string(out), err
}

func composeContainerID(composeFile, profile string, env []string, service string) (string, error) {
	out, err := composeCommand(composeFile, profile, env, "ps", "-q", service).Output()
	if err != nil {
		return "", fmt.Errorf("docker compose ps -q %s: %w", service, err)
	}
	id := strings.TrimSpace(string(out))
	if id == "" {
		return "", fmt.Errorf("no running container for service %q", service)
	}
	return strings.Fields(id)[0], nil
}

func waitHealthy(ctx context.Context, composeFile, profile string, env []string) error {
	for {
		out, err := composeCommand(composeFile, profile, env, "ps", "--format", "json").Output()
		if err != nil {
			return fmt.Errorf("docker compose ps: %w", err)
		}
		services, err := decodeComposeServices(out)
		if err != nil {
			return fmt.Errorf("parse compose ps output: %w", err)
		}
		if len(services) > 0 && allReady(services) {
			return nil
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("timed out waiting for profile %q services to become healthy: %w", profile, ctx.Err())
		case <-time.After(2 * time.Second):
		}
	}
}

func allReady(services []composeService) bool {
	for _, s := range services {
		if s.State != "running" {
			return false
		}
		if s.Health != "" && s.Health != "healthy" {
			return false
		}
	}
	return true
}

func dockerExecOutput(containerID string, command ...string) (string, error) {
	args := append([]string{"exec", containerID}, command...)
	out, err := exec.Command("docker", args...).Output()
	return string(out), err
}

const cgroupCPUScript = `if [ -f /sys/fs/cgroup/cpu.stat ]; then
  awk '$1 == "usage_usec" { print "1000", $2 }' /sys/fs/cgroup/cpu.stat
else
  echo "1 $(cat /sys/fs/cgroup/cpuacct/cpuacct.usage)"
fi`

const cgroupMemoryScript = `if [ -f /sys/fs/cgroup/memory.current ]; then
  echo "1 $(cat /sys/fs/cgroup/memory.current)"
else
  echo "1 $(cat /sys/fs/cgroup/memory/memory.usage_in_bytes)"
fi`

func readCgroupNumber(containerID, script string) (float64, error) {
	out, err := dockerExecOutput(containerID, "sh", "-c", script)
	if err != nil {
		return 0, fmt.Errorf("read cgroup stat from %s: %w", containerID, err)
	}
	fields := strings.Fields(out)
	if len(fields) != 2 {
		return 0, fmt.Errorf("unexpected cgroup stat %q from %s", out, containerID)
	}
	multiplier, err1 := strconv.ParseFloat(fields[0], 64)
	value, err2 := strconv.ParseFloat(fields[1], 64)
	if err1 != nil || err2 != nil {
		return 0, fmt.Errorf("parse cgroup stat %q from %s", out, containerID)
	}
	return multiplier * value, nil
}

func readCPUSeconds(containerID string) (float64, error) {
	ns, err := readCgroupNumber(containerID, cgroupCPUScript)
	return ns / 1e9, err
}

func readMemoryMB(containerID string) (float64, error) {
	bytes, err := readCgroupNumber(containerID, cgroupMemoryScript)
	return bytes / (1 << 20), err
}
