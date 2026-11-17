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
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const hotReloadTimeout = 10 * time.Second

func serveUnixHTTP(t *testing.T, socketPath string, handler http.HandlerFunc) {
	t.Helper()
	ln, err := net.Listen("unix", socketPath)
	require.NoError(t, err)

	server := &http.Server{Handler: handler}
	go server.Serve(ln)
	t.Cleanup(func() { server.Close() })
}

func buildBinary(t *testing.T, outDir, name, pkg string) string {
	t.Helper()
	goBin, err := exec.LookPath("go")
	require.NoError(t, err)

	wd, err := os.Getwd()
	require.NoError(t, err)

	binExt := ""
	if runtime.GOOS == "windows" {
		binExt = ".exe"
	}

	binPath := filepath.Join(outDir, name+binExt)
	cmd := exec.Command(goBin, "build", "-o", binPath, pkg)
	cmd.Dir = filepath.Dir(wd)
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, string(out))
	return binPath
}

func TestIntegration_Nautrouds(t *testing.T) {
	tempDir := t.TempDir()

	servicesDir := filepath.Join(tempDir, "services")
	entrypointsDir := filepath.Join(tempDir, "entrypoints")
	backendSvcDir := filepath.Join(servicesDir, "backend-service")
	require.NoError(t, os.MkdirAll(entrypointsDir, 0755))
	require.NoError(t, os.MkdirAll(backendSvcDir, 0755))

	backendSocket := filepath.Join(backendSvcDir, "instance.sock")
	serveUnixHTTP(t, backendSocket, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]any{
			"status":  "backend_ok",
			"path":    r.URL.Path,
			"headers": r.Header,
		})
	})

	configPath := filepath.Join(tempDir, "Ntufile")
	configContent := `
# Route all traffic to the backend service
*/** backend-service

# Virtual services for diagnostics
GET */health $ok("Nautrouds is healthy")
GET */_services $services
GET */_metrics $metrics
`
	writeConfig := func(t *testing.T, extra string) {
		require.NoError(t, os.WriteFile(configPath, []byte(configContent+extra), 0644))
	}
	writeConfig(t, "")

	ntucBin := buildBinary(t, tempDir, "ntuc", "./cmd/ntuc")
	nautroudsBin := buildBinary(t, tempDir, "nautrouds", "./cmd/core")

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
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	require.NoError(t, cmd.Start())
	defer func() {
		cmd.Process.Signal(syscall.SIGINT)
		cancel()
		cmd.Wait()
	}()

	socketPath := filepath.Join(entrypointsDir, "nautrouds-0.sock")
	require.Eventually(t, func() bool {
		_, err := os.Stat(socketPath)
		return err == nil
	}, 5*time.Second, 100*time.Millisecond, "entrypoint socket not created")

	client := &http.Client{
		Transport: &http.Transport{
			DialContext: func(_ context.Context, _, _ string) (net.Conn, error) {
				return net.Dial("unix", socketPath)
			},
		},
	}

	do := func(req *http.Request) (int, string) {
		resp, err := client.Do(req)
		if err != nil {
			return 0, ""
		}
		defer resp.Body.Close()

		body, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, string(body)
	}
	get := func(path string) (int, string) {
		req, _ := http.NewRequest("GET", "http://localhost"+path, nil)
		return do(req)
	}
	post := func(path, body string, contentLength int64) (int, string) {
		req, _ := http.NewRequest("POST", "http://localhost"+path, strings.NewReader(body))
		req.ContentLength = contentLength
		return do(req)
	}
	waitFor := func(t *testing.T, timeout time.Duration, cond func() bool) {
		t.Helper()
		require.Eventually(t, cond, timeout, 100*time.Millisecond)
	}

	t.Run("Virtual Service Health", func(t *testing.T) {
		code, body := get("/health")
		assert.Equal(t, http.StatusOK, code)
		assert.Contains(t, body, "Nautrouds is healthy")
	})

	t.Run("Proxy to Backend", func(t *testing.T) {
		code, body := get("/any-path")
		assert.Equal(t, http.StatusOK, code)
		assert.Contains(t, body, "backend_ok")
	})

	t.Run("Middleware - SetHeader and PathTrimPrefix", func(t *testing.T) {
		writeConfig(t, `
GET */middleware/* backend-service
    $SetHeader(X-Test-Header, middleware-applied)
    $PathTrimPrefix(/middleware)
`)

		var body string
		waitFor(t, hotReloadTimeout, func() bool {
			_, body = get("/middleware/any-path")
			return strings.Contains(body, `"path":"/any-path"`)
		})

		var res struct {
			Headers http.Header `json:"headers"`
		}
		require.NoError(t, json.Unmarshal([]byte(body), &res))
		assert.Equal(t, "middleware-applied", res.Headers.Get("X-Test-Header"))
	})

	t.Run("Middleware - BodySizeLimit", func(t *testing.T) {
		const oversized = "this body is way over the ten byte limit"
		writeConfig(t, `
POST */upload backend-service
    $BodySizeLimit(10)
`)

		waitFor(t, hotReloadTimeout, func() bool {
			code, _ := post("/upload", oversized, int64(len(oversized)))
			return code == http.StatusRequestEntityTooLarge
		})

		code, _ := post("/upload", "tiny", int64(len("tiny")))
		assert.Equal(t, http.StatusOK, code, "within limit")

		code, _ = post("/upload", oversized, -1)
		assert.Equal(t, http.StatusRequestEntityTooLarge, code, "chunked body exceeds limit")
	})

	t.Run("Hot Reload - Virtual Service", func(t *testing.T) {
		writeConfig(t, `
GET */new-virtual-service $ok("hot reload works")
`)

		waitFor(t, hotReloadTimeout, func() bool {
			code, body := get("/new-virtual-service")
			return code == http.StatusOK && strings.Contains(body, "hot reload works")
		})
	})

	t.Run("Multiple Backends", func(t *testing.T) {
		service2Dir := filepath.Join(servicesDir, "service2")
		require.NoError(t, os.MkdirAll(service2Dir, 0755))

		// Give watcher time to register the new directory
		time.Sleep(500 * time.Millisecond)

		service2Socket := filepath.Join(service2Dir, "instance.sock")
		serveUnixHTTP(t, service2Socket, func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`{"status":"backend2_ok"}`))
		})

		writeConfig(t, `
GET */service2/* service2
`)

		waitFor(t, 40*time.Second, func() bool {
			code, body := get("/service2/test")
			return code == http.StatusOK && strings.Contains(body, "backend2_ok")
		})
	})

	t.Run("Virtual Service ERR", func(t *testing.T) {
		writeConfig(t, `
GET */error $err(418,"I am a teapot")
`)

		waitFor(t, hotReloadTimeout, func() bool {
			code, body := get("/error")
			return code == http.StatusTeapot && strings.Contains(body, "I am a teapot")
		})
	})

	t.Run("Discovery and Metrics", func(t *testing.T) {
		code, body := get("/_services")
		require.Equal(t, http.StatusOK, code)

		var services map[string][]string
		require.NoError(t, json.Unmarshal([]byte(body), &services))
		assert.Contains(t, services, "backend-service")

		code, body = get("/_metrics")
		assert.Equal(t, http.StatusOK, code)
		assert.Contains(t, body, "nautrouds_requests_total")
	})
}
