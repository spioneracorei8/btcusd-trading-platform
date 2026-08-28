package handler

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/coder/websocket"

	_stream_us "github.com/spioneracorei8/btcusd-trading-platform/server/services/stream/usecase"
)

// serving starts the stream handler on a real listener, because the origin
// check happens during a handshake and a handshake needs a socket.
func serving(t *testing.T, allowed ...string) string {
	t.Helper()

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	hub := _stream_us.NewHub(logger)

	ctx, stop := context.WithCancel(context.Background())
	t.Cleanup(stop)
	go hub.Run(ctx)

	handler := NewStreamHandlerImpl(hub, logger, allowed)
	server := httptest.NewServer(http.HandlerFunc(handler.Stream))
	t.Cleanup(server.Close)

	return server.URL
}

// handshake opens a websocket with the given Origin header, and reports the
// HTTP status the server answered with.
//
// An accepted handshake is 101; a refused one is whatever Accept wrote, which
// is what these tests are about.
func handshake(t *testing.T, base, origin string) int {
	t.Helper()

	header := http.Header{}
	if origin != "" {
		header.Set("Origin", origin)
	}

	conn, resp, err := websocket.Dial(context.Background(),
		"ws"+strings.TrimPrefix(base, "http"),
		&websocket.DialOptions{HTTPHeader: header},
	)
	if conn != nil {
		defer conn.CloseNow()
	}
	if err == nil {
		return http.StatusSwitchingProtocols
	}
	if resp == nil {
		t.Fatalf("handshake failed without a response: %v", err)
	}
	return resp.StatusCode
}

/*
TestAForeignOriginCannotOpenTheStream.

# What this prevents

A websocket handshake is not bound by the same-origin policy the way fetch is.
Before phase 09b this handler ran with InsecureSkipVerify, which was a
deliberate choice with a reason attached: the only client was a native app on a
tailnet, and a native app sends no Origin at all.

Serving the app in a browser ends that. Any page the owner opens, in the same
browser, on the same tailnet, could otherwise open this endpoint and read every
signal with its entry, stop, target and full reason — with no prompt, no CORS
preflight to fail, and nothing in any log that reads as an intrusion.

ADR 0024 listed origin checking as required "the moment a browser can reach
it". This is the test that says it happened.
*/
func TestAForeignOriginCannotOpenTheStream(t *testing.T) {
	base := serving(t)

	got := handshake(t, base, "https://not-the-app.example.com")

	if got != http.StatusForbidden {
		t.Fatalf("a foreign origin got %d; want %d — the stream is readable by "+
			"any page in the owner's browser", got, http.StatusForbidden)
	}
}

/*
TestTheAppsOwnOriginIsAccepted.

Same-origin is the whole deployment: the app is served by this process so that
the page and the API share a host, and the check then holds with nothing
configured. If this fails, the app cannot open its own stream and the chart
stops updating with no error a person would connect to the cause.
*/
func TestTheAppsOwnOriginIsAccepted(t *testing.T) {
	base := serving(t)

	got := handshake(t, base, base)

	if got != http.StatusSwitchingProtocols {
		t.Fatalf("the app's own origin got %d; want %d", got, http.StatusSwitchingProtocols)
	}
}

/*
TestAClientWithNoOriginIsAccepted.

A native client, curl, or the reconnect path of a non-browser consumer sends no
Origin header. Refusing those would break every non-browser client to defend
against a header no page can omit: a browser always sends Origin on a
cross-origin websocket, so its absence is not something an attacker can arrange
from a page.
*/
func TestAClientWithNoOriginIsAccepted(t *testing.T) {
	base := serving(t)

	got := handshake(t, base, "")

	if got != http.StatusSwitchingProtocols {
		t.Fatalf("a client with no Origin got %d; want %d", got, http.StatusSwitchingProtocols)
	}
}

/*
TestAConfiguredOriginIsAccepted.

STREAM_ALLOWED_ORIGINS exists for development, where the app is served by Metro
on one port and the API answers on another. It is empty in a real deployment,
and a value here is a deliberate widening rather than a default.
*/
func TestAConfiguredOriginIsAccepted(t *testing.T) {
	base := serving(t)

	parsed, err := url.Parse(base)
	if err != nil {
		t.Fatalf("parse the test server url: %v", err)
	}
	// A different port on the same host: a different origin, which is exactly
	// the development case.
	metro := "http://" + parsed.Hostname() + ":8081"

	refused := handshake(t, base, metro)
	if refused != http.StatusForbidden {
		t.Fatalf("an unlisted development origin got %d; want %d", refused, http.StatusForbidden)
	}

	allowed := handshake(t, serving(t, parsed.Hostname()+":8081"), metro)
	if allowed != http.StatusSwitchingProtocols {
		t.Fatalf("a listed origin got %d; want %d", allowed, http.StatusSwitchingProtocols)
	}
}
