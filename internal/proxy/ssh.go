package proxy

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"sync"
	"time"

	"go-proxy/internal/config"
	"golang.org/x/crypto/ssh"
)

type SSHManager struct {
	mgr     *config.ConfigManager
	mu      sync.Mutex
	runners map[string]*sshTunnelRunner
	done    chan struct{}
}

type sshTunnelRunner struct {
	tunnel config.SSHTunnel
	stop   chan struct{}
	done   chan struct{}
}

func NewSSHManager(mgr *config.ConfigManager) *SSHManager {
	return &SSHManager{
		mgr:     mgr,
		runners: make(map[string]*sshTunnelRunner),
		done:    make(chan struct{}),
	}
}

func (sm *SSHManager) Start() error {
	sm.reconcile(sm.mgr.Get().SSHTunnels)
	return nil
}

func (sm *SSHManager) Reload() {
	sm.reconcile(sm.mgr.Get().SSHTunnels)
}

func (sm *SSHManager) Close() {
	select {
	case <-sm.done:
	default:
		close(sm.done)
	}
	sm.mu.Lock()
	runners := make([]*sshTunnelRunner, 0, len(sm.runners))
	for _, runner := range sm.runners {
		runners = append(runners, runner)
	}
	sm.runners = map[string]*sshTunnelRunner{}
	sm.mu.Unlock()

	for _, runner := range runners {
		runner.stopAndWait()
	}
}

func (sm *SSHManager) Watch(interval time.Duration) {
	if interval <= 0 {
		interval = time.Second
	}
	lastSnapshot := sm.snapshot()
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-sm.done:
			return
		case <-ticker.C:
			current := sm.snapshot()
			if current == lastSnapshot {
				continue
			}
			lastSnapshot = current
			sm.Reload()
		}
	}
}

func (sm *SSHManager) reconcile(tunnels []config.SSHTunnel) {
	desired := make(map[string]config.SSHTunnel, len(tunnels))
	for _, tunnel := range tunnels {
		if tunnel.IsEnabled() {
			desired[tunnel.Name] = tunnel
		}
	}

	sm.mu.Lock()
	var toStop []*sshTunnelRunner
	for name, runner := range sm.runners {
		tunnel, ok := desired[name]
		if !ok || !sameSSHTunnelConfig(runner.tunnel, tunnel) {
			toStop = append(toStop, runner)
			delete(sm.runners, name)
		}
	}

	for name, tunnel := range desired {
		if _, ok := sm.runners[name]; ok {
			continue
		}
		runner := &sshTunnelRunner{
			tunnel: tunnel,
			stop:   make(chan struct{}),
			done:   make(chan struct{}),
		}
		sm.runners[name] = runner
		go runner.run()
	}
	active := len(sm.runners)
	sm.mu.Unlock()

	for _, runner := range toStop {
		runner.stopAndWait()
	}

	log.Printf("[ssh] active tunnels: %d", active)
}

func (sm *SSHManager) snapshot() string {
	data, err := json.Marshal(sm.mgr.Get().SSHTunnels)
	if err != nil {
		return ""
	}
	return string(data)
}

func (r *sshTunnelRunner) stopAndWait() {
	select {
	case <-r.stop:
	default:
		close(r.stop)
	}
	<-r.done
}

func (r *sshTunnelRunner) run() {
	defer close(r.done)
	for {
		select {
		case <-r.stop:
			return
		default:
		}

		if err := r.runOnce(); err != nil {
			log.Printf("[ssh] tunnel %s error: %v", r.tunnel.Name, err)
		}

		select {
		case <-r.stop:
			return
		case <-time.After(3 * time.Second):
		}
	}
}

func (r *sshTunnelRunner) runOnce() error {
	client, err := r.dialClient()
	if err != nil {
		return err
	}
	defer client.Close()

	switch r.tunnel.Direction {
	case config.SSHModeRemote:
		return r.runRemoteForward(client)
	default:
		return r.runLocalForward(client)
	}
}

func (r *sshTunnelRunner) dialClient() (*ssh.Client, error) {
	authMethod, err := buildSSHAuthMethod(r.tunnel)
	if err != nil {
		return nil, err
	}

	clientConfig := &ssh.ClientConfig{
		User:            r.tunnel.Username,
		Auth:            []ssh.AuthMethod{authMethod},
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
		Timeout:         10 * time.Second,
	}

	addr := fmt.Sprintf("%s:%d", r.tunnel.Host, r.tunnel.Port)
	log.Printf("[ssh] connecting %s (%s)", r.tunnel.Name, addr)
	client, err := ssh.Dial("tcp", addr, clientConfig)
	if err != nil {
		return nil, fmt.Errorf("dial ssh %s: %w", addr, err)
	}
	return client, nil
}

func (r *sshTunnelRunner) runLocalForward(client *ssh.Client) error {
	ln, err := net.Listen("tcp", r.tunnel.LocalAddress)
	if err != nil {
		return fmt.Errorf("listen local %s: %w", r.tunnel.LocalAddress, err)
	}
	defer ln.Close()

	go func() {
		<-r.stop
		_ = ln.Close()
	}()

	log.Printf("[ssh] local forward %s: %s -> %s", r.tunnel.Name, r.tunnel.LocalAddress, r.tunnel.RemoteAddress)
	for {
		conn, err := ln.Accept()
		if err != nil {
			select {
			case <-r.stop:
				return nil
			default:
				if isClosedListenerError(err) {
					return nil
				}
				return fmt.Errorf("accept local %s: %w", r.tunnel.LocalAddress, err)
			}
		}
		go r.handleLocalForwardConn(client, conn)
	}
}

func (r *sshTunnelRunner) runRemoteForward(client *ssh.Client) error {
	ln, err := client.Listen("tcp", r.tunnel.RemoteAddress)
	if err != nil {
		return fmt.Errorf("listen remote %s: %w", r.tunnel.RemoteAddress, err)
	}
	defer ln.Close()

	go func() {
		<-r.stop
		_ = ln.Close()
	}()

	log.Printf("[ssh] remote forward %s: %s -> %s", r.tunnel.Name, r.tunnel.RemoteAddress, r.tunnel.LocalAddress)
	for {
		conn, err := ln.Accept()
		if err != nil {
			select {
			case <-r.stop:
				return nil
			default:
				if isClosedListenerError(err) {
					return nil
				}
				return fmt.Errorf("accept remote %s: %w", r.tunnel.RemoteAddress, err)
			}
		}
		go r.handleRemoteForwardConn(conn)
	}
}

func (r *sshTunnelRunner) handleLocalForwardConn(client *ssh.Client, inbound net.Conn) {
	defer inbound.Close()
	outbound, err := client.Dial("tcp", r.tunnel.RemoteAddress)
	if err != nil {
		log.Printf("[ssh] local forward %s dial %s: %v", r.tunnel.Name, r.tunnel.RemoteAddress, err)
		return
	}
	defer outbound.Close()
	proxyConns(inbound, outbound)
}

func (r *sshTunnelRunner) handleRemoteForwardConn(inbound net.Conn) {
	defer inbound.Close()
	outbound, err := net.DialTimeout("tcp", r.tunnel.LocalAddress, 10*time.Second)
	if err != nil {
		log.Printf("[ssh] remote forward %s dial %s: %v", r.tunnel.Name, r.tunnel.LocalAddress, err)
		return
	}
	defer outbound.Close()
	proxyConns(inbound, outbound)
}

func buildSSHAuthMethod(tunnel config.SSHTunnel) (ssh.AuthMethod, error) {
	if tunnel.AuthType == config.SSHAuthKey {
		keyData := tunnel.PrivateKey
		if keyData == "" && tunnel.PrivateKeyPath != "" {
			data, err := os.ReadFile(tunnel.PrivateKeyPath)
			if err != nil {
				return nil, fmt.Errorf("read private key: %w", err)
			}
			keyData = string(data)
		}
		if keyData == "" {
			return nil, fmt.Errorf("private key is required")
		}
		signer, err := ssh.ParsePrivateKey([]byte(keyData))
		if err != nil {
			return nil, fmt.Errorf("parse private key: %w", err)
		}
		return ssh.PublicKeys(signer), nil
	}
	return ssh.Password(tunnel.Password), nil
}

func proxyConns(left, right net.Conn) {
	var wg sync.WaitGroup
	wg.Add(2)

	go func() {
		defer wg.Done()
		_, _ = io.Copy(left, right)
		_ = left.SetDeadline(time.Now())
	}()
	go func() {
		defer wg.Done()
		_, _ = io.Copy(right, left)
		_ = right.SetDeadline(time.Now())
	}()
	wg.Wait()
}

func sameSSHTunnelConfig(a, b config.SSHTunnel) bool {
	return a == b
}
