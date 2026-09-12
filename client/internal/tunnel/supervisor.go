// Package tunnel supervises the chisel client subprocess that carries the
// cloud-connect reverse tunnel. It is only ever started once enrollment has
// completed - see the README's "Enrollment" section - and its whole reason
// to exist as a supervisor (rather than exec.Cmd being the container's own
// entrypoint, as it used to be) is to make "not started yet" a real,
// observable state instead of "container never runs at all".
package tunnel

import (
	"context"
	"net"
	"os"
	"os/exec"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Kaese72/cloud-connect/client/internal/logging"
)

// reverseTunnelSpec instructs the chisel server to bind port 9090 and
// tunnel incoming traffic to this pod's own nginx at port 9091 - see
// client/start.sh's original comment, preserved here since the shape of
// the tunnel itself hasn't changed, only what decides when to start it.
const reverseTunnelSpec = "R:9090:127.0.0.1:9091"

// tunnelBoundPort is the local port the reverse tunnel binds on this side
// once connected - polled to determine live connection state without
// depending on chisel's log output.
const tunnelBoundPort = "127.0.0.1:9090"

const (
	pollInterval   = 2 * time.Second
	minRestartWait = time.Second
	maxRestartWait = 30 * time.Second
)

// Supervisor owns at most one running chisel client process at a time.
type Supervisor struct {
	mu        sync.Mutex
	cancel    context.CancelFunc
	running   bool
	connected atomic.Bool
}

func NewSupervisor() *Supervisor {
	return &Supervisor{}
}

// Start begins supervising a chisel client connecting to serverURL with the
// given auth secret, restarting it with backoff if it exits. Calling Start
// while already running is a no-op - a boot-time resume and a fresh
// enrollment/complete call can both call it safely.
func (s *Supervisor) Start(secret string, serverURL string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.running {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	s.cancel = cancel
	s.running = true
	go s.runLoop(ctx, secret, serverURL)
	go s.pollConnection(ctx)
}

// Stop ends the supervised chisel process, if any, and clears connected
// state - used by enrollment/reset.
func (s *Supervisor) Stop() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cancel != nil {
		s.cancel()
	}
	s.cancel = nil
	s.running = false
	s.connected.Store(false)
}

// Connected reports whether the reverse tunnel is currently up, determined
// by whether tunnelBoundPort is accepting connections - not by parsing
// chisel's own log output, which is not a stable contract to depend on.
func (s *Supervisor) Connected() bool {
	return s.connected.Load()
}

func (s *Supervisor) runLoop(ctx context.Context, secret string, serverURL string) {
	wait := minRestartWait
	for ctx.Err() == nil {
		cmd := exec.CommandContext(ctx, "chisel", "client", "--auth", secret, serverURL, reverseTunnelSpec)
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		err := cmd.Run()
		s.connected.Store(false)
		if ctx.Err() != nil {
			return
		}
		if err != nil {
			logging.ErrorErr(err, ctx, map[string]interface{}{"component": "tunnel"})
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		}
		if wait < maxRestartWait {
			wait *= 2
			if wait > maxRestartWait {
				wait = maxRestartWait
			}
		}
	}
}

func (s *Supervisor) pollConnection(ctx context.Context) {
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			conn, err := net.DialTimeout("tcp", tunnelBoundPort, time.Second)
			if err != nil {
				s.connected.Store(false)
				continue
			}
			conn.Close()
			s.connected.Store(true)
		}
	}
}
