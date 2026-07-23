package main

import (
	"context"
	"fmt"
	"nautrouds/internal/core/configwatcher"
	"nautrouds/internal/core/logs"
	"nautrouds/internal/core/metrics"
	"nautrouds/internal/core/mmfg"
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
	"github.com/lucap9056/go-lifecycle/lifemanaged"
	"go.uber.org/zap"
)

func main() {
	// Define and parse flags
	opts := options.Load()

	logs.InitLogger(opts.LogLevel)
	defer logs.Sync()

	// Create entrypoint directory if it doesn't exist
	if err := os.MkdirAll(opts.EntrypointDir, opts.EntrypointDirMode); err != nil {
		logs.Out.Error("Failed to create entrypoint directory", zap.Error(err))
		return
	}
	// Ensure permissions if directory already existed
	os.Chmod(opts.EntrypointDir, opts.EntrypointDirMode)

	// Create services directory if it doesn't exist
	// Default (01777): sticky bit ensures only the owner can delete their own socket
	if err := os.MkdirAll(opts.ServicesDir, opts.ServicesDirMode); err != nil {
		logs.Out.Error("Failed to create services directory", zap.Error(err))
		return
	}
	os.Chmod(opts.ServicesDir, opts.ServicesDirMode)

	err := lifemanaged.Run(func(lc *lifecycle.LifecycleManager) error {
		return run(lc, opts)
	})
	if err != nil {
		logs.Out.Error("Error occurred during startup", zap.Error(err))
	}
}

func run(lc *lifecycle.LifecycleManager, opts *options.Options) error {
	fileLockCtx, fileLockCancel := context.WithTimeout(context.Background(), time.Second*5)
	defer fileLockCancel()
	fileLock := flock.New(filepath.Join(opts.EntrypointDir, "nautrouds.lock"))
	locked, err := fileLock.TryLockContext(fileLockCtx, 200*time.Millisecond)
	if err != nil || !locked {
		return fmt.Errorf("failed to acquire lock: %w", err)
	}
	defer fileLock.Unlock()

	// Initialize Registry and Watcher for dynamic node discovery
	reg, err := registry.NewRegistry()
	if err != nil {
		return fmt.Errorf("registry initialization failed: %w", err)
	}

	var mmfgHub mmfg.Hub
	if mmfg.IsAvailable {
		hub, err := mmfg.NewHub()
		if err != nil {
			return fmt.Errorf("mmfg hub initialization failed: %w", err)
		}
		mmfgHub = hub
	}

	manager := proxy.NewManager(reg, mmfgHub)

	// Initialize Config Watcher (Handles load & hot-reload)
	cw, err := configwatcher.NewConfigWatcher(opts.ConfigPath, opts.NtucPath, manager)
	if err != nil {
		return fmt.Errorf("config watcher initialization failed: %w", err)
	}
	lc.OnExit(func() {
		cw.Close()
	})

	if err := cw.LoadInitial(); err != nil {
		return fmt.Errorf("failed to perform initial route config load: %w", err)
	}

	w, err := watcher.NewWatcher(opts.ServicesDir, reg, mmfgHub)
	if err != nil {
		return fmt.Errorf("node watcher initialization failed: %w", err)
	}
	lc.OnExit(func() {
		w.Close()
	})

	if opts.MetricsPath != "-" {
		collectorPath := filepath.Join(opts.ServicesDir, "metrics.sock")
		if opts.MetricsPath != "" {
			collectorPath = filepath.Join(opts.ServicesDir, opts.MetricsPath)
		}
		collector := metrics.NewCollector(collectorPath, opts.MetricsSockMode, metrics.Global)
		if err := collector.Start(); err != nil {
			return fmt.Errorf("metrics collector startup failed: %w", err)
		}
		lc.OnExit(func() {
			collector.Stop()
		})
	}

	// Start All Watchers and Listener
	if err := cw.Start(); err != nil {
		return fmt.Errorf("failed to start config watcher: %w", err)
	}

	if err := w.Start(); err != nil {
		return fmt.Errorf("failed to start node watcher: %w", err)
	}

	if err := createEntrypoints(lc, manager, opts); err != nil {
		return fmt.Errorf("failed to initialize entrypoints: %w", err)
	}

	return nil
}

func createEntrypoints(lc *lifecycle.LifecycleManager, manager *proxy.Manager, opts *options.Options) error {
	hasToken := len(opts.Token) > 0
	token := "-"
	if hasToken {
		token = fmt.Sprintf("-%s-", opts.Token)
	}

	err := cleanLegacySockets(opts.EntrypointDir, token)
	if err != nil {
		return fmt.Errorf("failed to clean legacy sockets: %w", err)
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
			if cancel != nil {
				cancel()
			}
			logs.Out.Info("Removing socket", zap.String("socketPath", socketPath))
			if _, err := os.Stat(socketPath); err == nil {
				os.Remove(socketPath)
			}
		}
	})

	return nil
}

func cleanLegacySockets(dir string, token string) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".sock") || !strings.Contains(name, token) {
			continue
		}
		filePath := filepath.Join(dir, name)
		err := os.Remove(filePath)
		if err != nil {
			return err
		}
	}
	return nil
}
