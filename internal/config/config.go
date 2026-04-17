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
	Target  string `json:"target" yaml:"target"`
	Weight  int    `json:"weight" yaml:"weight"`   // 0 or 1 = equal, >1 = higher chance
	Backup  bool   `json:"backup" yaml:"backup"`   // only used when all non-backup are down
	Enabled *bool  `json:"enabled,omitempty" yaml:"enabled,omitempty"` // nil means enabled by default
}

const (
	RouteTypeProxy  = "proxy"
	RouteTypeMock   = "mock"
	SSHAuthPassword = "password"
	SSHAuthKey      = "key"
	SSHModeLocal    = "local"
	SSHModeRemote   = "remote"
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
	Enabled     *bool             `json:"enabled,omitempty" yaml:"enabled,omitempty"`
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
		return []Upstream{{Target: r.Target, Weight: 1, Enabled: boolPtr(true)}}
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

func (r Route) IsEnabled() bool {
	return boolValue(r.Enabled, true)
}

type TcpRoute struct {
	Listen    string     `json:"listen" yaml:"listen"`
	Enabled   *bool      `json:"enabled,omitempty" yaml:"enabled,omitempty"`
	Upstreams []Upstream `json:"upstreams" yaml:"upstreams"`
}

func (r TcpRoute) IsEnabled() bool {
	return boolValue(r.Enabled, true)
}

type SSHTunnel struct {
	Name           string `json:"name" yaml:"name"`
	Enabled        *bool  `json:"enabled,omitempty" yaml:"enabled,omitempty"`
	Host           string `json:"host" yaml:"host"`
	Port           int    `json:"port" yaml:"port"`
	Username       string `json:"username" yaml:"username"`
	AuthType       string `json:"auth_type" yaml:"auth_type"`
	Password       string `json:"password,omitempty" yaml:"password,omitempty"`
	PrivateKey     string `json:"private_key,omitempty" yaml:"private_key,omitempty"`
	PrivateKeyPath string `json:"private_key_path,omitempty" yaml:"private_key_path,omitempty"`
	Direction      string `json:"direction" yaml:"direction"`
	LocalAddress   string `json:"local_address" yaml:"local_address"`
	RemoteAddress  string `json:"remote_address" yaml:"remote_address"`
}

func (t SSHTunnel) IsEnabled() bool {
	return boolValue(t.Enabled, true)
}

func (u Upstream) IsEnabled() bool {
	return boolValue(u.Enabled, true)
}

type Config struct {
	Port             int         `json:"port" yaml:"port"`
	LogRequestParams bool        `json:"log_request_params" yaml:"log_request_params"`
	Routes           []Route     `json:"routes" yaml:"routes"`
	TcpRoutes        []TcpRoute  `json:"tcp_routes" yaml:"tcp_routes"`
	SSHTunnels       []SSHTunnel `json:"ssh_tunnels" yaml:"ssh_tunnels"`
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
			Port:       8080,
			Routes:     []Route{},
			TcpRoutes:  []TcpRoute{},
			SSHTunnels: []SSHTunnel{},
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

func (m *ConfigManager) SetTcpRoutes(routes []TcpRoute) {
	m.mu.Lock()
	defer m.mu.Unlock()
	normalizeTcpRoutes(routes)
	m.config.TcpRoutes = routes
}

func (m *ConfigManager) SetSSHTunnels(tunnels []SSHTunnel) {
	m.mu.Lock()
	defer m.mu.Unlock()
	normalizeSSHTunnels(tunnels)
	m.config.SSHTunnels = tunnels
}

func (m *ConfigManager) AddTcpRoute(route TcpRoute) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	normalizeTcpRoute(&route)
	for _, r := range m.config.TcpRoutes {
		if r.Listen == route.Listen {
			return fmt.Errorf("TCP route with listen %q already exists", route.Listen)
		}
	}
	m.config.TcpRoutes = append(m.config.TcpRoutes, route)
	return nil
}

func (m *ConfigManager) UpdateTcpRoute(originalListen string, route TcpRoute) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	normalizeTcpRoute(&route)
	for i, r := range m.config.TcpRoutes {
		if r.Listen == originalListen {
			m.config.TcpRoutes[i] = route
			return nil
		}
	}
	return fmt.Errorf("TCP route with listen %q not found", originalListen)
}

func (m *ConfigManager) DeleteTcpRoute(listen string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for i, r := range m.config.TcpRoutes {
		if r.Listen == listen {
			m.config.TcpRoutes = append(m.config.TcpRoutes[:i], m.config.TcpRoutes[i+1:]...)
			return nil
		}
	}
	return fmt.Errorf("TCP route with listen %q not found", listen)
}

func (m *ConfigManager) AddSSHTunnel(tunnel SSHTunnel) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	normalizeSSHTunnel(&tunnel)
	for _, item := range m.config.SSHTunnels {
		if item.Name == tunnel.Name {
			return fmt.Errorf("SSH tunnel with name %q already exists", tunnel.Name)
		}
	}
	m.config.SSHTunnels = append(m.config.SSHTunnels, tunnel)
	return nil
}

func (m *ConfigManager) UpdateSSHTunnel(originalName string, tunnel SSHTunnel) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	normalizeSSHTunnel(&tunnel)
	for i, item := range m.config.SSHTunnels {
		if item.Name == originalName {
			m.config.SSHTunnels[i] = tunnel
			return nil
		}
	}
	return fmt.Errorf("SSH tunnel with name %q not found", originalName)
}

func (m *ConfigManager) DeleteSSHTunnel(name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for i, item := range m.config.SSHTunnels {
		if item.Name == name {
			m.config.SSHTunnels = append(m.config.SSHTunnels[:i], m.config.SSHTunnels[i+1:]...)
			return nil
		}
	}
	return fmt.Errorf("SSH tunnel with name %q not found", name)
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
						log.Printf("[config] reloaded from file (port=%d, routes=%d, tcp=%d)", cfg.Port, len(cfg.Routes), len(cfg.TcpRoutes))
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
	if cfg.TcpRoutes == nil {
		cfg.TcpRoutes = []TcpRoute{}
	}
	if cfg.SSHTunnels == nil {
		cfg.SSHTunnels = []SSHTunnel{}
	}
	normalizeRoutes(cfg.Routes)
	normalizeTcpRoutes(cfg.TcpRoutes)
	normalizeSSHTunnels(cfg.SSHTunnels)

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
	if route.Enabled == nil {
		route.Enabled = boolPtr(true)
	}
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
	for i := range route.Upstreams {
		normalizeUpstream(&route.Upstreams[i])
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

func normalizeTcpRoutes(routes []TcpRoute) {
	for i := range routes {
		normalizeTcpRoute(&routes[i])
	}
}

func normalizeTcpRoute(route *TcpRoute) {
	if route.Enabled == nil {
		route.Enabled = boolPtr(true)
	}
	if route.Upstreams == nil {
		route.Upstreams = []Upstream{}
	}
	for i := range route.Upstreams {
		normalizeUpstream(&route.Upstreams[i])
	}
}

func normalizeSSHTunnels(tunnels []SSHTunnel) {
	for i := range tunnels {
		normalizeSSHTunnel(&tunnels[i])
	}
}

func normalizeSSHTunnel(tunnel *SSHTunnel) {
	if tunnel.Enabled == nil {
		tunnel.Enabled = boolPtr(true)
	}
	tunnel.Name = strings.TrimSpace(tunnel.Name)
	tunnel.Host = strings.TrimSpace(tunnel.Host)
	tunnel.Username = strings.TrimSpace(tunnel.Username)
	tunnel.AuthType = strings.ToLower(strings.TrimSpace(tunnel.AuthType))
	if tunnel.AuthType != SSHAuthKey {
		tunnel.AuthType = SSHAuthPassword
	}
	tunnel.Direction = strings.ToLower(strings.TrimSpace(tunnel.Direction))
	if tunnel.Direction != SSHModeRemote {
		tunnel.Direction = SSHModeLocal
	}
	tunnel.LocalAddress = strings.TrimSpace(tunnel.LocalAddress)
	tunnel.RemoteAddress = strings.TrimSpace(tunnel.RemoteAddress)
	tunnel.PrivateKeyPath = strings.TrimSpace(tunnel.PrivateKeyPath)
	if tunnel.Port == 0 {
		tunnel.Port = 22
	}
	if tunnel.Name == "" {
		tunnel.Name = fmt.Sprintf("%s-%s-%s", tunnel.Direction, tunnel.LocalAddress, tunnel.RemoteAddress)
	}
}

func normalizeUpstream(upstream *Upstream) {
	if upstream.Enabled == nil {
		upstream.Enabled = boolPtr(true)
	}
}

func boolPtr(value bool) *bool {
	v := value
	return &v
}

func boolValue(value *bool, defaultValue bool) bool {
	if value == nil {
		return defaultValue
	}
	return *value
}
