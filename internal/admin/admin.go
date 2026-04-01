package admin

import (
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"time"

	"go-proxy/internal/config"
)

//go:embed static/*
var staticFiles embed.FS

type Server struct {
	mgr *config.ConfigManager
}

func Start(mgr *config.ConfigManager, port int) error {
	s := &Server{mgr: mgr}

	mux := http.NewServeMux()

	mux.HandleFunc("/api/config", s.handleConfig)
	mux.HandleFunc("/api/routes", s.handleRoutes)
	mux.HandleFunc("/api/routes/add", s.handleRouteAdd)
	mux.HandleFunc("/api/routes/update", s.handleRouteUpdate)
	mux.HandleFunc("/api/routes/delete", s.handleRouteDelete)

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
		WriteTimeout: 10 * time.Second,
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
		s.mgr.SetRoutes(cfg.Routes)
		_ = s.mgr.Save()
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
	writeJSON(w, map[string]string{"status": "ok"})
}

func writeJSON(w http.ResponseWriter, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v)
}
