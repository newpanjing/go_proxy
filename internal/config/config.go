package config

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
	"gopkg.in/yaml.v3"
)

type Upstream struct {
	Target string `json:"target" yaml:"target"`
	Weight int    `json:"weight" yaml:"weight"` // 0 or 1 = equal, >1 = higher chance
	Backup bool   `json:"backup" yaml:"backup"` // only used when all non-backup are down
}

const (
	RouteTypeProxy = "proxy"
	RouteTypeMock  = "mock"
)

type MockConfig struct {
	Method       string            `json:"method,omitempty" yaml:"method,omitempty"`
	Params       map[string]string `json:"params,omitempty" yaml:"params,omitempty"`
	StatusCode   int               `json:"status_code,omitempty" yaml:"status_code,omitempty"`
	ResponseType string            `json:"response_type,omitempty" yaml:"response_type,omitempty"`
	Headers      map[string]string `json:"headers,omitempty" yaml:"headers,omitempty"`
	Data         interface{}       `json:"data,omitempty" yaml:"data,omitempty"`
}

type Route struct {
	Path        string            `json:"path" yaml:"path"`
	Priority    int               `json:"priority" yaml:"priority"`
	Type        string            `json:"type,omitempty" yaml:"type,omitempty"`
	Target      string            `json:"target,omitempty" yaml:"target,omitempty"` // legacy single target
	Upstreams   []Upstream        `json:"upstreams" yaml:"upstreams"`
	StripPrefix bool              `json:"strip_prefix" yaml:"strip_prefix"`
	Headers     map[string]string `json:"headers" yaml:"headers"`
	Timeout     int               `json:"timeout" yaml:"timeout"`
	Mock        *MockConfig       `json:"mock,omitempty" yaml:"mock,omitempty"`
}

// ResolveUpstreams returns the effective upstream list.
func (r *Route) ResolveUpstreams() []Upstream {
	if r.IsMock() {
		return nil
	}
	if len(r.Upstreams) > 0 {
		return r.Upstreams
	}
	if r.Target != "" {
		return []Upstream{{Target: r.Target, Weight: 1}}
	}
	return nil
}

func (r Route) EffectiveType() string {
	if strings.TrimSpace(r.Type) == "" {
		return RouteTypeProxy
	}
	return strings.ToLower(strings.TrimSpace(r.Type))
}

func (r Route) IsMock() bool {
	return r.EffectiveType() == RouteTypeMock
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
	normalizeRoutes(routes)
	m.config.Routes = routes
}

func (m *ConfigManager) AddRoute(route Route) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	normalizeRoute(&route)
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
	normalizeRoute(&route)
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
	normalizeRoutes(cfg.Routes)

	m.mu.Lock()
	m.config = cfg
	m.mu.Unlock()
	return nil
}

func normalizeRoutes(routes []Route) {
	for i := range routes {
		normalizeRoute(&routes[i])
	}
}

func normalizeRoute(route *Route) {
	route.Type = route.EffectiveType()
	if route.Headers == nil {
		route.Headers = map[string]string{}
	}

	if route.IsMock() {
		if route.Mock == nil {
			route.Mock = &MockConfig{}
		}
		normalizeMock(route.Mock)
		route.Target = ""
		route.Upstreams = nil
		route.Headers = nil
		route.Timeout = 0
		route.StripPrefix = false
		return
	}

	route.Type = RouteTypeProxy
	route.Mock = nil
	if route.Upstreams == nil {
		route.Upstreams = []Upstream{}
	}
}

func normalizeMock(mock *MockConfig) {
	mock.Method = strings.ToUpper(strings.TrimSpace(mock.Method))
	if mock.Method == "" {
		mock.Method = "ANY"
	}
	if mock.Params == nil {
		mock.Params = map[string]string{}
	}
	if mock.StatusCode == 0 {
		mock.StatusCode = 200
	}
	if strings.TrimSpace(mock.ResponseType) == "" {
		mock.ResponseType = "application/json"
	}
	if mock.Headers == nil {
		mock.Headers = map[string]string{}
	}
	mock.Data = normalizeGenericValue(mock.Data)
}

func normalizeGenericValue(v interface{}) interface{} {
	switch val := v.(type) {
	case nil:
		return map[string]interface{}{}
	case map[string]interface{}:
		out := make(map[string]interface{}, len(val))
		for k, item := range val {
			out[k] = normalizeGenericValue(item)
		}
		return out
	case map[interface{}]interface{}:
		out := make(map[string]interface{}, len(val))
		for k, item := range val {
			out[fmt.Sprint(k)] = normalizeGenericValue(item)
		}
		return out
	case []interface{}:
		out := make([]interface{}, len(val))
		for i, item := range val {
			out[i] = normalizeGenericValue(item)
		}
		return out
	default:
		return val
	}
}
