package helper

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/shopspring/decimal"

	"github.com/spioneracorei8/btcusd-trading-platform/server/constants"
)

// APIError is the body of every failed response.
//
// One shape for every failure, with a code the app may branch on and a message
// for the person reading it. The code is stable; the message is not.
type APIError struct {
	Error APIErrorBody `json:"error"`
}

// APIErrorBody carries the code and the message.
type APIErrorBody struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// WriteAPIJSON encodes body as JSON with the given status.
//
// The body is marshalled before the header is written, so an encoding failure
// still produces a clean error rather than a truncated response with a 200
// already on the wire.
func WriteAPIJSON(w http.ResponseWriter, log *slog.Logger, status int, body any) {
	payload, err := json.Marshal(body)
	if err != nil {
		if log != nil {
			log.Error("encode response body", "error", err)
		}
		WriteAPIError(w, nil, http.StatusInternalServerError,
			constants.APIErrInternal, "the response could not be encoded")
		return
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if _, err := w.Write(payload); err != nil && log != nil {
		log.Error("write response body", "error", err)
	}
}

// WriteAPIError writes the error envelope.
func WriteAPIError(
	w http.ResponseWriter, log *slog.Logger,
	status int, code constants.APIErrorCode, message string,
) {
	payload, err := json.Marshal(APIError{
		Error: APIErrorBody{Code: code.String(), Message: message},
	})
	if err != nil {
		// Nothing left to encode with, so the last resort is plain text.
		http.Error(w, `{"error":{"code":"internal","message":"unencodable"}}`,
			http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if _, err := w.Write(payload); err != nil && log != nil {
		log.Error("write error body", "error", err)
	}
}

// QueryTime reads an RFC3339 timestamp, falling back when absent.
func QueryTime(r *http.Request, name string, fallback time.Time) (time.Time, error) {
	raw := r.URL.Query().Get(name)
	if raw == "" {
		return fallback, nil
	}

	at, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		return time.Time{}, fmt.Errorf("%s=%q is not an RFC3339 timestamp", name, raw)
	}
	return at.UTC(), nil
}

// QueryLimit reads a row limit, refusing one above the cap.
//
// # Why the cap is an error and not a silent clamp
//
// A phone asking for three years of 1m candles and receiving five thousand
// would have no way to know it received a slice. Refusing says what happened
// and what to do instead; clamping produces a chart that is quietly wrong.
func QueryLimit(r *http.Request, name string, fallback, max int) (int, error) {
	raw := r.URL.Query().Get(name)
	if raw == "" {
		return fallback, nil
	}

	limit, err := strconv.Atoi(raw)
	if err != nil || limit <= 0 {
		return 0, fmt.Errorf("%s=%q is not a positive whole number", name, raw)
	}
	if limit > max {
		return 0, fmt.Errorf("%s=%d is above the maximum of %d; page instead", name, limit, max)
	}
	return limit, nil
}

// QueryOffset reads a non-negative page offset.
func QueryOffset(r *http.Request, name string) (int, error) {
	raw := r.URL.Query().Get(name)
	if raw == "" {
		return 0, nil
	}

	offset, err := strconv.Atoi(raw)
	if err != nil || offset < 0 {
		return 0, fmt.Errorf("%s=%q is not a whole number of rows to skip", name, raw)
	}
	return offset, nil
}

// NullableTime renders a time that may be absent.
//
// Absent is null and never the zero instant: 0001-01-01 read as a real
// timestamp is worse than nothing, because it sorts and formats like one.
func NullableTime(at time.Time) *time.Time {
	if at.IsZero() {
		return nil
	}
	utc := at.UTC()
	return &utc
}

// NullableDecimal renders a price that may be absent.
//
// Null, never "0": a zero price reads as a real one and would be charted,
// averaged and compared like one. Strings rather than JSON numbers, because a
// phone parsing 0.1 + 0.2 is the same hazard as a server doing it and a
// float64 cannot hold every numeric(20,8).
func NullableDecimal(d decimal.NullDecimal) *string {
	if !d.Valid {
		return nil
	}
	value := d.Decimal.String()
	return &value
}
