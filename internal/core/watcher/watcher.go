package watcher

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"time"

	"nautrouds/internal/core/logs"
	"nautrouds/internal/core/registry"
	"nautrouds/internal/core/watcher/nodescan"

	"github.com/fsnotify/fsnotify"
	"go.uber.org/zap"
)

type Watcher struct {
	registry *registry.Registry
	scanner  *nodescan.Scanner

	dirtyServices map[string]struct{}
	dirtyMu       sync.Mutex

	eventSignal chan struct{}
	cancel      context.CancelFunc
	fsWatcher   *fsnotify.Watcher
}

func NewWatcher(baseDir string, r *registry.Registry, scanHandlers ...nodescan.Handler) (*Watcher, error) {
	fw, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, err
	}

	s, err := nodescan.New(baseDir)
	if err != nil {
		return nil, err
	}

	if err := s.Register(r); err != nil {
		return nil, err
	}

	for _, h := range scanHandlers {
		if err := s.Register(h); err != nil {
			return nil, err
		}
	}

	watcher := &Watcher{
		registry:      r,
		scanner:       s,
		dirtyServices: make(map[string]struct{}),
		eventSignal:   make(chan struct{}, 1),
		fsWatcher:     fw,
	}

	return watcher, nil
}

func (w *Watcher) addRecursive(root string) error {
	return filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			logs.Out.Info("Watching directory", zap.String("path", path))
			return w.fsWatcher.Add(path)
		}
		return nil
	})
}

func (w *Watcher) Start() error {
	// Initial Scan
	if err := w.scanner.Scan(""); err != nil {
		return err
	}

	ctx, cancel := context.WithCancel(context.Background())
	w.cancel = cancel

	go w.listenEvents(ctx)
	go w.runWorkerLoop(ctx)

	root := w.scanner.BaseDir()
	if err := w.addRecursive(root); err != nil {
		return err
	}

	return nil
}

func (w *Watcher) listenEvents(ctx context.Context) {
	baseDir := w.scanner.BaseDir()
	for {
		select {
		case <-ctx.Done():
			return
		case event, ok := <-w.fsWatcher.Events:
			if !ok {
				return
			}

			if event.Has(fsnotify.Create) {
				info, err := os.Stat(event.Name)
				if err == nil && info.IsDir() {
					w.addRecursive(event.Name)
				}
			}
			switch event.Op {
			case fsnotify.Create, fsnotify.Write, fsnotify.Remove, fsnotify.Rename:
				relPath, err := filepath.Rel(baseDir, event.Name)
				if err != nil {
					logs.Out.Error("failed to get relative path", zap.Error(err))
					continue
				}

				serviceName := filepath.Dir(relPath)
				if serviceName != "" && serviceName != "." {
					w.dirtyMu.Lock()
					w.dirtyServices[serviceName] = struct{}{}
					w.dirtyMu.Unlock()

					select {
					case w.eventSignal <- struct{}{}:
					default:
					}
				}
			}

		case err, ok := <-w.fsWatcher.Errors:
			if !ok {
				return
			}
			logs.Out.Error("fsnotify error", zap.Error(err))
		}
	}
}

func (w *Watcher) runWorkerLoop(ctx context.Context) {
	const retryInterval = 5 * time.Second
	const ticksPerFullScan = 6 // 5s * 6 = 30s

	ticker := time.NewTicker(retryInterval)
	defer ticker.Stop()

	tickCount := 0

	for {
		select {
		case <-ctx.Done():
			return

		case <-w.eventSignal:
			w.dirtyMu.Lock()
			toScan := make([]string, 0, len(w.dirtyServices))
			for svc := range w.dirtyServices {
				toScan = append(toScan, svc)
			}
			w.dirtyServices = make(map[string]struct{})
			w.dirtyMu.Unlock()

			for _, svcName := range toScan {
				w.scanner.Scan(svcName)
				logs.Out.Debug("Targeted scan completed", zap.String("service", svcName))
			}

		case <-ticker.C:
			tickCount++
			if tickCount >= ticksPerFullScan {
				if err := w.scanner.Scan(""); err != nil {
					logs.Out.Error("Full registry scan failed", zap.Error(err))
				} else {
					logs.Out.Debug("Scheduled full scan completed")
				}
				tickCount = 0
			} else {
				w.registry.RetryUnhealthy()
			}
		}
	}
}

func (w *Watcher) Close() error {
	if w.cancel != nil {
		w.cancel()
	}
	if w.fsWatcher != nil {
		w.fsWatcher.Close()
	}
	return nil
}
