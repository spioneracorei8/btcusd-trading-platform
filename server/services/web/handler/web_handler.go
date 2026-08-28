package handler

import (
	"errors"
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/spioneracorei8/btcusd-trading-platform/server/services/web"
)

// entryDocument is what a path belonging to the app's own routing resolves to.
const entryDocument = "index.html"

type webHandler struct {
	root   string
	logger *slog.Logger
}

// NewAppHandlerImpl serves the app exported into root.
//
// The caller has already checked that root is a directory; see
// config.loader.directory, which does it at start-up so a typo is a refusal to
// boot rather than a site that 404s.
func NewAppHandlerImpl(root string, logger *slog.Logger) web.AppHandler {
	return &webHandler{root: filepath.Clean(root), logger: logger}
}

// App serves one file from the export.
//
// # Why a missing path is not always a 404
//
// The app routes in the browser: /signals/{id} is a screen, not a file, and a
// cold load of that URL — which is exactly what tapping a notification does —
// asks this server for a path that was never exported. So a path that could be
// a screen resolves to the entry document and the app takes it from there.
//
// A path that looks like an asset does not. Answering a missing .js with HTML
// produces "unexpected token '<'" in the console, which sends whoever is
// debugging to look for a syntax error in a file that is not there.
func (h *webHandler) App(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	clean := path.Clean("/" + r.URL.Path)

	target, err := h.resolve(clean)
	if err != nil {
		// Only a traversal attempt reaches here, and it is worth a line: the
		// path came from outside and tried to leave the directory.
		h.logger.WarnContext(r.Context(), "refused a path outside the web root",
			"path", r.URL.Path)
		http.NotFound(w, r)
		return
	}

	info, err := os.Stat(target)
	switch {
	case err == nil && info.IsDir():
		// A directory is the app's entry document, not a listing. http.FileServer
		// would redirect and then index the directory; neither is wanted.
		h.serve(w, r, filepath.Join(target, entryDocument), clean)
		return

	case err == nil:
		h.serve(w, r, target, clean)
		return

	case errors.Is(err, fs.ErrNotExist):
		if looksLikeAsset(clean) {
			http.NotFound(w, r)
			return
		}
		h.serve(w, r, filepath.Join(h.root, entryDocument), clean)
		return

	default:
		h.logger.ErrorContext(r.Context(), "stat a web asset",
			"path", r.URL.Path, "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
	}
}

// resolve maps a cleaned request path onto a path inside the root.
//
// path.Clean has already removed any ".." that could escape, because the path
// is rooted at "/" before cleaning. The prefix check is the second belt: it
// costs nothing and it is the check that still holds if the first line above
// is ever edited.
func (h *webHandler) resolve(clean string) (string, error) {
	target := filepath.Join(h.root, filepath.FromSlash(clean))
	if target != h.root && !strings.HasPrefix(target, h.root+string(filepath.Separator)) {
		return "", errors.New("path escapes the web root")
	}
	return target, nil
}

// serve writes one file with the caching its kind deserves.
func (h *webHandler) serve(w http.ResponseWriter, r *http.Request, file, requested string) {
	f, err := os.Open(file)
	if err != nil {
		// The entry document is missing, which means the export never
		// happened or WEB_ROOT points at the wrong directory. Say which,
		// because a bare 404 here reads like a routing bug.
		if errors.Is(err, fs.ErrNotExist) {
			h.logger.ErrorContext(r.Context(), "the web export has no entry document",
				"root", h.root, "wanted", file)
			http.Error(w, "the web app is not built", http.StatusNotFound)
			return
		}
		h.logger.ErrorContext(r.Context(), "open a web asset", "file", file, "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	defer func() { _ = f.Close() }()

	info, err := f.Stat()
	if err != nil || info.IsDir() {
		http.NotFound(w, r)
		return
	}

	w.Header().Set("Cache-Control", cacheControl(requested, file))
	// ServeContent handles range requests, conditional requests and the
	// content type, and it is the reason this is not a hand-written io.Copy.
	http.ServeContent(w, r, filepath.Base(file), info.ModTime(), f)
}

// cacheControl decides how long a browser may hold a file.
//
// # Why the default is revalidate rather than a long max-age
//
// Only a file whose name changes when its content changes may be cached hard.
// Everything else is answered with no-cache, and the two that matter most are
// covered by that default rather than by a rule of their own:
//
//   - The service worker. One answered from the browser's HTTP cache cannot
//     replace itself — the update check fetches the copy it is trying to
//     replace, finds it identical, and the deployment sticks on an old build
//     with no symptom except that nothing new ever appears. A worker is never
//     fingerprinted, because it has to stay at a stable URL to keep its scope,
//     so it lands here by construction.
//   - The entry document and the manifest, for the same reason: neither
//     carries a content hash, so a cached copy is a deployment nobody
//     receives.
//
// A stale asset is a bug; a re-request is a few milliseconds on a tailnet.
func cacheControl(requested, file string) string {
	if strings.HasSuffix(file, ".html") || strings.HasSuffix(requested, "manifest.json") {
		return "no-cache"
	}
	if fingerprinted(path.Base(requested)) {
		return "public, max-age=31536000, immutable"
	}
	return "no-cache"
}

// fingerprinted reports whether a filename carries a content hash.
//
// Expo's web export names bundles like `entry-8f3a....js` and its assets like
// `<hash>.png`. A name is treated as fingerprinted only when a segment is a
// long run of hex, which no hand-written filename here is.
func fingerprinted(name string) bool {
	for _, segment := range strings.FieldsFunc(name, func(r rune) bool {
		return r == '.' || r == '-' || r == '_'
	}) {
		if len(segment) < 16 {
			continue
		}
		if isHex(segment) {
			return true
		}
	}
	return false
}

func isHex(s string) bool {
	for _, r := range s {
		switch {
		case r >= '0' && r <= '9', r >= 'a' && r <= 'f', r >= 'A' && r <= 'F':
		default:
			return false
		}
	}
	return true
}

// looksLikeAsset reports whether a path names a file rather than a screen.
//
// The app's own routes have no extension — /signals/{uuid} is the deepest one
// — so an extension is the signal. It is a heuristic, and the cost of it being
// wrong is bounded: a screen path that somehow ends in an extension 404s
// instead of loading, which is visible immediately rather than silently.
func looksLikeAsset(clean string) bool {
	return path.Ext(clean) != ""
}
