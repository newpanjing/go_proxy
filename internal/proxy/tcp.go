package proxy

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"strings"
	"sync"
	"time"

	"go-proxy/internal/config"
	"go-proxy/internal/logstream"
	"go-proxy/internal/telemetry"
)

// TcpManager manages TCP proxy listeners and their lifecycle.
type TcpManager struct {
	mgr       *config.ConfigManager
	mu        sync.Mutex
	listeners map[string]net.Listener
	done      chan struct{}
}

// NewTcpManager creates a new TCP proxy manager.
func NewTcpManager(mgr *config.ConfigManager) *TcpManager {
	return &TcpManager{
		mgr:       mgr,
		listeners: make(map[string]net.Listener),
		done:      make(chan struct{}),
	}
}

// Start starts TCP listeners for all configured TCP routes.
func (tm *TcpManager) Start() error {
	tm.reconcile(tm.mgr.Get().TcpRoutes)
	return nil
}

// Reload reconciles listeners against current config without restarting healthy ports.
func (tm *TcpManager) Reload() {
	tm.reconcile(tm.mgr.Get().TcpRoutes)
}

func (tm *TcpManager) reconcile(routes []config.TcpRoute) {
	tm.mu.Lock()
	defer tm.mu.Unlock()

	desired := make(map[string]config.TcpRoute, len(routes))
	for i := range routes {
		route := routes[i]
		if route.IsEnabled() {
			desired[route.Listen] = route
		}
	}

	for addr, ln := range tm.listeners {
		if _, ok := desired[addr]; ok {
			continue
		}
		_ = ln.Close()
		delete(tm.listeners, addr)
		log.Printf("[tcp] stopped listener on %s", addr)
	}

	for addr, route := range desired {
		if _, ok := tm.listeners[addr]; ok {
			continue
		}
		routeCopy := route
		if err := tm.startListener(&routeCopy); err != nil {
			log.Printf("[tcp] failed to listen on %s: %v", route.Listen, err)
			continue
		}
	}

	log.Printf("[tcp] active listeners: %d", len(tm.listeners))
}

func (tm *TcpManager) closeListenersLocked() {
	for addr, ln := range tm.listeners {
		_ = ln.Close()
		delete(tm.listeners, addr)
	}
}

// Close shuts down all TCP listeners.
func (tm *TcpManager) Close() {
	select {
	case <-tm.done:
	default:
		close(tm.done)
	}
	tm.mu.Lock()
	defer tm.mu.Unlock()
	tm.closeListenersLocked()
}

func (tm *TcpManager) startListener(route *config.TcpRoute) error {
	ln, err := net.Listen("tcp", route.Listen)
	if err != nil {
		return fmt.Errorf("listen %s: %w", route.Listen, err)
	}

	tm.listeners[route.Listen] = ln
	log.Printf("[tcp] listening on %s", route.Listen)

	go tm.acceptLoop(ln, route.Listen)

	return nil
}

func (tm *TcpManager) acceptLoop(ln net.Listener, listenAddr string) {
	for {
		conn, err := ln.Accept()
		if err != nil {
			select {
			case <-tm.done:
				return
			default:
				if isClosedListenerError(err) {
					return
				}
				log.Printf("[tcp] accept error on %s: %v", listenAddr, err)
				return
			}
		}

		go tm.handleConnection(conn, listenAddr)
	}
}

func (tm *TcpManager) handleConnection(clientConn net.Conn, listenAddr string) {
	start := time.Now()
	src := clientConn.RemoteAddr().String()
	telemetry.Default.TCPStarted()
	defer func() {
		clientConn.Close()
	}()

	cfg := tm.mgr.Get()

	// Find the matching TCP route
	var route *config.TcpRoute
	for i := range cfg.TcpRoutes {
		if cfg.TcpRoutes[i].Listen == listenAddr {
			route = &cfg.TcpRoutes[i]
			break
		}
	}

	if route == nil {
		log.Printf("[tcp] %s -> no route found for %s (config changed?)", src, listenAddr)
		telemetry.Default.TCPFinished(0, 0)
		return
	}

	// Filter enabled upstreams
	var enabledUpstreams []config.Upstream
	for _, u := range route.Upstreams {
		if u.IsEnabled() {
			enabledUpstreams = append(enabledUpstreams, u)
		}
	}

	if len(enabledUpstreams) == 0 {
		log.Printf("[tcp] %s -> no enabled upstreams for %s", src, listenAddr)
		telemetry.Default.TCPFinished(0, 0)
		return
	}

	// Select upstream using weighted round-robin
	selected := selectTcpUpstream(listenAddr, enabledUpstreams)
	if selected == nil {
		log.Printf("[tcp] %s -> all upstreams exhausted for %s", src, listenAddr)
		telemetry.Default.TCPFinished(0, 0)
		return
	}

	// Try connecting to upstream, with backup fallback
	upstreamConn, upstream := tm.dialUpstream(listenAddr, enabledUpstreams, selected)
	if upstreamConn == nil {
		log.Printf("[tcp] %s -> failed to connect to any upstream for %s", src, listenAddr)
		telemetry.Default.TCPFinished(0, 0)
		return
	}
	defer upstreamConn.Close()

	log.Printf("[tcp] %s -> %s (%s) %dms", src, upstream.Target, listenAddr, time.Since(start).Milliseconds())

	// Bidirectional copy
	inboundDone := make(chan uint64, 1)

	go func() {
		n, _ := io.Copy(upstreamConn, clientConn)
		_ = upstreamConn.Close()
		inboundDone <- uint64(n)
	}()

	n, _ := io.Copy(clientConn, upstreamConn)
	outbound := uint64(n)
	inbound := <-inboundDone
	totalBytes := inbound + outbound
	durationMS := time.Since(start).Milliseconds()
	telemetry.Default.TCPFinished(inbound, outbound)
	logstream.Default.AddEntry(logstream.Entry{
		Kind:       "tcp",
		Listen:     listenAddr,
		Source:     src,
		Target:     upstream.Target,
		Bytes:      totalBytes,
		DurationMS: durationMS,
		Message:    fmt.Sprintf("%s [tcp] %s -> %s size=%dB latency=%dms", time.Now().Format("15:04:05"), src, upstream.Target, totalBytes, durationMS),
	})
}

func (tm *TcpManager) dialUpstream(listenAddr string, upstreams []config.Upstream, primary *config.Upstream) (net.Conn, *config.Upstream) {
	// Try primary first
	conn, err := net.DialTimeout("tcp", primary.Target, 10*time.Second)
	if err == nil {
		return conn, primary
	}

	log.Printf("[tcp] primary %s failed: %v, trying backups", primary.Target, err)

	// Try other non-backup upstreams
	for i := range upstreams {
		if upstreams[i].Target == primary.Target || upstreams[i].Backup {
			continue
		}
		conn, err := net.DialTimeout("tcp", upstreams[i].Target, 10*time.Second)
		if err == nil {
			return conn, &upstreams[i]
		}
	}

	// Try backups
	for i := range upstreams {
		if !upstreams[i].Backup {
			continue
		}
		conn, err := net.DialTimeout("tcp", upstreams[i].Target, 10*time.Second)
		if err == nil {
			return conn, &upstreams[i]
		}
	}

	return nil, nil
}

func selectTcpUpstream(listenAddr string, upstreams []config.Upstream) *config.Upstream {
	return weightedRoundRobin("tcp-"+listenAddr, upstreams)
}

func (tm *TcpManager) Watch(interval time.Duration) {
	if interval <= 0 {
		interval = time.Second
	}
	lastSnapshot := tm.snapshot()
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-tm.done:
			return
		case <-ticker.C:
			current := tm.snapshot()
			if current == lastSnapshot {
				continue
			}
			lastSnapshot = current
			tm.Reload()
		}
	}
}

func (tm *TcpManager) snapshot() string {
	data, err := json.Marshal(tm.mgr.Get().TcpRoutes)
	if err != nil {
		return ""
	}
	return string(data)
}

func isClosedListenerError(err error) bool {
	if err == nil {
		return false
	}
	if ne, ok := err.(*net.OpError); ok && ne.Err != nil {
		return strings.Contains(strings.ToLower(ne.Err.Error()), "closed")
	}
	return strings.Contains(strings.ToLower(err.Error()), "closed network connection")
}
