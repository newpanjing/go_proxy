package admin

import (
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"time"

	"go-proxy/internal/config"
	"go-proxy/internal/logstream"
	"go-proxy/internal/telemetry"
)

//go:embed static/*
var staticFiles embed.FS

type Server struct {
	mgr *config.ConfigManager
}

func Start(mgr *config.ConfigManager, port int) error {
	s := &Server{mgr: mgr}
	telemetry.Default.ApplyConfig(mgr.Get())
	telemetry.WatchConfig(context.Background(), mgr, time.Second)
	telemetry.WatchSystem(context.Background(), 5*time.Second)

	mux := http.NewServeMux()

	mux.HandleFunc("/api/config", s.handleConfig)
	mux.HandleFunc("/api/events", s.handleEvents)
	mux.HandleFunc("/api/logs/events", s.handleLogEvents)
	mux.HandleFunc("/api/routes", s.handleRoutes)
	mux.HandleFunc("/api/routes/add", s.handleRouteAdd)
	mux.HandleFunc("/api/routes/update", s.handleRouteUpdate)
	mux.HandleFunc("/api/routes/delete", s.handleRouteDelete)
	mux.HandleFunc("/api/tcp-routes", s.handleTcpRoutes)
	mux.HandleFunc("/api/tcp-routes/add", s.handleTcpRouteAdd)
	mux.HandleFunc("/api/tcp-routes/update", s.handleTcpRouteUpdate)
	mux.HandleFunc("/api/tcp-routes/delete", s.handleTcpRouteDelete)
	mux.HandleFunc("/api/ssh-tunnels", s.handleSSHTunnels)
	mux.HandleFunc("/api/ssh-tunnels/add", s.handleSSHTunnelAdd)
	mux.HandleFunc("/api/ssh-tunnels/update", s.handleSSHTunnelUpdate)
	mux.HandleFunc("/api/ssh-tunnels/delete", s.handleSSHTunnelDelete)

	staticFS, err := fs.Sub(staticFiles, "static")
	if err != nil {
		return fmt.Errorf("static files: %w", err)
	}
	mux.Handle("/", http.FileServer(http.FS(staticFS)))

	addr := fmt.Sprintf(":%d", port)
	log.Printf("[admin] web GUI listening on http://localhost:%d", port)

	server := &http.Server{
		Addr:         addr,
		Handler:      mux,
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 0,
	}
	return server.ListenAndServe()
}

func (s *Server) handleConfig(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		cfg := s.mgr.Get()
		writeJSON(w, cfg)
		return
	}
	if r.Method == http.MethodPut {
		var cfg config.Config
		if err := json.NewDecoder(r.Body).Decode(&cfg); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		s.mgr.SetPort(cfg.Port)
		s.mgr.SetLogRequestParams(cfg.LogRequestParams)
		s.mgr.SetRoutes(cfg.Routes)
		s.mgr.SetTcpRoutes(cfg.TcpRoutes)
		s.mgr.SetSSHTunnels(cfg.SSHTunnels)
		_ = s.mgr.Save()
		telemetry.Default.ApplyConfig(s.mgr.Get())
		writeJSON(w, map[string]string{"status": "ok"})
		return
	}
	http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
}

func (s *Server) handleRoutes(w http.ResponseWriter, r *http.Request) {
	cfg := s.mgr.Get()
	writeJSON(w, cfg.Routes)
}

func (s *Server) handleRouteAdd(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var route config.Route
	if err := json.NewDecoder(r.Body).Decode(&route); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if err := s.mgr.AddRoute(route); err != nil {
		writeJSON(w, map[string]string{"error": err.Error()})
		return
	}
	_ = s.mgr.Save()
	telemetry.Default.ApplyConfig(s.mgr.Get())
	writeJSON(w, map[string]string{"status": "ok"})
}

func (s *Server) handleRouteUpdate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPut {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		OriginalPath string       `json:"original_path"`
		Route        config.Route `json:"route"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if err := s.mgr.UpdateRoute(req.OriginalPath, req.Route); err != nil {
		writeJSON(w, map[string]string{"error": err.Error()})
		return
	}
	_ = s.mgr.Save()
	telemetry.Default.ApplyConfig(s.mgr.Get())
	writeJSON(w, map[string]string{"status": "ok"})
}

func (s *Server) handleRouteDelete(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		Path string `json:"path"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if err := s.mgr.DeleteRoute(req.Path); err != nil {
		writeJSON(w, map[string]string{"error": err.Error()})
		return
	}
	_ = s.mgr.Save()
	telemetry.Default.ApplyConfig(s.mgr.Get())
	writeJSON(w, map[string]string{"status": "ok"})
}

func (s *Server) handleTcpRoutes(w http.ResponseWriter, r *http.Request) {
	cfg := s.mgr.Get()
	writeJSON(w, cfg.TcpRoutes)
}

func (s *Server) handleTcpRouteAdd(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var route config.TcpRoute
	if err := json.NewDecoder(r.Body).Decode(&route); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if err := s.mgr.AddTcpRoute(route); err != nil {
		writeJSON(w, map[string]string{"error": err.Error()})
		return
	}
	_ = s.mgr.Save()
	telemetry.Default.ApplyConfig(s.mgr.Get())
	writeJSON(w, map[string]string{"status": "ok"})
}

func (s *Server) handleTcpRouteUpdate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPut {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		OriginalListen string          `json:"original_listen"`
		Route          config.TcpRoute `json:"route"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if err := s.mgr.UpdateTcpRoute(req.OriginalListen, req.Route); err != nil {
		writeJSON(w, map[string]string{"error": err.Error()})
		return
	}
	_ = s.mgr.Save()
	telemetry.Default.ApplyConfig(s.mgr.Get())
	writeJSON(w, map[string]string{"status": "ok"})
}

func (s *Server) handleTcpRouteDelete(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		Listen string `json:"listen"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if err := s.mgr.DeleteTcpRoute(req.Listen); err != nil {
		writeJSON(w, map[string]string{"error": err.Error()})
		return
	}
	_ = s.mgr.Save()
	telemetry.Default.ApplyConfig(s.mgr.Get())
	writeJSON(w, map[string]string{"status": "ok"})
}

func (s *Server) handleSSHTunnels(w http.ResponseWriter, r *http.Request) {
	cfg := s.mgr.Get()
	writeJSON(w, cfg.SSHTunnels)
}

func (s *Server) handleSSHTunnelAdd(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var tunnel config.SSHTunnel
	if err := json.NewDecoder(r.Body).Decode(&tunnel); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if err := s.mgr.AddSSHTunnel(tunnel); err != nil {
		writeJSON(w, map[string]string{"error": err.Error()})
		return
	}
	_ = s.mgr.Save()
	telemetry.Default.ApplyConfig(s.mgr.Get())
	writeJSON(w, map[string]string{"status": "ok"})
}

func (s *Server) handleSSHTunnelUpdate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPut {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		OriginalName string           `json:"original_name"`
		Tunnel       config.SSHTunnel `json:"tunnel"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if err := s.mgr.UpdateSSHTunnel(req.OriginalName, req.Tunnel); err != nil {
		writeJSON(w, map[string]string{"error": err.Error()})
		return
	}
	_ = s.mgr.Save()
	telemetry.Default.ApplyConfig(s.mgr.Get())
	writeJSON(w, map[string]string{"status": "ok"})
}

func (s *Server) handleSSHTunnelDelete(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		Name string `json:"name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if err := s.mgr.DeleteSSHTunnel(req.Name); err != nil {
		writeJSON(w, map[string]string{"error": err.Error()})
		return
	}
	_ = s.mgr.Save()
	telemetry.Default.ApplyConfig(s.mgr.Get())
	writeJSON(w, map[string]string{"status": "ok"})
}

func (s *Server) handleEvents(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "stream unsupported", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")

	writeSnapshot := func() error {
		data, err := json.Marshal(telemetry.Default.Snapshot())
		if err != nil {
			return err
		}
		if _, err := fmt.Fprintf(w, "event: metrics\ndata: %s\n\n", data); err != nil {
			return err
		}
		flusher.Flush()
		return nil
	}

	fmt.Fprint(w, ": connected\n\n")
	flusher.Flush()
	if err := writeSnapshot(); err != nil {
		return
	}

	notify := r.Context().Done()
	pushTicker := time.NewTicker(5 * time.Second)
	defer pushTicker.Stop()

	for {
		select {
		case <-notify:
			return
		case <-pushTicker.C:
			if err := writeSnapshot(); err != nil {
				return
			}
		}
	}
}

func (s *Server) handleLogEvents(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "stream unsupported", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")

	writeEntry := func(entry logstream.Entry) error {
		data, err := json.Marshal(entry)
		if err != nil {
			return err
		}
		if _, err := fmt.Fprintf(w, "event: log\ndata: %s\n\n", data); err != nil {
			return err
		}
		flusher.Flush()
		return nil
	}

	fmt.Fprint(w, ": connected\n\n")
	flusher.Flush()

	for _, entry := range logstream.Default.Recent() {
		if err := writeEntry(entry); err != nil {
			return
		}
	}

	updates, cancel := logstream.Default.Subscribe()
	defer cancel()

	heartbeat := time.NewTicker(5 * time.Second)
	defer heartbeat.Stop()

	for {
		select {
		case <-r.Context().Done():
			return
		case <-heartbeat.C:
			if _, err := fmt.Fprint(w, ": ping\n\n"); err != nil {
				return
			}
			flusher.Flush()
		case entry, ok := <-updates:
			if !ok {
				return
			}
			if err := writeEntry(entry); err != nil {
				return
			}
		}
	}
}

func writeJSON(w http.ResponseWriter, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v)
}
