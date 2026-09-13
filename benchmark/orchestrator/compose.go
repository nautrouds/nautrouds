package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
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

func composeBuild(composeFile string, env []string, services ...string) error {
	args := append([]string{"compose", "-f", composeFile, "build"}, services...)
	cmd := exec.Command("docker", args...)
	cmd.Env = env
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func composeUp(composeFile, profile string, env []string) error {
	cmd := composeCommand(composeFile, profile, env, "up", "-d")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func composeDown(composeFile, profile string, env []string) error {
	cmd := composeCommand(composeFile, profile, env, "down", "-v")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func composeRunClient(composeFile, profile string, env []string, benchRegex, benchtime string) (string, error) {
	cmd := composeCommand(composeFile, profile, env,
		"run", "--rm", "--no-deps", "client",
		"go", "test", "-bench="+benchRegex, "-benchtime="+benchtime, "-run=^$", "./...",
	)
	var buf bytes.Buffer
	cmd.Stdout = io.MultiWriter(os.Stdout, &buf)
	cmd.Stderr = io.MultiWriter(os.Stderr, &buf)
	err := cmd.Run()
	return buf.String(), err
}

func waitHealthy(ctx context.Context, composeFile, profile string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for {
		out, err := composeCommand(composeFile, profile, os.Environ(), "ps", "--format", "json").Output()
		if err != nil {
			return fmt.Errorf("docker compose ps: %w", err)
		}
		services, err := decodeComposeServices(out)
		if err != nil {
			return fmt.Errorf("parse compose ps output: %w", err)
		}
		if len(services) > 0 && allHealthy(services) {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("timed out waiting for profile %q services to become healthy", profile)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(2 * time.Second):
		}
	}
}

func allHealthy(services []composeService) bool {
	for _, s := range services {
		if s.Health != "healthy" {
			return false
		}
	}
	return true
}
