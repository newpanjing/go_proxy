package config

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
	"gopkg.in/yaml.v3"
)

type Upstream struct {
	Target string `json:"target" yaml:"target"`
	Weight int    `json:"weight" yaml:"weight"`  // 0 or 1 = equal, >1 = higher chance
	Backup bool   `json:"backup" yaml:"backup"`  // only used when all non-backup are down
}

type Route struct {
	Path        string            `json:"path" yaml:"path"`
	Target      string            `json:"target,omitempty" yaml:"target,omitempty"` // legacy single target
	Upstreams   []Upstream        `json:"upstreams" yaml:"upstreams"`
	StripPrefix bool              `json:"strip_prefix" yaml:"strip_prefix"`
	Headers     map[string]string `json:"headers" yaml:"headers"`
	Timeout     int               `json:"timeout" yaml:"timeout"`
}

// ResolveUpstreams returns the effective upstream list.
func (r *Route) ResolveUpstreams() []Upstream {
	if len(r.Upstreams) > 0 {
		return r.Upstreams
	}
	if r.Target != "" {
		return []Upstream{{Target: r.Target, Weight: 1}}
	}
	return nil
}

type Config struct {
	Port             int     `json:"port" yaml:"port"`
	LogRequestParams bool    `json:"log_request_params" yaml:"log_request_params"`
	Routes           []Route `json:"routes" yaml:"routes"`
}

type ConfigManager struct {
	mu      sync.RWMutex
	config  *Config
	path    string
	watcher *fsnotify.Watcher
	done    chan struct{}
}

func NewManager(path string) *ConfigManager {
	return &ConfigManager{
		config: &Config{
			Port:   8080,
			Routes: []Route{},
		},
		path: path,
		done: make(chan struct{}),
	}
}

func (m *ConfigManager) Get() Config {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return *m.config
}

func (m *ConfigManager) SetPort(port int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.config.Port = port
}

func (m *ConfigManager) SetLogRequestParams(enabled bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.config.LogRequestParams = enabled
}

func (m *ConfigManager) SetRoutes(routes []Route) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.config.Routes = routes
}

func (m *ConfigManager) AddRoute(route Route) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, r := range m.config.Routes {
		if r.Path == route.Path {
			return fmt.Errorf("route with path %q already exists", route.Path)
		}
	}
	m.config.Routes = append(m.config.Routes, route)
	return nil
}

func (m *ConfigManager) UpdateRoute(originalPath string, route Route) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for i, r := range m.config.Routes {
		if r.Path == originalPath {
			m.config.Routes[i] = route
			return nil
		}
	}
	return fmt.Errorf("route with path %q not found", originalPath)
}

func (m *ConfigManager) DeleteRoute(path string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for i, r := range m.config.Routes {
		if r.Path == path {
			m.config.Routes = append(m.config.Routes[:i], m.config.Routes[i+1:]...)
			return nil
		}
	}
	return fmt.Errorf("route with path %q not found", path)
}

func (m *ConfigManager) Load() error {
	if m.path == "" {
		return nil
	}
	return m.loadFromFile()
}

func (m *ConfigManager) Save() error {
	if m.path == "" {
		return nil
	}
	m.mu.RLock()
	data, err := yaml.Marshal(m.config)
	m.mu.RUnlock()
	if err != nil {
		return fmt.Errorf("marshal config: %w", err)
	}
	return os.WriteFile(m.path, data, 0644)
}

func (m *ConfigManager) Watch() error {
	if m.path == "" {
		return nil
	}

	absPath, err := filepath.Abs(m.path)
	if err != nil {
		return fmt.Errorf("resolve config path: %w", err)
	}

	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return fmt.Errorf("create watcher: %w", err)
	}
	m.watcher = watcher

	dir := filepath.Dir(absPath)
	if err := watcher.Add(dir); err != nil {
		watcher.Close()
		return fmt.Errorf("watch directory %s: %w", dir, err)
	}

	go func() {
		var debounceTimer *time.Timer
		for {
			select {
			case <-m.done:
				return
			case _, ok := <-watcher.Events:
				if !ok {
					return
				}
				// Debounce all events
				if debounceTimer != nil {
					debounceTimer.Stop()
				}
				debounceTimer = time.AfterFunc(200*time.Millisecond, func() {
					if err := m.loadFromFile(); err != nil {
						log.Printf("[config] reload error: %v", err)
					} else {
						cfg := m.Get()
						log.Printf("[config] reloaded from file (port=%d, routes=%d)", cfg.Port, len(cfg.Routes))
					}
				})
			case _, ok := <-watcher.Errors:
				if !ok {
					return
				}
			}
		}
	}()

	log.Printf("[config] watching %s for changes", absPath)
	return nil
}

func (m *ConfigManager) Close() {
	close(m.done)
	if m.watcher != nil {
		m.watcher.Close()
	}
}

func (m *ConfigManager) loadFromFile() error {
	if m.path == "" {
		return nil
	}
	data, err := os.ReadFile(m.path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("read: %w", err)
	}

	cfg := &Config{}
	if err := yaml.Unmarshal(data, cfg); err != nil {
		if err := json.Unmarshal(data, cfg); err != nil {
			return fmt.Errorf("parse: unsupported format")
		}
	}
	if cfg.Port == 0 {
		cfg.Port = 8080
	}
	if cfg.Routes == nil {
		cfg.Routes = []Route{}
	}

	m.mu.Lock()
	m.config = cfg
	m.mu.Unlock()
	return nil
}
