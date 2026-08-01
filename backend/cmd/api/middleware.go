package main

import (
	"log/slog"
	"net/http"
	"runtime/debug"
	"time"

	"github.com/go-chi/chi/v5/middleware"
)

// requestLogger logs one structured record per request with the method, path,
// status, duration and request id.
func requestLogger(logger *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			ww := middleware.NewWrapResponseWriter(w, r.ProtoMajor)

			next.ServeHTTP(ww, r)

			logger.LogAttrs(r.Context(), levelForStatus(ww.Status()), "http request",
				slog.String("method", r.Method),
				slog.String("path", r.URL.Path),
				slog.Int("status", ww.Status()),
				slog.Duration("duration", time.Since(start)),
				slog.String("request_id", middleware.GetReqID(r.Context())),
				slog.Int("bytes", ww.BytesWritten()),
			)
		})
	}
}

// levelForStatus keeps ordinary traffic at debug-free info level while making
// failures visible at a glance.
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

// recoverer turns a panic in a handler into a 500 instead of killing the
// process. Business logic must never panic; this is the last line of defence.
func recoverer(logger *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
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

				logger.LogAttrs(r.Context(), slog.LevelError, "panic recovered in handler",
					slog.Any("panic", rec),
					slog.String("method", r.Method),
					slog.String("path", r.URL.Path),
					slog.String("request_id", middleware.GetReqID(r.Context())),
					slog.String("stack", string(debug.Stack())),
				)
				writeJSON(w, logger, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
			}()

			next.ServeHTTP(w, r)
		})
	}
}
