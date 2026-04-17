package proxy

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
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
	"go-proxy/internal/telemetry"
)

// ANSI color codes for terminal output
const (
	colorReset  = "\033[0m"
	colorRed    = "\033[31m"
	colorYellow = "\033[33m"
	colorGreen  = "\033[32m"
	colorCyan   = "\033[36m"
	colorBlue   = "\033[34m"
)

// statusColor returns the ANSI color for a given HTTP status code.
func statusColor(code int) string {
	switch {
	case code >= 500:
		return colorRed
	case code == 404:
		return colorYellow
	case code >= 400:
		return colorYellow
	case code >= 300:
		return colorBlue
	case code >= 200:
		return colorGreen
	default:
		return colorReset
	}
}

func methodColor(method string) string {
	switch strings.ToUpper(strings.TrimSpace(method)) {
	case http.MethodGet:
		return colorGreen
	case http.MethodPost:
		return colorBlue
	case http.MethodPut, http.MethodPatch:
		return colorYellow
	case http.MethodDelete:
		return colorRed
	default:
		return colorCyan
	}
}

// logRequest logs a proxied request with colored output.
func logRequest(src, method, path, target string, statusCode int, duration int64, queryParams string, logParams bool) {
	statusStr := fmt.Sprintf("%s%d%s", statusColor(statusCode), statusCode, colorReset)
	srcStr := fmt.Sprintf("%s%s%s", colorCyan, src, colorReset)
	methodStr := fmt.Sprintf("%s%s%s", methodColor(method), method, colorReset)
	targetStr := fmt.Sprintf("%s%s%s", colorBlue, target, colorReset)
	durStr := fmt.Sprintf("%s%dms%s", colorGreen, duration, colorReset)

	var paramStr string
	if logParams && queryParams != "" {
		paramStr = fmt.Sprintf(" params=%s%s%s", colorYellow, queryParams, colorReset)
	}

	log.Printf("[proxy] %s %s %s -> %s %s %s%s",
		srcStr, methodStr, path, targetStr, statusStr, durStr, paramStr)
}

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
		if !u.IsEnabled() {
			continue
		}
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
		if !upstreams[i].IsEnabled() {
			continue
		}
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
	telemetry.Default.HTTPStarted()
	defer telemetry.Default.HTTPFinished()

	if route, ok := selectMatchingRoute(cfg.Routes, r.URL.Path); ok {
		p.handleMatchedRoute(w, r, route, start)
		return
	}

	rw := &responseWriter{ResponseWriter: w, status: 200}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	rw.WriteHeader(http.StatusNotFound)
	n, _ := fmt.Fprintf(rw, `{"status":404,"message":"no matching route","path":"%s","hint":"check proxy config or visit admin GUI to add routes"}`, r.URL.Path)
	telemetry.Default.RecordHTTP(requestSize(r), uint64(n))
	logRequest(src, r.Method, r.URL.Path, "", http.StatusNotFound, time.Since(start).Milliseconds(), r.URL.RawQuery, cfg.LogRequestParams)
}

func selectMatchingRoute(routes []config.Route, path string) (config.Route, bool) {
	bestIdx := -1
	bestPriority := 0

	for i, route := range routes {
		if !route.IsEnabled() {
			continue
		}
		if !strings.HasPrefix(path, route.Path) {
			continue
		}
		if bestIdx == -1 || route.Priority > bestPriority {
			bestIdx = i
			bestPriority = route.Priority
		}
	}

	if bestIdx == -1 {
		return config.Route{}, false
	}
	return routes[bestIdx], true
}

func (p *Proxy) handleMatchedRoute(w http.ResponseWriter, r *http.Request, route config.Route, start time.Time) {
	if route.IsMock() {
		p.handleMockRoute(w, r, route, start)
		return
	}
	p.handleRoute(w, r, route, start, false)
}

func (p *Proxy) handleRoute(w http.ResponseWriter, r *http.Request, route config.Route, start time.Time, isRetry bool) {
	src := r.RemoteAddr
	cfg := p.mgr.Get()
	upstreams := route.ResolveUpstreams()
	requestBytes := requestSize(r)
	if len(upstreams) == 0 {
		http.Error(w, "no upstream configured", http.StatusBadGateway)
		telemetry.Default.RecordHTTP(requestBytes, 0)
		logRequest(src, r.Method, r.URL.Path, "", http.StatusBadGateway, time.Since(start).Milliseconds(), r.URL.RawQuery, cfg.LogRequestParams)
		return
	}

	selected := selectUpstream(route.Path, upstreams, isRetry)
	if selected == nil {
		http.Error(w, "all upstreams exhausted", http.StatusBadGateway)
		telemetry.Default.RecordHTTP(requestBytes, 0)
		logRequest(src, r.Method, r.URL.Path, "", http.StatusBadGateway, time.Since(start).Milliseconds(), r.URL.RawQuery, cfg.LogRequestParams)
		return
	}

	targetURL, err := url.Parse(selected.Target)
	if err != nil {
		http.Error(w, fmt.Sprintf("bad upstream target: %v", err), http.StatusBadGateway)
		telemetry.Default.RecordHTTP(requestBytes, 0)
		logRequest(src, r.Method, r.URL.Path, "", http.StatusBadGateway, time.Since(start).Milliseconds(), r.URL.RawQuery, cfg.LogRequestParams)
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
		outbound := uint64(0)
		if resp.ContentLength > 0 {
			outbound = uint64(resp.ContentLength)
		}
		telemetry.Default.RecordHTTP(requestBytes, outbound)
		logRequest(src, r.Method, r.URL.Path, selected.Target+forwardPath, resp.StatusCode, time.Since(start).Milliseconds(), r.URL.RawQuery, cfg.LogRequestParams)
		return nil
	}

	rp.ErrorHandler = func(w http.ResponseWriter, r *http.Request, err error) {
		if isTimeoutError(err) {
			http.Error(w, fmt.Sprintf("gateway timeout: %v", err), http.StatusGatewayTimeout)
			telemetry.Default.RecordHTTP(requestBytes, 0)
			logRequest(src, r.Method, r.URL.Path, selected.Target+forwardPath, http.StatusGatewayTimeout, time.Since(start).Milliseconds(), r.URL.RawQuery, cfg.LogRequestParams)
		} else {
			logRequest(src, r.Method, r.URL.Path, selected.Target+forwardPath, http.StatusBadGateway, time.Since(start).Milliseconds(), r.URL.RawQuery, cfg.LogRequestParams)
			// Try backup if available and not already a retry
			if !isRetry && hasBackup(upstreams) {
				p.handleRoute(w, r, route, start, true)
				return
			}
			http.Error(w, fmt.Sprintf("bad gateway: %v", err), http.StatusBadGateway)
			telemetry.Default.RecordHTTP(requestBytes, 0)
		}
	}

	rp.ServeHTTP(w, r)
}

func (p *Proxy) handleMockRoute(w http.ResponseWriter, r *http.Request, route config.Route, start time.Time) {
	src := r.RemoteAddr
	cfg := p.mgr.Get()
	target := "mock://" + route.Path
	mock := route.Mock
	if mock == nil {
		payload := map[string]interface{}{
			"code":    50001,
			"message": "mock route misconfigured",
			"data": map[string]interface{}{
				"path": route.Path,
			},
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.Header().Set("X-Go-Proxy-Mock", "true")
		w.WriteHeader(http.StatusInternalServerError)
		data, _ := json.Marshal(payload)
		_, _ = w.Write(append(data, '\n'))
		telemetry.Default.RecordHTTP(requestSize(r), uint64(len(data)+1))
		logRequest(src, r.Method, r.URL.Path, target, http.StatusInternalServerError, time.Since(start).Milliseconds(), r.URL.RawQuery, cfg.LogRequestParams)
		return
	}

	method := strings.ToUpper(strings.TrimSpace(mock.Method))
	if method == "" {
		method = "ANY"
	}
	if method != "ANY" && r.Method != method {
		payload := map[string]interface{}{
			"code":    40501,
			"message": "mock method mismatch",
			"data": map[string]interface{}{
				"expected_method": method,
				"actual_method":   r.Method,
				"path":            r.URL.Path,
			},
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.Header().Set("X-Go-Proxy-Mock", "true")
		w.WriteHeader(http.StatusMethodNotAllowed)
		data, _ := json.Marshal(payload)
		_, _ = w.Write(append(data, '\n'))
		telemetry.Default.RecordHTTP(requestSize(r), uint64(len(data)+1))
		logRequest(src, r.Method, r.URL.Path, target, http.StatusMethodNotAllowed, time.Since(start).Milliseconds(), r.URL.RawQuery, cfg.LogRequestParams)
		return
	}

	requestParams := collectRequestParams(r)
	if mismatch := findMockParamMismatch(mock.Params, requestParams); len(mismatch) > 0 {
		payload := map[string]interface{}{
			"code":    40001,
			"message": "mock params mismatch",
			"data": map[string]interface{}{
				"expected_params": mock.Params,
				"actual_params":   requestParams,
				"mismatch":        mismatch,
				"path":            r.URL.Path,
			},
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.Header().Set("X-Go-Proxy-Mock", "true")
		w.WriteHeader(http.StatusBadRequest)
		data, _ := json.Marshal(payload)
		_, _ = w.Write(append(data, '\n'))
		telemetry.Default.RecordHTTP(requestSize(r), uint64(len(data)+1))
		logRequest(src, r.Method, r.URL.Path, target, http.StatusBadRequest, time.Since(start).Milliseconds(), r.URL.RawQuery, cfg.LogRequestParams)
		return
	}

	statusCode := mock.StatusCode
	if statusCode == 0 {
		statusCode = http.StatusOK
	}

	// Set response type
	contentType := strings.TrimSpace(mock.ResponseType)
	if contentType == "" {
		contentType = "application/json"
	}
	w.Header().Set("Content-Type", contentType)

	// Set custom response headers
	for k, v := range mock.Headers {
		w.Header().Set(k, v)
	}

	w.Header().Set("X-Go-Proxy-Mock", "true")
	w.WriteHeader(statusCode)

	// Output raw data as-is
	outbound := uint64(0)
	if mock.Data != nil {
		data, err := json.Marshal(mock.Data)
		if err == nil {
			_, _ = w.Write(append(data, '\n'))
			outbound = uint64(len(data) + 1)
		}
	}
	telemetry.Default.RecordHTTP(requestSize(r), outbound)

	logRequest(src, r.Method, r.URL.Path, target, statusCode, time.Since(start).Milliseconds(), r.URL.RawQuery, cfg.LogRequestParams)
}

func collectRequestParams(r *http.Request) map[string]string {
	params := make(map[string]string)

	for key, values := range r.URL.Query() {
		params[key] = strings.Join(values, ",")
	}

	contentType := strings.ToLower(r.Header.Get("Content-Type"))
	switch {
	case strings.Contains(contentType, "application/json"):
		body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
		if err == nil && len(strings.TrimSpace(string(body))) > 0 {
			var payload map[string]interface{}
			if err := json.Unmarshal(body, &payload); err == nil {
				for key, value := range payload {
					params[key] = stringifyParam(value)
				}
			} else {
				params["_body"] = string(body)
			}
		}
	case strings.Contains(contentType, "application/x-www-form-urlencoded"),
		strings.Contains(contentType, "multipart/form-data"):
		if err := r.ParseForm(); err == nil {
			for key, values := range r.PostForm {
				params[key] = strings.Join(values, ",")
			}
		}
	}

	return params
}

func stringifyParam(v interface{}) string {
	switch val := v.(type) {
	case nil:
		return ""
	case string:
		return val
	case bool, float64, float32, int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64:
		return fmt.Sprint(val)
	default:
		data, err := json.Marshal(val)
		if err != nil {
			return fmt.Sprint(val)
		}
		return string(data)
	}
}

func findMockParamMismatch(expected, actual map[string]string) map[string]string {
	mismatch := make(map[string]string)
	for key, value := range expected {
		if actual[key] != value {
			mismatch[key] = fmt.Sprintf("expected=%q actual=%q", value, actual[key])
		}
	}
	return mismatch
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

func requestSize(r *http.Request) uint64 {
	if r == nil || r.ContentLength <= 0 {
		return 0
	}
	return uint64(r.ContentLength)
}

func Start(mgr *config.ConfigManager) error {
	cfg := mgr.Get()
	p := New(mgr)

	mux := http.NewServeMux()
	mux.Handle("/", p)

	addr := fmt.Sprintf(":%d", cfg.Port)
	log.Printf("[proxy] listening on %s", addr)

	// Start TCP proxy listeners
	tcpMgr := NewTcpManager(mgr)
	if err := tcpMgr.Start(); err != nil {
		log.Printf("[tcp] warning: %v", err)
	}
	go tcpMgr.Watch(time.Second)
	defer tcpMgr.Close()

	sshMgr := NewSSHManager(mgr)
	if err := sshMgr.Start(); err != nil {
		log.Printf("[ssh] warning: %v", err)
	}
	go sshMgr.Watch(time.Second)
	defer sshMgr.Close()

	server := &http.Server{
		Addr:         addr,
		Handler:      mux,
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 60 * time.Second,
		IdleTimeout:  120 * time.Second,
	}

	return server.ListenAndServe()
}
