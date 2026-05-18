package main

import (
	"context"
	"fmt"
	"nautrouds/internal/core/configwatcher"
	"nautrouds/internal/core/logs"
	"nautrouds/internal/core/metrics"
	"nautrouds/internal/core/options"
	"nautrouds/internal/core/proxy"
	"nautrouds/internal/core/registry"
	"nautrouds/internal/core/watcher"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/gofrs/flock"

	"github.com/lucap9056/go-lifecycle/lifecycle"
	"go.uber.org/zap"
)

func getEnv(key, fallback string) string {
	if value, ok := os.LookupEnv(key); ok {
		return value
	}
	return fallback
}

func main() {
	// 1. Define and parse flags
	opts := options.Load()

	logs.InitLogger(opts.LogLevel)
	defer logs.Sync()

	// Create entrypoint directory if it doesn't exist
	if err := os.MkdirAll(opts.EntrypointDir, 0755); err != nil {
		logs.Out.Error("Failed to create entrypoint directory", zap.Error(err))
		return
	}
	// Ensure permissions if directory already existed
	os.Chmod(opts.EntrypointDir, 0755)

	// Create services directory if it doesn't exist
	// 01777: Sticky bit ensures only the owner can delete their own socket
	if err := os.MkdirAll(opts.ServicesDir, 01777); err != nil {
		logs.Out.Error("Failed to create services directory", zap.Error(err))
		return
	}
	os.Chmod(opts.ServicesDir, 01777)

	lc := lifecycle.New()

	// 2. Initialize Registry and Watcher for dynamic node discovery
	reg, err := registry.NewRegistry(opts.ServicesDir)
	if err != nil {
		logs.Out.Error("Failed to initialize registry", zap.Error(err))
		return
	}

	manager := proxy.NewManager(reg)

	// 3. Initialize Config Watcher (Handles load & hot-reload)
	cw, err := configwatcher.NewConfigWatcher(opts.ConfigPath, opts.NtlcPath, manager)
	if err != nil {
		logs.Out.Error("Failed to initialize config watcher", zap.Error(err))
		return
	}
	if err := cw.LoadInitial(); err != nil {
		logs.Out.Error("Failed to load initial route table", zap.Error(err))
		return
	}

	w, err := watcher.NewWatcher(reg)
	if err != nil {
		logs.Out.Error("Failed to initialize config watcher", zap.Error(err))
		return
	}

	collector := metrics.NewCollector(filepath.Join(opts.ServicesDir, "metrics.sock"), metrics.Global)
	if err := collector.Start(); err != nil {
		logs.Out.Error("Failed to start metrics collector", zap.Error(err))
		return
	}

	// 4. Register Cleanup Hook
	lc.OnExit(func() {
		collector.Stop()
		w.Close()
		cw.Close()
	})

	// 5. Start All Watchers and Listener
	if err := cw.Start(); err != nil {
		logs.Out.Error("Failed to start config watcher", zap.Error(err))
		return
	}

	if err := w.Start(); err != nil {
		logs.Out.Error("Failed to start watcher", zap.Error(err))
		return
	}

	if err := createEntrypoints(lc, manager, opts); err != nil {
		logs.Out.Error("Failed to create entrypoints", zap.Error(err))
		return
	}

	lc.Wait()
}

func createEntrypoints(lc *lifecycle.LifecycleManager, manager *proxy.Manager, opts *options.Options) error {
	fileLockCtx, fileLockCancel := context.WithTimeout(context.Background(), time.Second*1)
	defer fileLockCancel()
	fileLock := flock.New(filepath.Join(opts.EntrypointDir, "nautrouds.lock"))
	locked, err := fileLock.TryLockContext(fileLockCtx, 200*time.Millisecond)
	if err != nil || !locked {
		return fmt.Errorf("failed to acquire lock: %w", err)
	}
	defer fileLock.Unlock()

	hasToken := len(opts.Token) > 0
	token := "-"
	if hasToken {
		token = fmt.Sprintf("-%s-", opts.Token)
	}
	if hasToken || opts.ForceClean {
		err := cleanLegacySockets(opts.EntrypointDir, token, opts.ForceClean)
		if err != nil {
			return fmt.Errorf("failed to clean legacy sockets: %w", err)
		}
	}

	socketPathMap := make(map[string]context.CancelFunc, opts.EntrypointCount)
	offset := 0
	for i := 0; i < opts.EntrypointCount; {
		socketName := fmt.Sprintf("nautrouds%s%d.sock", token, i+offset)
		socketPath := filepath.Join(opts.EntrypointDir, socketName)

		if _, err := os.Stat(socketPath); err == nil {
			offset++
			continue
		}

		socketPathMap[socketPath] = nil
		i++
	}

	for socketPath := range socketPathMap {
		ctx, cancel := context.WithCancel(context.Background())
		socketPathMap[socketPath] = cancel
		go func(s string) {
			if err := manager.StartUDSListener(ctx, s); err != nil {
				logs.Out.Error("Failed to start listener", zap.Error(err))
				cancel()
				lc.Exit()
			}
		}(socketPath)
	}

	lc.OnExit(func() {
		logs.Out.Info("Shutting down Nautrouds Core...")
		for socketPath, cancel := range socketPathMap {
			cancel()
			logs.Out.Info("Removing socket", zap.String("socketPath", socketPath))
			if _, err := os.Stat(socketPath); err == nil {
				os.Remove(socketPath)
			}
		}
	})

	return nil
}

func cleanLegacySockets(dir string, token string, force bool) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".sock") {
			continue
		}
		filePath := filepath.Join(dir, entry.Name())
		if force {
			err := os.Remove(filePath)
			if err != nil {
				logs.Out.Error("Failed to remove entrypoint", zap.Error(err))
			}
			continue
		}
		if !strings.Contains(entry.Name(), token) {
			continue
		}

		err := os.Remove(filePath)
		if err != nil {
			return err
		}
	}
	return nil
}
