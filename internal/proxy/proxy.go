package proxy

import (
	"context"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"go-proxy/internal/config"
)

type Proxy struct {
	mgr    *config.ConfigManager
	logger *log.Logger
}

func New(mgr *config.ConfigManager) *Proxy {
	return &Proxy{mgr: mgr}
}

// --- Weighted Round-Robin ---

type wrrState struct {
	mu      sync.Mutex
	current int32
}

var (
	wrrCounters   = make(map[string]*wrrState)
	wrrCountersMu sync.Mutex
)

func getWRR(key string) *wrrState {
	wrrCountersMu.Lock()
	defer wrrCountersMu.Unlock()
	if s, ok := wrrCounters[key]; ok {
		return s
	}
	s := &wrrState{}
	wrrCounters[key] = s
	return s
}

// weightedRoundRobin selects an upstream using weighted round-robin.
// Falls back to backup nodes when all primaries fail.
func weightedRoundRobin(routePath string, upstreams []config.Upstream) *config.Upstream {
	var primaries, backups []config.Upstream
	for i := range upstreams {
		u := &upstreams[i]
		if u.Backup {
			backups = append(backups, *u)
		} else {
			primaries = append(primaries, *u)
		}
	}

	targets := primaries
	if len(targets) == 0 {
		targets = backups
	}
	if len(targets) == 0 {
		return nil
	}

	// Flatten weights for round-robin
	// weight 0 is treated as 1
	var expanded []config.Upstream
	for _, u := range targets {
		w := u.Weight
		if w <= 0 {
			w = 1
		}
		for j := 0; j < w; j++ {
			expanded = append(expanded, u)
		}
	}

	if len(expanded) == 0 {
		return nil
	}

	state := getWRR(routePath)
	state.mu.Lock()
	defer state.mu.Unlock()

	idx := int(atomic.AddInt32(&state.current, 1)) % len(expanded)
	selected := expanded[idx]
	return &selected
}

// selectUpstream picks a target, trying round-robin first, then backup on failure.
func selectUpstream(routePath string, upstreams []config.Upstream, tryBackup bool) *config.Upstream {
	if !tryBackup {
		return weightedRoundRobin(routePath, upstreams)
	}
	// When backup is needed, skip primaries, only use backups
	var backups []config.Upstream
	for i := range upstreams {
		if upstreams[i].Backup {
			backups = append(backups, upstreams[i])
		}
	}
	if len(backups) == 0 {
		return nil
	}
	return weightedRoundRobin(routePath, backups)
}

// --- Proxy handler ---

func (p *Proxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	src := r.RemoteAddr
	cfg := p.mgr.Get()

	for _, route := range cfg.Routes {
		if strings.HasPrefix(r.URL.Path, route.Path) {
			p.handleRoute(w, r, route, start, false)
			return
		}
	}

	rw := &responseWriter{ResponseWriter: w, status: 200}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	rw.WriteHeader(http.StatusNotFound)
	fmt.Fprintf(rw, `{"status":404,"message":"no matching route","path":"%s","hint":"check proxy config or visit admin GUI to add routes"}`, r.URL.Path)
	log.Printf("[proxy] %s %s %s -> 404 %dms (no route)",
		src, r.Method, r.URL.Path, time.Since(start).Milliseconds())
}

func (p *Proxy) handleRoute(w http.ResponseWriter, r *http.Request, route config.Route, start time.Time, isRetry bool) {
	src := r.RemoteAddr
	upstreams := route.ResolveUpstreams()
	if len(upstreams) == 0 {
		http.Error(w, "no upstream configured", http.StatusBadGateway)
		log.Printf("[proxy] %s %s %s -> 502 %dms (no upstream)", src, r.Method, r.URL.Path, time.Since(start).Milliseconds())
		return
	}

	selected := selectUpstream(route.Path, upstreams, isRetry)
	if selected == nil {
		http.Error(w, "all upstreams exhausted", http.StatusBadGateway)
		log.Printf("[proxy] %s %s %s -> 502 %dms (all upstreams exhausted)", src, r.Method, r.URL.Path, time.Since(start).Milliseconds())
		return
	}

	targetURL, err := url.Parse(selected.Target)
	if err != nil {
		http.Error(w, fmt.Sprintf("bad upstream target: %v", err), http.StatusBadGateway)
		log.Printf("[proxy] %s %s %s -> 502 %dms (bad target %s: %v)", src, r.Method, r.URL.Path, time.Since(start).Milliseconds(), selected.Target, err)
		return
	}

	// Determine forward path
	forwardPath := r.URL.Path
	if route.StripPrefix {
		forwardPath = strings.TrimPrefix(forwardPath, route.Path)
		if !strings.HasPrefix(forwardPath, "/") {
			forwardPath = "/" + forwardPath
		}
	}

	timeout := time.Duration(route.Timeout) * time.Second
	if timeout == 0 {
		timeout = 30 * time.Second
	}

	rp := httputil.NewSingleHostReverseProxy(targetURL)

	rp.Transport = &http.Transport{
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			dialer := &net.Dialer{
				Timeout:   timeout,
				KeepAlive: 30 * time.Second,
			}
			return dialer.DialContext(ctx, network, addr)
		},
		TLSHandshakeTimeout:   10 * time.Second,
		ResponseHeaderTimeout: timeout,
		MaxIdleConns:          100,
		IdleConnTimeout:       90 * time.Second,
	}

	originalDirector := rp.Director
	rp.Director = func(req *http.Request) {
		req.URL.Scheme = targetURL.Scheme
		req.URL.Host = targetURL.Host
		req.URL.Path = forwardPath
		req.URL.RawQuery = r.URL.RawQuery
		req.Host = targetURL.Host

		if req.Header.Get("X-Forwarded-For") == "" {
			req.Header.Set("X-Forwarded-For", r.RemoteAddr)
		}
		if req.Header.Get("X-Forwarded-Host") == "" {
			req.Header.Set("X-Forwarded-Host", r.Host)
		}
		if req.Header.Get("X-Forwarded-Proto") == "" {
			req.Header.Set("X-Forwarded-Proto", "http")
		}

		for k, v := range route.Headers {
			req.Header.Set(k, v)
		}

		originalDirector(req)
	}

	rp.ModifyResponse = func(resp *http.Response) error {
		log.Printf("[proxy] %s %s %s -> %s%s %d %dms",
			src, r.Method, r.URL.Path, selected.Target, forwardPath, resp.StatusCode, time.Since(start).Milliseconds())
		return nil
	}

	rp.ErrorHandler = func(w http.ResponseWriter, r *http.Request, err error) {
		if isTimeoutError(err) {
			http.Error(w, fmt.Sprintf("gateway timeout: %v", err), http.StatusGatewayTimeout)
			log.Printf("[proxy] %s %s %s -> %s%s 504 %dms (timeout: %v)",
				src, r.Method, r.URL.Path, selected.Target, forwardPath, time.Since(start).Milliseconds(), err)
		} else {
			log.Printf("[proxy] %s %s %s -> %s%s 502 %dms (%v) trying next upstream...",
				src, r.Method, r.URL.Path, selected.Target, forwardPath, time.Since(start).Milliseconds(), err)
			// Try backup if available and not already a retry
			if !isRetry && hasBackup(upstreams) {
				p.handleRoute(w, r, route, start, true)
				return
			}
			http.Error(w, fmt.Sprintf("bad gateway: %v", err), http.StatusBadGateway)
		}
	}

	rp.ServeHTTP(w, r)
}

func hasBackup(upstreams []config.Upstream) bool {
	for _, u := range upstreams {
		if u.Backup {
			return true
		}
	}
	return false
}

type responseWriter struct {
	http.ResponseWriter
	status int
}

func (w *responseWriter) WriteHeader(code int) {
	w.status = code
	w.ResponseWriter.WriteHeader(code)
}

func isTimeoutError(err error) bool {
	if err == nil {
		return false
	}
	if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
		return true
	}
	if strings.Contains(err.Error(), "timeout") || strings.Contains(err.Error(), "deadline exceeded") {
		return true
	}
	return false
}

func Start(mgr *config.ConfigManager) error {
	cfg := mgr.Get()
	p := New(mgr)

	mux := http.NewServeMux()
	mux.Handle("/", p)

	addr := fmt.Sprintf(":%d", cfg.Port)
	log.Printf("[proxy] listening on %s", addr)

	server := &http.Server{
		Addr:         addr,
		Handler:      mux,
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 60 * time.Second,
		IdleTimeout:  120 * time.Second,
	}

	return server.ListenAndServe()
}
