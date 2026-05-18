package configwatcher

import (
	"encoding/gob"
	"fmt"
	"io"
	"nautrouds/internal/core/logs"
	"nautrouds/internal/core/metrics"
	"nautrouds/internal/core/proxy"
	"nautrouds/internal/rtree"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/fsnotify/fsnotify"
	"go.uber.org/zap"
)

type ConfigWatcher struct {
	configDirectory string
	configFileName  string
	fullConfigPath  string
	ntucPath        string
	manager         *proxy.Manager
	isSource        bool
	fw              *fsnotify.Watcher
}

func NewConfigWatcher(configPath, ntucPath string, manager *proxy.Manager) (*ConfigWatcher, error) {
	fw, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, err
	}

	absPath, err := filepath.Abs(configPath)
	if err != nil {
		absPath = configPath
	}

	return &ConfigWatcher{
		configDirectory: filepath.Dir(configPath),
		configFileName:  filepath.Base(configPath),
		fullConfigPath:  absPath,
		ntucPath:        ntucPath,
		manager:         manager,
		isSource:        !strings.HasSuffix(configPath, ".ntu"),
		fw:              fw,
	}, nil
}

func (cw *ConfigWatcher) LoadInitial() error {
	var tree *rtree.RouteTree
	var err error

	if cw.isSource {
		tree, err = cw.compileAndLoad()
	} else {
		tree, err = cw.loadStatic()
	}

	if err != nil {
		return err
	}

	cw.manager.UpdateTree(tree)
	return nil
}

func (cw *ConfigWatcher) Start() error {
	if err := cw.fw.Add(cw.configDirectory); err != nil {
		return fmt.Errorf("failed to watch config file: %v", err)
	}

	go cw.listen()
	logs.Out.Info("Config watcher started", zap.String("path", cw.fullConfigPath))
	return nil
}

func (cw *ConfigWatcher) listen() {
	var timer *time.Timer

	for {
		select {
		case event, ok := <-cw.fw.Events:
			if !ok {
				return
			}

			if filepath.Base(event.Name) != cw.configFileName {
				continue
			}

			if event.Has(fsnotify.Write) || event.Has(fsnotify.Create) || event.Has(fsnotify.Rename) {
				logs.Out.Info("Config file change detected", zap.String("path", event.Name))

				if timer != nil {
					timer.Stop()
				}
				timer = time.AfterFunc(100*time.Millisecond, func() {
					cw.reload()
				})
			}
		case err, ok := <-cw.fw.Errors:
			if !ok {
				return
			}
			logs.Out.Error("Config watcher error", zap.Error(err))
		}
	}
}

func (cw *ConfigWatcher) reload() {
	start := time.Now()
	var newTree *rtree.RouteTree
	var err error

	if cw.isSource {
		newTree, err = cw.compileAndLoad()
	} else {
		newTree, err = cw.loadStatic()
	}

	if err != nil {
		logs.Out.Error("Error reloading route table", zap.Error(err))
		metrics.Global.ConfigErrorsTotal.WithLabelValues("reload").Inc()
		return
	}

	cw.manager.UpdateTree(newTree)
	metrics.Global.ConfigReloadDuration.Observe(time.Since(start).Seconds())
	logs.Out.Info("Route table reloaded and updated")
}

func (cw *ConfigWatcher) loadStatic() (*rtree.RouteTree, error) {
	file, err := os.Open(cw.fullConfigPath)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	var tree rtree.RouteTree
	dec := gob.NewDecoder(file)
	if err := dec.Decode(&tree); err != nil {
		metrics.Global.ConfigErrorsTotal.WithLabelValues("decode").Inc()
		return nil, err
	}
	return &tree, nil
}

func (cw *ConfigWatcher) compileAndLoad() (*rtree.RouteTree, error) {
	cmd := exec.Command(cw.ntucPath, "-i", cw.fullConfigPath, "-o", "-")

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}

	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, err
	}

	if err := cmd.Start(); err != nil {
		metrics.Global.ConfigErrorsTotal.WithLabelValues("compile_start").Inc()
		return nil, err
	}

	var tree rtree.RouteTree
	dec := gob.NewDecoder(stdout)
	decodeErr := dec.Decode(&tree)

	slurp, _ := io.ReadAll(stderr)
	if err := cmd.Wait(); err != nil {
		metrics.Global.ConfigErrorsTotal.WithLabelValues("compile_fail").Inc()
		return nil, fmt.Errorf("ntuc failed: %v, stderr: %s", err, string(slurp))
	}

	if decodeErr != nil {
		metrics.Global.ConfigErrorsTotal.WithLabelValues("decode").Inc()
		return nil, fmt.Errorf("decode compiled data failed: %v", decodeErr)
	}

	return &tree, nil
}

func (cw *ConfigWatcher) Close() error {
	return cw.fw.Close()
}
