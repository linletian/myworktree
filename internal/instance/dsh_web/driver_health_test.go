package dsh_web

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"testing"

	"myworktree/internal/framework"
)

// TestProbeHealth pins the health-probe contract: a reachable server
// answering <400 is UP, a reachable server answering >=400 (other than
// the browser-auth 401, which dsh >=0.1.2 returns for the gated SPA) is
// DOWN, and an unreachable server is DOWN.
func TestProbeHealth(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer up.Close()
	badGateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer badGateway.Close()
	// dsh >=0.1.2 gates even GET / behind browser auth: 401 means the
	// server is UP, not failed (otherwise healthFailThreshold flips new
	// instances to MarkFailed ~15s after start).
	unauthorized := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer unauthorized.Close()

	upHost, upPort := splitAddr(t, up)
	downHost, downPort := splitAddr(t, badGateway)
	authHost, authPort := splitAddr(t, unauthorized)

	// Guaranteed-refused address: bind then close, so nothing listens.
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("bind refused port: %v", err)
	}
	refusedAddr := l.Addr().String()
	_ = l.Close()
	refusedHost, refusedPort, err := net.SplitHostPort(refusedAddr)
	if err != nil {
		t.Fatalf("split refused addr: %v", err)
	}

	cases := []struct {
		name string
		host string
		port string
		want bool
	}{
		{"200 is up", upHost, upPort, true},
		{"401 browser-auth gate is up", authHost, authPort, true},
		{"502 is down", downHost, downPort, false},
		{"connection refused is down", refusedHost, refusedPort, false},
	}
	for _, c := range cases {
		if got := probeHealth(context.Background(), c.host, c.port); got != c.want {
			t.Errorf("probeHealth(%s:%s) [%s] = %v, want %v", c.host, c.port, c.name, got, c.want)
		}
	}
}

func splitAddr(t *testing.T, srv *httptest.Server) (string, string) {
	t.Helper()
	h, p, err := net.SplitHostPort(srv.Listener.Addr().String())
	if err != nil {
		t.Fatalf("split %q: %v", srv.Listener.Addr(), err)
	}
	return h, p
}

// TestStatusHealthProbeFailureIsUnhealthyNotFailed pins issue #136: a
// health-probe failure is degraded, not terminal. StatusFailed makes
// runLifecycle drop the only handle to a still-live process, after which
// Manager.Stop finds nothing and the proxy listener leaks.
func TestStatusHealthProbeFailureIsUnhealthyNotFailed(t *testing.T) {
	h := &Handle{host: "127.0.0.1", port: "1"}
	// Status checks cmd/Process only to distinguish "spawned" from
	// "never started"; it does not signal this process.
	h.cmd = &exec.Cmd{Process: &os.Process{Pid: os.Getpid()}}
	d := &Driver{}
	handle := framework.NewHandle(kindName, h)

	h.failCount.Store(int32(healthFailThreshold - 1))
	if got, _ := d.Status(handle); got != framework.StatusRunning {
		t.Fatalf("status below threshold = %v, want running", got)
	}

	h.failCount.Store(int32(healthFailThreshold))
	got, reason := d.Status(handle)
	if got != framework.StatusUnhealthy {
		t.Fatalf("status at threshold = %v, want unhealthy (not failed)", got)
	}
	if reason == "" {
		t.Fatal("unhealthy status lost its reason")
	}
}
