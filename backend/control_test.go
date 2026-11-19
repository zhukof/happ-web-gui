package main

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const fakeHapp = `#!/bin/sh
[ "$1" = "--home" ] || exit 2
data_dir=$2
shift 2
case "$1" in
	connect)
		trap 'exit 0' INT
		printf 'partial message' >&2
		printf 'ready\n'
		while :; do sleep 0.05; done
		;;
	list)
		printf 'INDEX   PROTOCOL   ADDRESS   TAG\n1\tsocks\tlocalhost\tMain Server\n'
		;;
	sub)
		case "$2" in
			list) printf 'ACTIVE    NAME    TITLE    SERVERS\n* demo    Test plan    1\n' ;;
			add|update) exit 0 ;;
			use)
				touch "$data_dir/use-started"
				while [ ! -f "$data_dir/use-done" ]; do sleep 0.01; done
				;;
			*) exit 2 ;;
		esac
		;;
	*) exit 2 ;;
esac
`

func testManager(t *testing.T) *Manager {
	t.Helper()
	dir := t.TempDir()
	binary := filepath.Join(dir, "happ")
	if err := os.WriteFile(binary, []byte(fakeHapp), 0700); err != nil {
		t.Fatal(err)
	}
	manager := &Manager{
		cli:        happCLI{binary: binary, dataDir: dir},
		configPath: filepath.Join(dir, "control.json"),
	}
	t.Cleanup(func() {
		if done := manager.beginShutdown(); done != nil {
			select {
			case <-done:
			case <-time.After(7 * time.Second):
				t.Error("HAPP did not stop during cleanup")
			}
		}
	})
	return manager
}

func TestAPI(t *testing.T) {
	manager := testManager(t)
	web, err := webHandler()
	if err != nil {
		t.Fatal(err)
	}
	handler := newAPI(manager, web)
	tests := []struct {
		name     string
		method   string
		path     string
		body     string
		wantCode int
		wantBody string
	}{
		{"status", http.MethodGet, "/api/status", "", 200, `"logs":[]`},
		{"server list", http.MethodGet, "/api/servers", "", 200, `"tag":"Main Server"`},
		{"subscription list", http.MethodGet, "/api/subscriptions", "", 200, `"active":true`},
		{"valid subscription", http.MethodPost, "/api/subscriptions", `{"url":"https://example.test/sub","name":"demo"}`, 200, `"ok":true`},
		{"invalid subscription", http.MethodPost, "/api/subscriptions", `{"url":"https:///sub","name":"demo"}`, 400, "HTTPS subscription URL required"},
		{"update subscription", http.MethodPost, "/api/subscriptions/update", "", 200, `"ok":true`},
		{"invalid JSON", http.MethodPost, "/api/connect", `{"server":`, 400, "unexpected EOF"},
		{"multiple values", http.MethodPost, "/api/connect", `{} {}`, 400, "single JSON value"},
		{"oversize body", http.MethodPost, "/api/connect", `{}` + strings.Repeat(" ", maxRequestBytes), 413, "request body too large"},
		{"connect", http.MethodPost, "/api/connect", `{}`, 200, `"ok":true`},
		{"duplicate connect", http.MethodPost, "/api/connect", `{}`, 400, "already connected"},
		{"use while running", http.MethodPost, "/api/subscriptions/use", `{"name":"demo"}`, 400, "disconnect before changing subscription"},
		{"disconnect", http.MethodPost, "/api/disconnect", "", 200, `"ok":true`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, httptest.NewRequest(tt.method, tt.path, strings.NewReader(tt.body)))
			if response.Code != tt.wantCode || !strings.Contains(response.Body.String(), tt.wantBody) {
				t.Fatalf("unexpected response %d %s; want %d with %q", response.Code, response.Body, tt.wantCode, tt.wantBody)
			}
		})
	}
	if config, err := loadConfig(manager.configPath); err != nil || config.Server != "1" {
		t.Fatalf("load saved config = %+v, %v; want default server", config, err)
	}
}

func TestConnectionLifecycle(t *testing.T) {
	manager := testManager(t)
	if err := manager.Start(" 2 "); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool {
		return strings.Contains(strings.Join(manager.status().Logs, "\n"), "ready")
	})
	done := manager.beginShutdown()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("HAPP did not exit on interrupt")
	}
	current := manager.status()
	if current.Running || current.Stopping || current.Server != "2" {
		t.Fatalf("unexpected final state: %+v", current)
	}
	if !strings.Contains(strings.Join(current.Logs, "\n"), "error: partial message") {
		t.Fatalf("partial stderr line missing from logs: %v", current.Logs)
	}
	if err := manager.Start("1"); err == nil {
		t.Fatal("connection started during shutdown")
	}
}

func TestSubscriptionChangeExcludesConnect(t *testing.T) {
	manager := testManager(t)
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	finished := make(chan error, 1)
	go func() { finished <- manager.useSubscription(ctx, "demo") }()
	waitFor(t, func() bool {
		_, err := os.Stat(filepath.Join(manager.cli.dataDir, "use-started"))
		return err == nil
	})
	if err := manager.Start("1"); err == nil || !strings.Contains(err.Error(), "subscription change in progress") {
		t.Fatalf("unexpected connect result while changing subscription: %v", err)
	}
	if err := os.WriteFile(filepath.Join(manager.cli.dataDir, "use-done"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	if err := <-finished; err != nil {
		t.Fatal(err)
	}
	if err := manager.Start("1"); err != nil {
		t.Fatal(err)
	}
}

func TestTCPForwardingHalfCloseAndShutdown(t *testing.T) {
	listen := func() *net.TCPListener {
		t.Helper()
		listener, err := net.ListenTCP("tcp", &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1)})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = listener.Close() })
		if err := listener.SetDeadline(time.Now().Add(3 * time.Second)); err != nil {
			t.Fatal(err)
		}
		return listener
	}
	backend, proxy := listen(), listen()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	serveDone := make(chan error, 1)
	go func() { serveDone <- serveProxy(ctx, proxy, backend.Addr().String()) }()

	dial := func() *net.TCPConn {
		t.Helper()
		client, err := net.DialTCP("tcp", nil, proxy.Addr().(*net.TCPAddr))
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = client.Close() })
		if err := client.SetDeadline(time.Now().Add(3 * time.Second)); err != nil {
			t.Fatal(err)
		}
		return client
	}
	accept := func() *net.TCPConn {
		t.Helper()
		peer, err := backend.AcceptTCP()
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = peer.Close() })
		if err := peer.SetDeadline(time.Now().Add(3 * time.Second)); err != nil {
			t.Fatal(err)
		}
		return peer
	}

	client, peer := dial(), accept()
	if _, err := io.WriteString(client, "hello"); err != nil {
		t.Fatal(err)
	}
	if err := client.CloseWrite(); err != nil {
		t.Fatal(err)
	}
	if data, err := io.ReadAll(peer); err != nil || string(data) != "hello" {
		t.Fatalf("backend read = %q, %v", data, err)
	}
	if _, err := io.WriteString(peer, "world"); err != nil {
		t.Fatal(err)
	}
	if err := peer.CloseWrite(); err != nil {
		t.Fatal(err)
	}
	if data, err := io.ReadAll(client); err != nil || string(data) != "world" {
		t.Fatalf("client read = %q, %v", data, err)
	}

	idleClient, idlePeer := dial(), accept()
	cancel()
	select {
	case err := <-serveDone:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("proxy listener did not shut down")
	}
	for _, conn := range []*net.TCPConn{idleClient, idlePeer} {
		if _, err := io.ReadAll(conn); err != nil {
			t.Fatalf("idle connection did not close cleanly: %v", err)
		}
	}
}

func waitFor(t *testing.T, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for !condition() {
		if time.Now().After(deadline) {
			t.Fatal("condition did not become true")
		}
		time.Sleep(10 * time.Millisecond)
	}
}
