//go:build unix

package mmfg

import (
	"context"
	"errors"
	"net/http"
	"path/filepath"
	"strings"
	"sync"

	"github.com/nautrouds/mmfg-http/go/mmfghttp"
)

const IsAvailable = true

const (
	nodeSuffix    = ".mmfg"
	controlSuffix = ".ctl.mmfg"
)

type UnixHub struct {
	hub  *mmfghttp.Hub
	dial func(nodeName, socketPath, controlSocketPath string) error

	mu     sync.Mutex
	dialed map[string]string
}

func NewHub() (Hub, error) {
	hub, err := mmfghttp.NewHub()
	if err != nil {
		return nil, err
	}

	h := &UnixHub{
		hub:    hub,
		dialed: make(map[string]string),
	}
	h.dial = h.hub.Dial

	return h, nil
}

func (h *UnixHub) Extension() string {
	return nodeSuffix
}

func isControlSocket(path string) bool {
	return strings.HasSuffix(path, controlSuffix)
}

func nodeKey(baseDir, path string) (string, error) {
	rel, err := filepath.Rel(baseDir, path)
	if err != nil {
		return "", err
	}
	rel = filepath.ToSlash(rel)

	if strings.HasSuffix(rel, controlSuffix) {
		return strings.TrimSuffix(rel, controlSuffix), nil
	}
	return strings.TrimSuffix(rel, nodeSuffix), nil
}

func splitNodesAndControls(baseDir string, paths []string) (mains []string, controlByKey map[string]string, err error) {
	controlByKey = make(map[string]string)

	for _, p := range paths {
		if !isControlSocket(p) {
			continue
		}
		key, err := nodeKey(baseDir, p)
		if err != nil {
			return nil, nil, err
		}
		controlByKey[key] = p
	}

	for _, p := range paths {
		if !isControlSocket(p) {
			mains = append(mains, p)
		}
	}

	return mains, controlByKey, nil
}

func (h *UnixHub) dialNode(baseDir, serviceName, mainPath, controlPath string) error {
	h.mu.Lock()
	_, already := h.dialed[mainPath]
	h.mu.Unlock()
	if already {
		return nil
	}

	nodeName, err := nodeKey(baseDir, mainPath)
	if err != nil {
		return err
	}

	if err := h.dial(nodeName, mainPath, controlPath); err != nil {
		return err
	}

	h.mu.Lock()
	h.dialed[mainPath] = serviceName
	h.mu.Unlock()
	return nil
}

func (h *UnixHub) dialDiscovered(baseDir, serviceName string, paths []string) error {
	mains, controlByKey, err := splitNodesAndControls(baseDir, paths)
	if err != nil {
		return err
	}

	var errs error
	for _, main := range mains {
		key, err := nodeKey(baseDir, main)
		if err != nil {
			errs = errors.Join(errs, err)
			continue
		}
		if err := h.dialNode(baseDir, serviceName, main, controlByKey[key]); err != nil {
			errs = errors.Join(errs, err)
		}
	}
	return errs
}

func (h *UnixHub) ApplyFullScan(baseDir string, byService map[string]map[string]struct{}) error {
	current := make(map[string]struct{})
	for _, nodes := range byService {
		for p := range nodes {
			if !isControlSocket(p) {
				current[p] = struct{}{}
			}
		}
	}

	h.mu.Lock()
	for p := range h.dialed {
		if _, ok := current[p]; !ok {
			delete(h.dialed, p)
		}
	}
	h.mu.Unlock()

	var errs error
	for svcName, nodes := range byService {
		paths := make([]string, 0, len(nodes))
		for p := range nodes {
			paths = append(paths, p)
		}
		if err := h.dialDiscovered(baseDir, svcName, paths); err != nil {
			errs = errors.Join(errs, err)
		}
	}
	return errs
}

func (h *UnixHub) ApplyServiceScan(baseDir string, serviceName string, discovered []string) error {
	current := make(map[string]struct{}, len(discovered))
	for _, p := range discovered {
		if !isControlSocket(p) {
			current[p] = struct{}{}
		}
	}

	h.mu.Lock()
	for p, svc := range h.dialed {
		if svc != serviceName {
			continue
		}
		if _, ok := current[p]; !ok {
			delete(h.dialed, p)
		}
	}
	h.mu.Unlock()

	return h.dialDiscovered(baseDir, serviceName, discovered)
}

func (h *UnixHub) Request(ctx context.Context, r *http.Request) (Request, error) {
	return h.hub.Request(ctx, r)
}
