// Package middleware holds the HTTP middleware shared by every route.
package middleware

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"runtime/debug"
	"time"

	"github.com/go-chi/chi/v5/middleware"

	"github.com/spioneracorei8/btcusd-trading-platform/server/constants"
	"github.com/spioneracorei8/btcusd-trading-platform/server/models"
)

// MyMiddleware carries what the middleware needs to do its job.
type MyMiddleware struct {
	logger *slog.Logger
}

// InitMiddleware builds the middleware set.
func InitMiddleware(logger *slog.Logger) MyMiddleware {
	return MyMiddleware{logger: logger}
}

// RequestID attaches a correlation id to every request.
func (m MyMiddleware) RequestID(next http.Handler) http.Handler {
	return middleware.RequestID(next)
}

// RequestLogger logs one structured record per request with the method, path,
// status, duration and request id.
func (m MyMiddleware) RequestLogger(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		ww := middleware.NewWrapResponseWriter(w, r.ProtoMajor)

		next.ServeHTTP(ww, r)

		m.logger.LogAttrs(r.Context(), levelForStatus(ww.Status()), "http request",
			slog.String("method", r.Method),
			slog.String("path", r.URL.Path),
			slog.Int("status", ww.Status()),
			slog.Duration("duration", time.Since(start)),
			slog.String("request_id", middleware.GetReqID(r.Context())),
			slog.Int("bytes", ww.BytesWritten()),
		)
	})
}

// levelForStatus keeps ordinary traffic at info level while making failures
// visible at a glance.
func levelForStatus(status int) slog.Level {
	switch {
	case status >= http.StatusInternalServerError:
		return slog.LevelError
	case status >= http.StatusBadRequest:
		return slog.LevelWarn
	default:
		return slog.LevelInfo
	}
}

// Recoverer turns a panic in a handler into a 500 instead of killing the
// process. Business logic must never panic; this is the last line of defence.
func (m MyMiddleware) Recoverer(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			rec := recover()
			if rec == nil {
				return
			}
			// http.ErrAbortHandler is a deliberate abort, not a bug.
			if rec == http.ErrAbortHandler {
				panic(rec)
			}

			m.logger.LogAttrs(r.Context(), slog.LevelError, "panic recovered in handler",
				slog.Any("panic", rec),
				slog.String("method", r.Method),
				slog.String("path", r.URL.Path),
				slog.String("request_id", middleware.GetReqID(r.Context())),
				slog.String("stack", string(debug.Stack())),
			)

			payload, err := json.Marshal(models.ErrorResponse{Error: constants.MsgInternalServerError})
			if err != nil {
				http.Error(w, constants.MsgInternalServerError, http.StatusInternalServerError)
				return
			}
			w.Header().Set("Content-Type", "application/json; charset=utf-8")
			w.WriteHeader(http.StatusInternalServerError)
			if _, err := w.Write(payload); err != nil {
				m.logger.Error("write panic response", "error", err)
			}
		}()

		next.ServeHTTP(w, r)
	})
}
