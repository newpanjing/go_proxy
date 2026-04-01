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
	"time"

	"go-proxy/internal/config"
)

type Proxy struct {
	mgr *config.ConfigManager
}

func New(mgr *config.ConfigManager) *Proxy {
	return &Proxy{mgr: mgr}
}

func (p *Proxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	src := r.RemoteAddr
	cfg := p.mgr.Get()

	for _, route := range cfg.Routes {
		if strings.HasPrefix(r.URL.Path, route.Path) {
			p.handleRoute(w, r, route, start)
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

func (p *Proxy) handleRoute(w http.ResponseWriter, r *http.Request, route config.Route, start time.Time) {
	src := r.RemoteAddr
	target, err := url.Parse(route.Target)
	if err != nil {
		rw := &responseWriter{ResponseWriter: w, status: 200}
		http.Error(rw, fmt.Sprintf("bad target: %v", err), http.StatusBadGateway)
		log.Printf("[proxy] %s %s %s -> %s 502 %dms (bad target: %v)",
			src, r.Method, r.URL.Path, route.Target, time.Since(start).Milliseconds(), err)
		return
	}

	// Determine the path to forward
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

	rp := httputil.NewSingleHostReverseProxy(target)

	// Custom Transport with timeout
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
		req.URL.Scheme = target.Scheme
		req.URL.Host = target.Host
		req.URL.Path = forwardPath
		req.URL.RawQuery = r.URL.RawQuery
		req.Host = target.Host

		if req.Header.Get("X-Forwarded-For") == "" {
			req.Header.Set("X-Forwarded-For", r.RemoteAddr)
		}
		if req.Header.Get("X-Forwarded-Host") == "" {
			req.Header.Set("X-Forwarded-Host", r.Host)
		}
		if req.Header.Get("X-Forwarded-Proto") == "" {
			req.Header.Set("X-Forwarded-Proto", "http")
		}

		// Apply custom headers from route config
		for k, v := range route.Headers {
			req.Header.Set(k, v)
		}

		originalDirector(req)
	}

	rp.ModifyResponse = func(resp *http.Response) error {
		log.Printf("[proxy] %s %s %s -> %s%s %d %dms",
			src, r.Method, r.URL.Path, route.Target, forwardPath, resp.StatusCode, time.Since(start).Milliseconds())
		return nil
	}

	rp.ErrorHandler = func(w http.ResponseWriter, r *http.Request, err error) {
		if isTimeoutError(err) {
			http.Error(w, fmt.Sprintf("gateway timeout: %v", err), http.StatusGatewayTimeout)
			log.Printf("[proxy] %s %s %s -> %s%s 504 %dms (timeout: %v)",
				src, r.Method, r.URL.Path, route.Target, forwardPath, time.Since(start).Milliseconds(), err)
		} else {
			http.Error(w, fmt.Sprintf("bad gateway: %v", err), http.StatusBadGateway)
			log.Printf("[proxy] %s %s %s -> %s%s 502 %dms (%v)",
				src, r.Method, r.URL.Path, route.Target, forwardPath, time.Since(start).Milliseconds(), err)
		}
	}

	rp.ServeHTTP(w, r)
}

// responseWriter wraps http.ResponseWriter to capture status code
type responseWriter struct {
	http.ResponseWriter
	status int
}

func (w *responseWriter) WriteHeader(code int) {
	w.status = code
	w.ResponseWriter.WriteHeader(code)
}

// isTimeoutError checks if the error is a timeout (context deadline, dial timeout, etc.)
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
