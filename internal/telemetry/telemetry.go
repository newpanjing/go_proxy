package telemetry

import (
	"context"
	"encoding/json"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"github.com/shirou/gopsutil/v4/cpu"
	"github.com/shirou/gopsutil/v4/disk"
	"github.com/shirou/gopsutil/v4/mem"
	"go-proxy/internal/config"
)

type Snapshot struct {
	UpdatedAt         time.Time `json:"updated_at"`
	HTTPRequests      uint64    `json:"http_requests"`
	HTTPActive        int64     `json:"http_active"`
	HTTPInboundBytes  uint64    `json:"http_inbound_bytes"`
	HTTPOutboundBytes uint64    `json:"http_outbound_bytes"`
	TCPConnections    uint64    `json:"tcp_connections"`
	TCPActive         int64     `json:"tcp_active"`
	TCPInboundBytes   uint64    `json:"tcp_inbound_bytes"`
	TCPOutboundBytes  uint64    `json:"tcp_outbound_bytes"`
	HTTPRoutes        int       `json:"http_routes"`
	HTTPEnabledRoutes int       `json:"http_enabled_routes"`
	TCPRoutes         int       `json:"tcp_routes"`
	TCPEnabledRoutes  int       `json:"tcp_enabled_routes"`
	SSHTunnels        int       `json:"ssh_tunnels"`
	SystemCPUPercent  float64   `json:"system_cpu_percent"`
	SystemMemoryUsed  uint64    `json:"system_memory_used"`
	SystemMemoryTotal uint64    `json:"system_memory_total"`
	SystemMemoryPct   float64   `json:"system_memory_percent"`
	SystemDiskUsed    uint64    `json:"system_disk_used"`
	SystemDiskTotal   uint64    `json:"system_disk_total"`
	SystemDiskPct     float64   `json:"system_disk_percent"`
}

type Hub struct {
	httpRequests      atomic.Uint64
	httpActive        atomic.Int64
	httpInboundBytes  atomic.Uint64
	httpOutboundBytes atomic.Uint64
	tcpConnections    atomic.Uint64
	tcpActive         atomic.Int64
	tcpInboundBytes   atomic.Uint64
	tcpOutboundBytes  atomic.Uint64

	mu              sync.RWMutex
	httpRoutes      int
	httpEnabled     int
	tcpRoutes       int
	tcpEnabled      int
	sshTunnels      int
	systemCPU       float64
	systemMemUsed   uint64
	systemMemTotal  uint64
	systemMemPct    float64
	systemDiskUsed  uint64
	systemDiskTotal uint64
	systemDiskPct   float64
	subscribers     map[chan Snapshot]struct{}
}

func NewHub() *Hub {
	return &Hub{
		subscribers: make(map[chan Snapshot]struct{}),
	}
}

var Default = NewHub()

func (h *Hub) Snapshot() Snapshot {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return Snapshot{
		UpdatedAt:         time.Now(),
		HTTPRequests:      h.httpRequests.Load(),
		HTTPActive:        h.httpActive.Load(),
		HTTPInboundBytes:  h.httpInboundBytes.Load(),
		HTTPOutboundBytes: h.httpOutboundBytes.Load(),
		TCPConnections:    h.tcpConnections.Load(),
		TCPActive:         h.tcpActive.Load(),
		TCPInboundBytes:   h.tcpInboundBytes.Load(),
		TCPOutboundBytes:  h.tcpOutboundBytes.Load(),
		HTTPRoutes:        h.httpRoutes,
		HTTPEnabledRoutes: h.httpEnabled,
		TCPRoutes:         h.tcpRoutes,
		TCPEnabledRoutes:  h.tcpEnabled,
		SSHTunnels:        h.sshTunnels,
		SystemCPUPercent:  h.systemCPU,
		SystemMemoryUsed:  h.systemMemUsed,
		SystemMemoryTotal: h.systemMemTotal,
		SystemMemoryPct:   h.systemMemPct,
		SystemDiskUsed:    h.systemDiskUsed,
		SystemDiskTotal:   h.systemDiskTotal,
		SystemDiskPct:     h.systemDiskPct,
	}
}

func (h *Hub) Publish() {
	snapshot := h.Snapshot()
	h.mu.RLock()
	defer h.mu.RUnlock()
	for ch := range h.subscribers {
		select {
		case ch <- snapshot:
		default:
		}
	}
}

func (h *Hub) Subscribe() (<-chan Snapshot, func()) {
	ch := make(chan Snapshot, 8)
	h.mu.Lock()
	h.subscribers[ch] = struct{}{}
	h.mu.Unlock()

	ch <- h.Snapshot()

	cancel := func() {
		h.mu.Lock()
		if _, ok := h.subscribers[ch]; ok {
			delete(h.subscribers, ch)
			close(ch)
		}
		h.mu.Unlock()
	}
	return ch, cancel
}

func (h *Hub) ApplyConfig(cfg config.Config) {
	httpEnabled := 0
	for _, route := range cfg.Routes {
		if route.Enabled {
			httpEnabled++
		}
	}
	tcpEnabled := 0
	for _, route := range cfg.TcpRoutes {
		if route.Enabled {
			tcpEnabled++
		}
	}
	sshEnabled := 0
	for _, tunnel := range cfg.SSHTunnels {
		if tunnel.Enabled {
			sshEnabled++
		}
	}

	h.mu.Lock()
	h.httpRoutes = len(cfg.Routes)
	h.httpEnabled = httpEnabled
	h.tcpRoutes = len(cfg.TcpRoutes)
	h.tcpEnabled = tcpEnabled
	h.sshTunnels = sshEnabled
	h.mu.Unlock()
	h.Publish()
}

func (h *Hub) UpdateSystemMetrics(cpuPercent float64, memoryUsed, memoryTotal uint64, memoryPercent float64, diskUsed, diskTotal uint64, diskPercent float64) {
	h.mu.Lock()
	h.systemCPU = cpuPercent
	h.systemMemUsed = memoryUsed
	h.systemMemTotal = memoryTotal
	h.systemMemPct = memoryPercent
	h.systemDiskUsed = diskUsed
	h.systemDiskTotal = diskTotal
	h.systemDiskPct = diskPercent
	h.mu.Unlock()
	h.Publish()
}

func (h *Hub) RecordHTTP(inboundBytes, outboundBytes uint64) {
	h.httpRequests.Add(1)
	h.httpInboundBytes.Add(inboundBytes)
	h.httpOutboundBytes.Add(outboundBytes)
	h.Publish()
}

func (h *Hub) HTTPStarted() {
	h.httpActive.Add(1)
	h.Publish()
}

func (h *Hub) HTTPFinished() {
	h.httpActive.Add(-1)
	h.Publish()
}

func (h *Hub) TCPStarted() {
	h.tcpConnections.Add(1)
	h.tcpActive.Add(1)
	h.Publish()
}

func (h *Hub) TCPFinished(inboundBytes, outboundBytes uint64) {
	h.tcpInboundBytes.Add(inboundBytes)
	h.tcpOutboundBytes.Add(outboundBytes)
	h.tcpActive.Add(-1)
	h.Publish()
}

func WatchConfig(ctx context.Context, mgr *config.ConfigManager, interval time.Duration) {
	if interval <= 0 {
		interval = time.Second
	}
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()

		var last string
		for {
			cfg := mgr.Get()
			data, _ := json.Marshal(cfg)
			current := string(data)
			if current != last {
				last = current
				Default.ApplyConfig(cfg)
			}

			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	}()
}

func WatchSystem(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		interval = 5 * time.Second
	}
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		sampleSystem()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				sampleSystem()
			}
		}
	}()
}

func sampleSystem() {
	var cpuPercent float64
	if values, err := cpu.Percent(0, false); err == nil && len(values) > 0 {
		cpuPercent = values[0]
	}

	var memoryUsed uint64
	var memoryTotal uint64
	var memoryPercent float64
	if vm, err := mem.VirtualMemory(); err == nil {
		memoryUsed = vm.Used
		memoryTotal = vm.Total
		memoryPercent = vm.UsedPercent
	}

	var diskUsed uint64
	var diskTotal uint64
	var diskPercent float64
	path, _ := os.Getwd()
	if usage, err := disk.Usage(path); err == nil {
		diskUsed = usage.Used
		diskTotal = usage.Total
		diskPercent = usage.UsedPercent
	}

	Default.UpdateSystemMetrics(cpuPercent, memoryUsed, memoryTotal, memoryPercent, diskUsed, diskTotal, diskPercent)
}
