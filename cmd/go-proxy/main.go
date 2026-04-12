package main

import (
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"strings"

	"go-proxy/internal/admin"
	"go-proxy/internal/config"
	"go-proxy/internal/logstream"
	"go-proxy/internal/proxy"
)

func main() {
	log.SetOutput(io.MultiWriter(os.Stderr, logstream.NewWriter(logstream.Default)))

	port := flag.Int("port", 0, "proxy listen port (default from config or 8080)")
	configFile := flag.String("config", "config.yaml", "config file path")
	adminPort := flag.Int("admin-port", 0, "admin web GUI port (default: proxy port + 1)")
	routes := flag.String("route", "", "route in format: path=target[:strip], e.g. /aaa=http://192.168.1.10:true")
	flag.Parse()

	mgr := config.NewManager(*configFile)
	if err := mgr.Load(); err != nil {
		log.Fatalf("load config: %v", err)
	}

	if *port > 0 {
		mgr.SetPort(*port)
	}

	// Parse CLI route overrides
	if *routes != "" {
		for _, r := range strings.Split(*routes, ",") {
			parts := strings.SplitN(r, "=", 2)
			if len(parts) != 2 {
				log.Fatalf("invalid route format: %q (expected path=target)", r)
			}
			path := parts[0]
			targetAndStrip := parts[1]

			stripPrefix := true
			target := targetAndStrip
			if idx := strings.LastIndex(targetAndStrip, ":"); idx > 0 {
				maybeBool := targetAndStrip[idx+1:]
				if maybeBool == "true" || maybeBool == "false" {
					target = targetAndStrip[:idx]
					stripPrefix = maybeBool == "true"
				}
			}

			if !strings.HasPrefix(target, "http://") && !strings.HasPrefix(target, "https://") {
				target = "http://" + target
			}

			_ = mgr.AddRoute(config.Route{
				Path:        path,
				Target:      target,
				StripPrefix: stripPrefix,
				Upstreams:   []config.Upstream{{Target: target, Weight: 1}},
			})
		}
	}

	cfg := mgr.Get()
	if cfg.Port == 0 {
		mgr.SetPort(8080)
	}

	ap := *adminPort
	if ap == 0 {
		ap = mgr.Get().Port + 1
	}

	fmt.Println("╔══════════════════════════════════════╗")
	fmt.Println("║         Go Proxy Server              ║")
	fmt.Println("╚══════════════════════════════════════╝")
	cfg = mgr.Get()
	fmt.Printf("  Proxy port : %d\n", cfg.Port)
	fmt.Printf("  Admin GUI  : http://localhost:%d\n", ap)
	fmt.Printf("  Config     : %s\n", *configFile)
	fmt.Println()
	fmt.Println("  Routes:")
	if len(cfg.Routes) == 0 {
		fmt.Println("    (none)")
	}
	for _, r := range cfg.Routes {
		if r.IsMock() {
			mock := r.Mock
			method := "ANY"
			statusCode := 200
			responseType := "application/json"
			paramCount := 0
			headerCount := 0
			if mock != nil {
				if mock.Method != "" {
					method = mock.Method
				}
				if mock.StatusCode > 0 {
					statusCode = mock.StatusCode
				}
				if mock.ResponseType != "" {
					responseType = mock.ResponseType
				}
				paramCount = len(mock.Params)
				headerCount = len(mock.Headers)
			}
			fmt.Printf("    %s -> mock %s priority=%d status=%d type=%s params=%d headers=%d\n", r.Path, method, r.Priority, statusCode, responseType, paramCount, headerCount)
			continue
		}

		upstreams := r.ResolveUpstreams()
		var parts []string
		for _, u := range upstreams {
			s := u.Target
			if u.Weight > 1 {
				s += fmt.Sprintf(" w=%d", u.Weight)
			}
			if u.Backup {
				s += " (backup)"
			}
			parts = append(parts, s)
		}
		fmt.Printf("    %s -> %s (priority=%d strip=%v)\n", r.Path, strings.Join(parts, ", "), r.Priority, r.StripPrefix)
	}
	fmt.Println()

	fmt.Println("  TCP Routes:")
	if len(cfg.TcpRoutes) == 0 {
		fmt.Println("    (none)")
	}
	for _, r := range cfg.TcpRoutes {
		var parts []string
		for _, u := range r.Upstreams {
			s := u.Target
			if u.Weight > 1 {
				s += fmt.Sprintf(" w=%d", u.Weight)
			}
			if u.Backup {
				s += " (backup)"
			}
			if !u.Enabled {
				s += " (disabled)"
			}
			parts = append(parts, s)
		}
		enabled := ""
		if !r.Enabled {
			enabled = " [disabled]"
		}
		fmt.Printf("    %s -> %s%s\n", r.Listen, strings.Join(parts, ", "), enabled)
	}
	fmt.Println()

	go func() {
		if err := admin.Start(mgr, ap); err != nil {
			log.Fatalf("admin server: %v", err)
		}
	}()

	if err := mgr.Watch(); err != nil {
		log.Printf("warn: config watch disabled: %v", err)
	}
	defer mgr.Close()

	if err := proxy.Start(mgr); err != nil {
		log.Fatalf("proxy server: %v", err)
	}
}
