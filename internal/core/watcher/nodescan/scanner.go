package nodescan

import (
	"errors"
	"fmt"
	"maps"
	"nautrouds/internal/core/metrics"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

type Handler interface {
	Extension() string
	ApplyFullScan(baseDir string, byService map[string]map[string]struct{}) error
	ApplyServiceScan(baseDir string, serviceName string, discovered []string) error
}

type Scanner struct {
	baseDir string

	mu       sync.RWMutex
	handlers map[string]Handler // extension -> handler
}

func New(baseDir string) (*Scanner, error) {
	absBase, err := filepath.Abs(baseDir)
	if err != nil {
		return nil, err
	}

	return &Scanner{
		baseDir:  strings.TrimRight(filepath.ToSlash(absBase), "/"),
		handlers: make(map[string]Handler),
	}, nil
}

func (s *Scanner) BaseDir() string {
	return s.baseDir
}

func (s *Scanner) Register(h Handler) error {
	ext := h.Extension()

	s.mu.Lock()
	defer s.mu.Unlock()

	if _, exists := s.handlers[ext]; exists {
		return fmt.Errorf("nodescan: extension %q already registered", ext)
	}
	s.handlers[ext] = h
	return nil
}

func (s *Scanner) Scan(target string) error {
	start := time.Now()
	defer func() {
		metrics.Global.RegistryScanDuration.Observe(time.Since(start).Seconds())
	}()

	// If target is empty or matches baseDir, perform full scan
	if target == "" || target == s.baseDir {
		return s.scanAll()
	}

	return s.scanService(target)
}

func (s *Scanner) scanAll() error {
	handlers := s.snapshotHandlers()

	byExt := make(map[string]map[string]map[string]struct{}, len(handlers))
	for ext := range handlers {
		byExt[ext] = make(map[string]map[string]struct{})
	}

	baseLen := len(s.baseDir) + 1

	walkErr := filepath.WalkDir(s.baseDir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}

		grouped, ok := byExt[filepath.Ext(d.Name())]
		if !ok {
			return nil
		}

		rel := filepath.ToSlash(path[baseLen:])
		if !strings.Contains(rel, "/") {
			return nil
		}

		serviceName := filepath.Dir(rel)
		service, ok := grouped[serviceName]
		if !ok {
			service = make(map[string]struct{})
			grouped[serviceName] = service
		}
		service[path] = struct{}{}
		return nil
	})

	if walkErr != nil && !os.IsNotExist(walkErr) {
		return walkErr
	}

	var errs error
	for ext, handler := range handlers {
		if err := handler.ApplyFullScan(s.baseDir, byExt[ext]); err != nil {
			errs = errors.Join(errs, err)
		}
	}
	return errs
}

func (s *Scanner) scanService(serviceName string) error {
	handlers := s.snapshotHandlers()

	discovered := make(map[string][]string, len(handlers))

	dir := filepath.Join(s.baseDir, serviceName)
	walkErr := filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}

		ext := filepath.Ext(d.Name())
		if _, ok := handlers[ext]; !ok {
			return nil
		}

		discovered[ext] = append(discovered[ext], path)
		return nil
	})

	if walkErr != nil && !os.IsNotExist(walkErr) {
		return walkErr
	}

	var errs error
	for ext, handler := range handlers {
		if err := handler.ApplyServiceScan(s.baseDir, serviceName, discovered[ext]); err != nil {
			errs = errors.Join(errs, err)
		}
	}
	return errs
}

func (s *Scanner) snapshotHandlers() map[string]Handler {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return maps.Clone(s.handlers)
}
