package server

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"testing"
	"time"

	"github.com/spioneracorei8/btcusd-trading-platform/server/config"
	"github.com/spioneracorei8/btcusd-trading-platform/server/constants"
)

// freePort asks the kernel for a port that is currently unused.
func freePort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserve a port: %v", err)
	}
	port := l.Addr().(*net.TCPAddr).Port
	if err := l.Close(); err != nil {
		t.Fatalf("release the port: %v", err)
	}
	return port
}

// TestListenDrainsInFlightRequestOnShutdown covers the promise behind the 10s
// grace period: a request already being served when SIGTERM arrives is
// finished, not cut off mid-response.
func TestListenDrainsInFlightRequestOnShutdown(t *testing.T) {
	port := freePort(t)
	srv := &Server{
		Config: &config.Config{App: config.App{HTTPPort: port}},
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	}

	// The handler is slower than the client's wait but far inside the grace
	// period, so only a real drain lets it finish.
	const handlerDelay = 300 * time.Millisecond
	handler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(handlerDelay)
		w.WriteHeader(http.StatusOK)
		if _, err := io.WriteString(w, "done"); err != nil {
			t.Errorf("write response: %v", err)
		}
	})

	ctx, cancel := context.WithCancel(context.Background())
	listenErr := make(chan error, 1)
	go func() { listenErr <- srv.listen(ctx, handler) }()

	addr := fmt.Sprintf("127.0.0.1:%d", port)
	url := "http://" + addr + "/slow"
	waitUntilServing(t, addr)

	type result struct {
		status int
		body   string
		err    error
	}
	got := make(chan result, 1)
	go func() {
		resp, err := http.Get(url) //nolint:noctx // the timeout under test is the server's
		if err != nil {
			got <- result{err: err}
			return
		}
		defer func() { _ = resp.Body.Close() }()
		body, err := io.ReadAll(resp.Body)
		got <- result{status: resp.StatusCode, body: string(body), err: err}
	}()

	// Cancel while the handler is still running, the way SIGTERM would.
	time.Sleep(handlerDelay / 3)
	cancel()

	select {
	case r := <-got:
		if r.err != nil {
			t.Fatalf("the in-flight request was cut off instead of drained: %v", r.err)
		}
		if r.status != http.StatusOK {
			t.Errorf("status = %d, want %d", r.status, http.StatusOK)
		}
		if r.body != "done" {
			t.Errorf("body = %q, want %q", r.body, "done")
		}
	case <-time.After(constants.ShutdownTimeout):
		t.Fatal("the in-flight request never completed")
	}

	select {
	case err := <-listenErr:
		if err != nil {
			t.Errorf("listen() returned error: %v", err)
		}
	case <-time.After(constants.ShutdownTimeout):
		t.Fatal("listen() did not return after shutdown")
	}
}

// waitUntilServing blocks until the server accepts connections on addr.
func waitUntilServing(t *testing.T, addr string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("tcp", addr, 50*time.Millisecond)
		if err == nil {
			if err := conn.Close(); err != nil {
				t.Fatalf("close probe connection: %v", err)
			}
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("server never started listening")
}
