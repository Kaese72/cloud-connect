// Package tunnel supervises a chisel client (used as a library, not a
// subprocess - see below) that carries the cloud-connect reverse tunnel. It
// is only ever started once enrollment has completed - see the README's
// "Enrollment" section - and its whole reason to exist as a supervisor
// (rather than the client running unconditionally as the container's own
// entrypoint, as it used to) is to make "not started yet" a real,
// observable state instead of "container never runs at all".
package tunnel

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	chclient "github.com/jpillora/chisel/client"

	"github.com/Kaese72/cloud-connect/client/internal/logging"
)

// reverseTunnelSpec instructs the chisel server to bind port 9090 and
// tunnel incoming traffic to this pod's own nginx at port 9091 - see
// client/start.sh's original comment, preserved here since the shape of
// the tunnel itself hasn't changed, only what decides when to start it.
//
// This is a *reverse* remote ("R:"): the bound port 9090 lives on the
// chisel *server*, forwarding to 127.0.0.1:9091 on this client. There is
// no local socket on this side that reflects the tunnel's up/down state -
// that's why Connected() below is driven from the client's own connect/
// disconnect events rather than by dialing a local port.
const reverseTunnelSpec = "R:9090:127.0.0.1:9091"

const (
	minRestartWait = time.Second
	maxRestartWait = 30 * time.Second
)

// Supervisor owns at most one running chisel client at a time.
type Supervisor struct {
	mu        sync.Mutex
	cancel    context.CancelFunc
	client    *chclient.Client
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
}

// Stop ends the supervised chisel client, if any, and clears connected
// state - used by enrollment/reset.
func (s *Supervisor) Stop() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cancel != nil {
		s.cancel()
	}
	if s.client != nil {
		s.client.Close()
	}
	s.cancel = nil
	s.client = nil
	s.running = false
	s.connected.Store(false)
}

// Connected reports whether the reverse tunnel is currently up, determined
// by the chisel client's own "Connected"/"Disconnected" log events (see
// watchLogs) - not by dialing a local port, since a reverse tunnel binds
// its port on the server, not here.
func (s *Supervisor) Connected() bool {
	return s.connected.Load()
}

// newClient builds a chisel client for serverURL/secret. The chisel client
// package exposes no connection-state callback, only a logger that writes
// straight to the process's real os.Stderr with no way to redirect it after
// construction - so os.Stderr is swapped for a private pipe for the
// instant of construction only, which is enough for the client to capture
// a reference to the pipe's write end for the rest of its life. Every line
// it logs is still forwarded to the real stderr (via watchLogs) so
// connection attempts remain visible in pod logs; the same lines are also
// parsed there to drive s.connected.
func (s *Supervisor) newClient(secret, serverURL string) (*chclient.Client, error) {
	r, w, err := os.Pipe()
	if err != nil {
		return nil, err
	}
	realStderr := os.Stderr
	os.Stderr = w
	client, err := chclient.NewClient(&chclient.Config{
		Auth:    secret,
		Server:  serverURL,
		Remotes: []string{reverseTunnelSpec},
		// Mirrors the chisel CLI's own defaults (see its main.go), since
		// this replaces what used to be an exec'd `chisel client` process.
		KeepAlive:        25 * time.Second,
		MaxRetryCount:    -1,
		MaxRetryInterval: 0,
	})
	os.Stderr = realStderr
	if err != nil {
		w.Close()
		r.Close()
		return nil, err
	}
	go s.watchLogs(r, realStderr)
	return client, nil
}

// watchLogs forwards the chisel client's log lines to realStderr and
// updates s.connected from the two lines chisel's client package itself
// emits around a live tunnel: "client: Connected (Latency ...)" when the
// SSH handshake over the websocket completes, and "client: Disconnected"
// when that connection drops.
func (s *Supervisor) watchLogs(r *os.File, realStderr *os.File) {
	defer r.Close()
	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		line := scanner.Text()
		fmt.Fprintln(realStderr, line)
		switch {
		case strings.Contains(line, "client: Connected ("):
			s.connected.Store(true)
		case strings.Contains(line, "client: Disconnected"):
			s.connected.Store(false)
		}
	}
}

// runLoop rebuilds and runs the chisel client until ctx is cancelled.
// chisel's own connectionLoop already retries the connection forever with
// exponential backoff (MaxRetryCount: -1 above) - the retry/backoff here
// only covers newClient itself failing (a construction-time error, e.g. an
// unparseable server URL), which chisel's internal loop never sees since it
// starts after construction succeeds.
func (s *Supervisor) runLoop(ctx context.Context, secret string, serverURL string) {
	wait := minRestartWait
	for ctx.Err() == nil {
		client, err := s.newClient(secret, serverURL)
		if err != nil {
			logging.ErrorErr(err, ctx, map[string]interface{}{"component": "tunnel"})
		} else {
			s.mu.Lock()
			if ctx.Err() != nil {
				// Stop() ran while newClient was still building this one -
				// close it unrun rather than leaving it supervised by no one.
				s.mu.Unlock()
				client.Close()
				return
			}
			s.client = client
			s.mu.Unlock()

			err = client.Run() // blocks; retries internally until ctx is cancelled
			s.connected.Store(false)
			if err != nil && ctx.Err() == nil {
				logging.ErrorErr(err, ctx, map[string]interface{}{"component": "tunnel"})
			}
		}
		if ctx.Err() != nil {
			return
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
