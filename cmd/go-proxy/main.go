package main

import (
	"flag"
	"fmt"
	"log"
	"strings"

	"go-proxy/internal/admin"
	"go-proxy/internal/config"
	"go-proxy/internal/proxy"
)

func main() {
	port := flag.Int("port", 0, "proxy listen port (default from config or 8080)")
	configFile := flag.String("config", "config.yaml", "config file path")
	adminPort := flag.Int("admin-port", 0, "admin web GUI port (default: proxy port + 1)")
	routes := flag.String("route", "", "route in format: path=target[:strip], e.g. /aaa=http://192.168.1.10:true (can be repeated)")
	flag.Parse()

	mgr := config.NewManager(*configFile)
	if err := mgr.Load(); err != nil {
		log.Fatalf("load config: %v", err)
	}

	// Apply CLI overrides
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

			stripPrefix := true // default strip
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

			mgr.AddRoute(config.Route{
				Path:        path,
				Target:      target,
				StripPrefix: stripPrefix,
			})
		}
	}

	cfg := mgr.Get()
	if cfg.Port == 0 {
		mgr.SetPort(8080)
	}

	// Determine admin port
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
		fmt.Printf("    %s -> %s (strip_prefix=%v)\n", r.Path, r.Target, r.StripPrefix)
	}
	fmt.Println()

	// Start admin GUI in background
	go func() {
		if err := admin.Start(mgr, ap); err != nil {
			log.Fatalf("admin server: %v", err)
		}
	}()

	// Watch config file for hot-reload
	if err := mgr.Watch(); err != nil {
		log.Printf("warn: config watch disabled: %v", err)
	}
	defer mgr.Close()

	// Start proxy (blocking)
	if err := proxy.Start(mgr); err != nil {
		log.Fatalf("proxy server: %v", err)
	}
}
