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

type Route struct {
	Path        string            `json:"path" yaml:"path"`
	Target      string            `json:"target" yaml:"target"`
	StripPrefix bool              `json:"strip_prefix" yaml:"strip_prefix"`
	Headers     map[string]string `json:"headers" yaml:"headers"`
	Timeout     int               `json:"timeout" yaml:"timeout"` // seconds, 0 = default 30s
}

type Config struct {
	Port   int     `json:"port" yaml:"port"`
	Routes []Route `json:"routes" yaml:"routes"`
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

func (m *ConfigManager) UpdateRoute(path string, route Route) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for i, r := range m.config.Routes {
		if r.Path == path {
			m.config.Routes[i] = route
			return nil
		}
	}
	return fmt.Errorf("route with path %q not found", path)
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
	data, err := os.ReadFile(m.path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("read config file: %w", err)
	}

	cfg := &Config{}
	if err := yaml.Unmarshal(data, cfg); err != nil {
		if err := json.Unmarshal(data, cfg); err != nil {
			return fmt.Errorf("parse config file: unsupported format")
		}
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	if cfg.Port > 0 {
		m.config.Port = cfg.Port
	}
	if len(cfg.Routes) > 0 {
		m.config.Routes = cfg.Routes
	}
	return nil
}

func (m *ConfigManager) Save() error {
	if m.path == "" {
		return nil
	}
	data, err := yaml.Marshal(m.config)
	if err != nil {
		return fmt.Errorf("marshal config: %w", err)
	}
	return os.WriteFile(m.path, data, 0644)
}

// Watch starts watching the config file for changes and auto-reloads.
// Call Close() to stop the watcher.
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

	// Watch the directory (more reliable than watching the file directly,
	// especially for editors that write temp files then rename)
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
			case event, ok := <-watcher.Events:
				if !ok {
					return
				}
				// Only care about events on our config file
				eventPath, _ := filepath.Abs(event.Name)
				if eventPath != absPath {
					continue
				}
				if event.Has(fsnotify.Write) || event.Has(fsnotify.Create) || event.Has(fsnotify.Rename) {
					// Debounce: wait 200ms for write to complete
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
				}
			case err, ok := <-watcher.Errors:
				if !ok {
					return
				}
				log.Printf("[config] watcher error: %v", err)
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

// loadFromFile reads and fully replaces config from the file on disk.
func (m *ConfigManager) loadFromFile() error {
	if m.path == "" {
		return nil
	}
	data, err := os.ReadFile(m.path)
	if err != nil {
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
