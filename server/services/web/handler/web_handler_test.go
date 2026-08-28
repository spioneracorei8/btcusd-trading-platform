package handler

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spioneracorei8/btcusd-trading-platform/server/services/web"
)

// exported builds a directory shaped like an expo web export.
func exported(t *testing.T) string {
	t.Helper()
	root := t.TempDir()

	write := func(rel, body string) {
		t.Helper()
		full := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatalf("make %s: %v", filepath.Dir(full), err)
		}
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			t.Fatalf("write %s: %v", rel, err)
		}
	}

	write("index.html", "<!doctype html><title>app</title>")
	write("manifest.json", `{"name":"BTCUSD Signals"}`)
	write("sw.js", "// service worker")
	write("_expo/static/js/entry-0123456789abcdef0123.js", "console.log(1)")
	write("assets/logo.png", "PNG")

	return root
}

func serve(t *testing.T, root, method, path string) *http.Response {
	t.Helper()
	handler := newHandler(root)

	req := httptest.NewRequest(method, path, nil)
	rec := httptest.NewRecorder()
	handler.App(rec, req)
	return rec.Result()
}

func newHandler(root string) web.AppHandler {
	return NewAppHandlerImpl(root, slog.New(slog.NewTextHandler(io.Discard, nil)))
}

func body(t *testing.T, resp *http.Response) string {
	t.Helper()
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read the body: %v", err)
	}
	return string(raw)
}

/*
TestAScreenPathLoadsTheApp.

# What this prevents

/signals/{id} is a screen, not a file, and nothing exports it. It is also the
URL a notification tap cold-loads, which is the one path in this system that a
person reaches without the app already running.

Answering it with 404 would mean every notification opens an error page, and
the failure would only appear on a real phone with a real alert — the most
expensive place to find it.
*/
func TestAScreenPathLoadsTheApp(t *testing.T) {
	root := exported(t)

	resp := serve(t, root, http.MethodGet, "/signals/3f2504e0-4f89-11d3-9a0c-0305e82c3301")

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("a screen path got %d; want 200", resp.StatusCode)
	}
	if got := body(t, resp); got != "<!doctype html><title>app</title>" {
		t.Fatalf("a screen path served %q; want the entry document", got)
	}
}

/*
TestAMissingAssetIs404AndNotTheApp.

The mirror of the case above, and the reason it is a heuristic rather than a
blanket fallback. Answering a missing .js with HTML produces "unexpected token
'<'" in the console, which reads like a syntax error in a file that does not
exist — and sends whoever is debugging to the wrong place entirely.
*/
func TestAMissingAssetIs404AndNotTheApp(t *testing.T) {
	root := exported(t)

	resp := serve(t, root, http.MethodGet, "/_expo/static/js/gone-9999999999999999.js")

	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("a missing asset got %d; want 404", resp.StatusCode)
	}
}

/*
TestAPathCannotLeaveTheWebRoot.

The root holds an export; the process holds a database URL and an FCM
credential path in its environment and a checkout above it on disk. A path
walking out of the root is the difference between serving an app and serving
the filesystem.
*/
func TestAPathCannotLeaveTheWebRoot(t *testing.T) {
	root := exported(t)

	secret := filepath.Join(filepath.Dir(root), "secrets.env")
	if err := os.WriteFile(secret, []byte("DATABASE_URL=postgres://real"), 0o600); err != nil {
		t.Fatalf("write the file that must not be served: %v", err)
	}

	for _, path := range []string{
		"/../secrets.env",
		"/assets/../../secrets.env",
		"/%2e%2e/secrets.env",
		"/..%2fsecrets.env",
	} {
		resp := serve(t, root, http.MethodGet, path)
		got := body(t, resp)

		if strings.Contains(got, "postgres://real") {
			t.Fatalf("%s served the file outside the root", path)
		}
		// 404 is the honest answer. The entry document is tolerable — it is
		// what any unknown screen path gets — but the secret never is.
		if resp.StatusCode != http.StatusNotFound && !strings.HasPrefix(got, "<!doctype html>") {
			t.Fatalf("%s answered %d with %q", path, resp.StatusCode, got)
		}
	}
}

/*
TestTheServiceWorkerIsNeverServedFromCache.

# What this prevents

A service worker answered from the browser's HTTP cache cannot replace itself:
the update check fetches the copy it is trying to replace, finds it identical,
and the deployment stops reaching anybody. There is no error — the app simply
stays on an old build indefinitely, which is the classic PWA failure and the
one the phase text calls out by name.

The entry document and the manifest are the same argument: neither carries a
content hash, so a cached copy is a deployment nobody receives.
*/
func TestTheServiceWorkerIsNeverServedFromCache(t *testing.T) {
	root := exported(t)

	for _, path := range []string{"/sw.js", "/index.html", "/manifest.json", "/"} {
		resp := serve(t, root, http.MethodGet, path)
		if got := resp.Header.Get("Cache-Control"); got != "no-cache" {
			t.Fatalf("%s was served with Cache-Control %q; want no-cache", path, got)
		}
	}
}

/*
TestAFingerprintedBundleIsCachedHard.

The other half of the rule: a file whose name changes with its content is safe
to keep forever, and on a phone over a tailnet that is the difference between
an app that opens instantly and one that refetches its bundle every launch.
*/
func TestAFingerprintedBundleIsCachedHard(t *testing.T) {
	root := exported(t)

	resp := serve(t, root, http.MethodGet, "/_expo/static/js/entry-0123456789abcdef0123.js")

	if got := resp.Header.Get("Cache-Control"); got != "public, max-age=31536000, immutable" {
		t.Fatalf("a fingerprinted bundle was served with Cache-Control %q", got)
	}
}

/*
TestAnUnfingerprintedAssetIsRevalidated.

The safe answer for anything unrecognised. A stale asset is a bug; a
re-request is a few milliseconds on a tailnet.
*/
func TestAnUnfingerprintedAssetIsRevalidated(t *testing.T) {
	root := exported(t)

	resp := serve(t, root, http.MethodGet, "/assets/logo.png")

	if got := resp.Header.Get("Cache-Control"); got != "no-cache" {
		t.Fatalf("an unfingerprinted asset was served with Cache-Control %q; want no-cache", got)
	}
}

/*
TestTheAppRefusesToBeWrittenTo.

The app is a reader (phase 09 part D). This server serves files; there is no
request shape that should change one, and a POST arriving here is either a
mistake or somebody probing. Neither deserves the entry document and a 200.
*/
func TestTheAppRefusesToBeWrittenTo(t *testing.T) {
	root := exported(t)

	resp := serve(t, root, http.MethodPost, "/signals")

	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("POST to the app got %d; want 405", resp.StatusCode)
	}
	if got := resp.Header.Get("Allow"); got != "GET, HEAD" {
		t.Fatalf("Allow was %q; want \"GET, HEAD\"", got)
	}
}

/*
TestAnUnbuiltExportSaysSo.

WEB_ROOT pointing at a directory that exists but holds no export is a
deployment where somebody forgot to run the build. A bare 404 there reads like
a routing bug and sends the next hour in the wrong direction.
*/
func TestAnUnbuiltExportSaysSo(t *testing.T) {
	resp := serve(t, t.TempDir(), http.MethodGet, "/")

	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("an unbuilt export got %d; want 404", resp.StatusCode)
	}
	if got := body(t, resp); got == "" {
		t.Fatal("an unbuilt export answered with an empty body")
	}
}

/*
TestResolveRefusesAPathThatEscapesTheRoot.

# Why this tests a function rather than a request

No request can reach this branch. `path.Clean("/" + r.URL.Path)` runs first,
and a path rooted at "/" before cleaning cannot climb out of it — which is why
TestAPathCannotLeaveTheWebRoot passes with the check below deleted.

That makes it defence in depth, and an untested guard is a guard nobody knows
works. It is the check that still holds if the Clean above it is ever edited,
so it is tested where it can actually be reached.
*/
func TestResolveRefusesAPathThatEscapesTheRoot(t *testing.T) {
	root := exported(t)
	h, ok := newHandler(root).(*webHandler)
	if !ok {
		t.Fatal("the handler is no longer a *webHandler; this test needs its internals")
	}

	if _, err := h.resolve("/../secrets.env"); err == nil {
		t.Fatal("an uncleaned path climbed out of the web root")
	}
	if _, err := h.resolve("/assets/logo.png"); err != nil {
		t.Fatalf("a path inside the root was refused: %v", err)
	}
}
