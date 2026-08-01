// Package logger builds the structured slog logger used by every binary.
//
// All logging goes through log/slog; fmt.Println is not used anywhere.
package logger

import (
	"io"
	"log/slog"

	"github.com/spioneracorei8/btcusd-trading-platform/server/constants"
)

// Format selects the slog handler used to render records.
type Format string

// Supported log formats.
const (
	// FormatText is human readable and meant for local development.
	FormatText Format = "text"
	// FormatJSON is machine readable and meant for production.
	FormatJSON Format = "json"
)

// Options configures the logger returned by New.
type Options struct {
	// Level is the minimum level that is emitted.
	Level slog.Level
	// Format selects the handler; an unknown value falls back to FormatText.
	Format Format
	// AddSource adds the source file and line to every record.
	AddSource bool
}

// FormatForEnv maps an application environment onto a log format:
// JSON in prod, text everywhere else.
func FormatForEnv(env constants.AppEnv) Format {
	if env == constants.EnvProd {
		return FormatJSON
	}
	return FormatText
}

// New builds a slog.Logger writing to w with the given options.
func New(w io.Writer, opts Options) *slog.Logger {
	handlerOpts := &slog.HandlerOptions{
		Level:     opts.Level,
		AddSource: opts.AddSource,
	}

	var handler slog.Handler
	switch opts.Format {
	case FormatJSON:
		handler = slog.NewJSONHandler(w, handlerOpts)
	default:
		handler = slog.NewTextHandler(w, handlerOpts)
	}
	return slog.New(handler)
}
