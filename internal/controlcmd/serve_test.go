package controlcmd

import (
	"context"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/dinoderek/engine/internal/hosts"
	"github.com/dinoderek/engine/internal/remote"
)

func TestBackgroundRunsEveryLoopUntilShutdown(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	var started, stopped atomic.Int32
	loop := func(ctx context.Context) {
		started.Add(1)
		<-ctx.Done()
		stopped.Add(1)
	}
	done := make(chan struct{})
	go func() { background(loop, loop)(ctx); close(done) }()
	cancel()
	<-done
	if started.Load() != 2 || stopped.Load() != 2 {
		t.Fatalf("started %d, stopped %d", started.Load(), stopped.Load())
	}
}

func TestObserverReachesHostsThroughTheHostConfig(t *testing.T) {
	e, svc := environment(t)
	dir := hosts.ConfigDir(e.Home)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "host"), []byte("devbox\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	clients := hostClients(context.Background(), e, time.Minute)
	if _, ok := clients(hosts.Host{Name: "devbox"}).(remote.Local); !ok {
		t.Fatal("this host is not reached in-process")
	}
	if c, ok := clients(hosts.Host{Name: "mac", Address: "mac.example.test"}).(remote.SSH); !ok || c.Address != "mac.example.test" {
		t.Fatal("another host is not reached over SSH")
	}
	service, c, obs := wire(svc.Store, "http://control.test", e)
	if c.Observer != obs || c.Service.Observe == nil || service.Observe == nil || service.Reconcile == nil {
		t.Fatal("the session view is not wired to the observer")
	}
	if _, err := obs.Config(); err == nil {
		t.Fatal("a missing hosts.toml was accepted")
	}
	if got := obs.Observation("a1"); got.State != "unknown" {
		t.Fatalf("unobserved attempt: %+v", got)
	}
}
