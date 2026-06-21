package test

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestIntegration_Nautrouds(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "nautrouds-integration-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	servicesDir := filepath.Join(tempDir, "services")
	entrypointsDir := filepath.Join(tempDir, "entrypoints")
	err = os.MkdirAll(servicesDir, 0755)
	assert.NoError(t, err)
	err = os.MkdirAll(entrypointsDir, 0755)
	assert.NoError(t, err)

	// Create a dummy backend service directory
	backendSvcDir := filepath.Join(servicesDir, "backend-service")
	err = os.MkdirAll(backendSvcDir, 0755)
	assert.NoError(t, err)

	backendSocketPath := filepath.Join(backendSvcDir, "instance.sock")
	backendLn, err := net.Listen("unix", backendSocketPath)
	if err != nil {
		t.Fatalf("failed to listen on backend socket: %v", err)
	}

	backendServer := &http.Server{
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			data := map[string]interface{}{
				"status":  "backend_ok",
				"path":    r.URL.Path,
				"headers": r.Header,
			}
			json.NewEncoder(w).Encode(data)
		}),
	}
	go backendServer.Serve(backendLn)
	defer backendServer.Close()

	// Create a Ntufile (Routing config)
	configPath := filepath.Join(tempDir, "Ntufile")
	configContent := `
# Route all traffic to the backend service
*/** backend-service

# Virtual services for diagnostics
GET */health $ok("Nautrouds is healthy")
GET */_services $services
GET */_metrics $metrics
`
	err = os.WriteFile(configPath, []byte(configContent), 0644)
	assert.NoError(t, err)

	goBin, err := exec.LookPath("go")
	if err != nil {
		t.Fatalf("go binary not found: %v", err)
	}

	// Get current working directory of the project
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("failed to get working directory: %v", err)
	}

	binExt := ""
	if runtime.GOOS == "windows" {
		binExt = ".exe"
	}

	ntucBin := filepath.Join(tempDir, "ntuc"+binExt)
	cmdBuildNtuc := exec.Command(goBin, "build", "-o", ntucBin, "./cmd/ntuc/main.go")

	cmdBuildNtuc.Dir = filepath.Dir(wd)
	if out, err := cmdBuildNtuc.CombinedOutput(); err != nil {
		t.Fatalf("failed to build ntuc: %v\nOutput: %s", err, string(out))
	}

	nautroudsBin := filepath.Join(tempDir, "nautrouds"+binExt)
	cmdBuildCore := exec.Command(goBin, "build", "-o", nautroudsBin, "./cmd/core/main.go")
	cmdBuildCore.Dir = filepath.Dir(wd)
	if out, err := cmdBuildCore.CombinedOutput(); err != nil {
		t.Fatalf("failed to build nautrouds: %v\nOutput: %s", err, string(out))
	}

	// Start nautrouds core process
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	cmd := exec.CommandContext(ctx, nautroudsBin,
		"-config", configPath,
		"-ntuc", ntucBin,
		"-services", servicesDir,
		"-entrypoint-dir", entrypointsDir,
		"-entrypoint-count", "1",
		"-log-level", "debug",
	)
	cmd.Dir = tempDir

	// Capture process stdout/stderr for troubleshooting
	stderrPipe, err := cmd.StderrPipe()
	assert.NoError(t, err)
	stdoutPipe, err := cmd.StdoutPipe()
	assert.NoError(t, err)

	err = cmd.Start()
	if err != nil {
		t.Fatalf("failed to start nautrouds core: %v", err)
	}

	go func() {
		_, _ = io.Copy(os.Stdout, stdoutPipe)
	}()
	go func() {
		_, _ = io.Copy(os.Stderr, stderrPipe)
	}()

	// Wait for entrypoint to be created
	socketPath := filepath.Join(entrypointsDir, "nautrouds-0.sock")
	found := false
	for i := 0; i < 50; i++ {
		if _, err := os.Stat(socketPath); err == nil {
			found = true
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if !found {
		t.Fatal("socket file not found after timeout")
	}

	// Create a client that dials the UDS
	client := &http.Client{
		Transport: &http.Transport{
			DialContext: func(_ context.Context, _, _ string) (net.Conn, error) {
				return net.Dial("unix", socketPath)
			},
		},
	}

	// Test Virtual Service: GET /health
	t.Run("Virtual Service Health", func(t *testing.T) {
		resp, err := client.Get("http://localhost/health")
		if err != nil {
			t.Fatalf("failed to send request to health endpoint: %v", err)
		}
		defer resp.Body.Close()

		assert.Equal(t, http.StatusOK, resp.StatusCode)
		body, err := io.ReadAll(resp.Body)
		assert.NoError(t, err)
		assert.Contains(t, string(body), "Nautrouds is healthy")
	})

	// Test Proxy to Backend: GET /any-path
	t.Run("Proxy to Backend", func(t *testing.T) {
		resp, err := client.Get("http://localhost/any-path")
		if err != nil {
			t.Fatalf("failed to send request to proxy endpoint: %v", err)
		}
		defer resp.Body.Close()

		assert.Equal(t, http.StatusOK, resp.StatusCode)
		body, err := io.ReadAll(resp.Body)
		assert.NoError(t, err)
		assert.Contains(t, string(body), "backend_ok")
	})

	// Test Middleware: $SetHeader and $PathTrimPrefix
	t.Run("Middleware - SetHeader and PathTrimPrefix", func(t *testing.T) {
		// Update Ntufile to include middleware
		middlewareConfig := `
GET */middleware/* backend-service
    $SetHeader(X-Test-Header, middleware-applied)
    $PathTrimPrefix(/middleware)
`
		err = os.WriteFile(configPath, []byte(configContent+middlewareConfig), 0644)
		assert.NoError(t, err)

		// Wait for hot-reload
		time.Sleep(2 * time.Second)

		resp, err := client.Get("http://localhost/middleware/any-path")
		if err != nil {
			t.Fatalf("failed to send request to middleware endpoint: %v", err)
		}
		defer resp.Body.Close()

		assert.Equal(t, http.StatusOK, resp.StatusCode)

		var res map[string]interface{}
		err = json.NewDecoder(resp.Body).Decode(&res)
		assert.NoError(t, err)

		// Check path (should be trimmed)
		assert.Equal(t, "/any-path", res["path"])

		// Check header
		headers := res["headers"].(map[string]interface{})
		assert.Equal(t, "middleware-applied", headers["X-Test-Header"].([]interface{})[0])
	})

	// Test Hot Reload: Add a new virtual service
	t.Run("Hot Reload - Virtual Service", func(t *testing.T) {
		newConfig := configContent + `
GET */new-virtual-service $ok("hot reload works")
`
		err = os.WriteFile(configPath, []byte(newConfig), 0644)
		assert.NoError(t, err)

		// Wait for hot-reload
		time.Sleep(2 * time.Second)

		resp, err := client.Get("http://localhost/new-virtual-service")
		if err != nil {
			t.Fatalf("failed to send request to new virtual service: %v", err)
		}
		defer resp.Body.Close()

		assert.Equal(t, http.StatusOK, resp.StatusCode)
		body, err := io.ReadAll(resp.Body)
		assert.NoError(t, err)
		assert.Contains(t, string(body), "hot reload works")
	})

	// Test Multiple Backends
	t.Run("Multiple Backends", func(t *testing.T) {
		// Create another backend
		service2Dir := filepath.Join(servicesDir, "service2")
		err = os.MkdirAll(service2Dir, 0755)
		assert.NoError(t, err)

		// Give watcher time to register the new directory
		time.Sleep(500 * time.Millisecond)

		backend2Ln, err := net.Listen("unix", filepath.Join(service2Dir, "instance.sock"))
		if err != nil {
			t.Fatalf("failed to listen on service2 socket: %v", err)
		}
		backend2 := &http.Server{
			Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusOK)
				w.Write([]byte(`{"status":"backend2_ok"}`))
			}),
		}
		go backend2.Serve(backend2Ln)
		defer backend2.Close()

		// Add route for service2
		configWithSvc2 := configContent + `
GET */service2/* service2
`
		err = os.WriteFile(configPath, []byte(configWithSvc2), 0644)
		assert.NoError(t, err)

		// Wait for hot-reload
		time.Sleep(2 * time.Second)

		// Wait for service to be discovered
		foundSvc := false
		for i := 0; i < 400; i++ { // 40 seconds
			resp, err := client.Get("http://localhost/_services")
			if err == nil {
				var svcs map[string][]string
				json.NewDecoder(resp.Body).Decode(&svcs)
				resp.Body.Close()
				if _, ok := svcs["service2"]; ok {
					foundSvc = true
					break
				}
			}
			time.Sleep(100 * time.Millisecond)
		}
		assert.True(t, foundSvc, "service2 should be discovered")

		resp, err := client.Get("http://localhost/service2/test")
		if err != nil {
			t.Fatalf("failed to send request to service2: %v", err)
		}
		defer resp.Body.Close()

		assert.Equal(t, http.StatusOK, resp.StatusCode)
		body, err := io.ReadAll(resp.Body)
		assert.NoError(t, err)
		assert.Contains(t, string(body), "backend2_ok")
	})

	// Test Error Handling Virtual Service
	t.Run("Virtual Service ERR", func(t *testing.T) {
		errConfig := configContent + `
GET */error $err(418,"I am a teapot")
`
		err = os.WriteFile(configPath, []byte(errConfig), 0644)
		assert.NoError(t, err)

		time.Sleep(2 * time.Second)

		resp, err := client.Get("http://localhost/error")
		assert.NoError(t, err)
		defer resp.Body.Close()

		assert.Equal(t, http.StatusTeapot, resp.StatusCode)
		body, err := io.ReadAll(resp.Body)
		assert.NoError(t, err)
		assert.Contains(t, string(body), "I am a teapot")
	})

	// Test Discovery and Metrics
	t.Run("Discovery and Metrics", func(t *testing.T) {
		// Test /_services
		resp, err := client.Get("http://localhost/_services")
		assert.NoError(t, err)
		defer resp.Body.Close()
		assert.Equal(t, http.StatusOK, resp.StatusCode)

		var services map[string][]string
		err = json.NewDecoder(resp.Body).Decode(&services)
		assert.NoError(t, err)
		assert.Contains(t, services, "backend-service")

		// Test /_metrics
		resp, err = client.Get("http://localhost/_metrics")
		assert.NoError(t, err)
		defer resp.Body.Close()
		assert.Equal(t, http.StatusOK, resp.StatusCode)
		body, _ := io.ReadAll(resp.Body)
		assert.Contains(t, string(body), "nautrouds_requests_total")
	})

	// Send SIGINT or cancel context to stop
	if cmd.Process != nil {
		_ = cmd.Process.Signal(syscall.SIGINT)
	}
	cancel()
	_ = cmd.Wait()
}
