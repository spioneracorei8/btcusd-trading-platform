package handler

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/spioneracorei8/btcusd-trading-platform/server/constants"
	"github.com/spioneracorei8/btcusd-trading-platform/server/models"
	"github.com/spioneracorei8/btcusd-trading-platform/server/services/signal"
)

const testSymbol = "BTCUSDT"

var signalTime = time.Date(2024, 3, 1, 12, 0, 0, 0, time.UTC)

func quiet() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

// fakeSignals serves a prepared page and records what it was asked for.
type fakeSignals struct {
	page  []models.Signal
	total int64
	err   error

	one    models.Signal
	oneErr error

	asked    signal.ListParams
	askedId  uuid.UUID
	listCall int
}

func (f *fakeSignals) ListSignals(_ context.Context, params signal.ListParams) ([]models.Signal, int64, error) {
	f.asked = params
	f.listCall++
	return f.page, f.total, f.err
}

func (f *fakeSignals) FetchSignalById(_ context.Context, id uuid.UUID) (models.Signal, error) {
	f.askedId = id
	return f.one, f.oneErr
}

func (f *fakeSignals) CreateSignal(context.Context, models.Signal, models.Candle) (models.Signal, error) {
	return models.Signal{}, nil
}

func (f *fakeSignals) SetEntryPrice(context.Context, uuid.UUID, decimal.Decimal) (models.Signal, error) {
	return models.Signal{}, nil
}

func aSignal(id uuid.UUID, direction constants.Direction, at time.Time) models.Signal {
	return models.Signal{
		Id: id, Symbol: testSymbol, MarketType: constants.MarketTypeSpot,
		Timeframe: constants.Timeframe4h, SignalTime: at, Direction: direction,
		Strength:    decimal.NewFromInt(50),
		SignalPrice: decimal.NewNullDecimal(decimal.RequireFromString("64000")),
		EntryPrice:  decimal.NewNullDecimal(decimal.RequireFromString("64010.01")),
		StopLoss:    decimal.NewNullDecimal(decimal.RequireFromString("63500")),
		TakeProfit:  decimal.NewNullDecimal(decimal.RequireFromString("65000")),

		StrategyName: "ema_crossover", StrategyVersion: "v1",
		Reason:    []byte(`{"trigger":"fast crossed slow","indicators":{"ema":64000.5}}`),
		CreatedAt: at.Add(time.Second),
	}
}

func newHandler(fake *fakeSignals) signal.SignalHandler {
	return NewSignalHandlerImpl(fake, quiet(), testSymbol, constants.MarketTypeSpot)
}

func list(t *testing.T, fake *fakeSignals, query string) (*httptest.ResponseRecorder, []byte) {
	t.Helper()

	recorder := httptest.NewRecorder()
	newHandler(fake).Signals(recorder, httptest.NewRequest(http.MethodGet, "/api/v1/signals"+query, nil))
	return recorder, recorder.Body.Bytes()
}

// byId routes through chi so the {id} parameter is populated the way it is in
// production; reading it out of a bare request would return "".
func byId(t *testing.T, fake *fakeSignals, id string) (*httptest.ResponseRecorder, []byte) {
	t.Helper()

	router := chi.NewRouter()
	router.Get("/api/v1/signals/{id}", newHandler(fake).Signal)

	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/v1/signals/"+id, nil))
	return recorder, recorder.Body.Bytes()
}

// TestTheReasonIsOnTheDetailEndpointAndNotOnTheList.
//
// # What this prevents
//
// The reason carries the indicator snapshot, the trend state and the resolved
// parameter set — it is what makes a signal reviewable months later, and it is
// large. A page of fifty would be mostly reason, over a mobile connection, to
// render a list that shows none of it. It has to be on one endpoint and not
// the other, and this is the pair of assertions that keeps it that way.
func TestTheReasonIsOnTheDetailEndpointAndNotOnTheList(t *testing.T) {
	id := uuid.New()
	one := aSignal(id, constants.DirectionLong, signalTime)

	_, listed := list(t, &fakeSignals{page: []models.Signal{one}, total: 1}, "")
	if strings.Contains(string(listed), "\"reason\"") {
		t.Errorf("the list carries the reason:\n%s", listed)
	}
	if !strings.Contains(string(listed), id.String()) {
		t.Fatalf("the list does not carry the signal at all:\n%s", listed)
	}

	_, detail := byId(t, &fakeSignals{one: one}, id.String())

	var body struct {
		Reason json.RawMessage `json:"reason"`
	}
	if err := json.Unmarshal(detail, &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(body.Reason) == 0 {
		t.Fatalf("the detail endpoint carries no reason:\n%s", detail)
	}
	if !strings.Contains(string(body.Reason), "fast crossed slow") {
		t.Errorf("the reason was not passed through intact: %s", body.Reason)
	}
}

// TestTotalIsTheCollectionRatherThanThePage, so a client can tell a short page
// from the last page without a second request.
func TestTotalIsTheCollectionRatherThanThePage(t *testing.T) {
	page := []models.Signal{
		aSignal(uuid.New(), constants.DirectionLong, signalTime),
		aSignal(uuid.New(), constants.DirectionShort, signalTime.Add(-4*time.Hour)),
	}

	_, body := list(t, &fakeSignals{page: page, total: 137}, "?limit=2")

	var response struct {
		Count int   `json:"count"`
		Total int64 `json:"total"`
		Limit int   `json:"limit"`
	}
	if err := json.Unmarshal(body, &response); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if response.Count != 2 {
		t.Errorf("count = %d, want 2", response.Count)
	}
	if response.Total != 137 {
		t.Errorf("total = %d, want 137: the size of the collection, not the page", response.Total)
	}
	if response.Limit != 2 {
		t.Errorf("limit = %d, want 2", response.Limit)
	}
}

// TestPagingIsPassedThroughToTheQuery. A handler that dropped offset would
// serve page one forever, which looks like a client bug and is not.
func TestPagingIsPassedThroughToTheQuery(t *testing.T) {
	fake := &fakeSignals{}
	list(t, fake, "?limit=25&offset=50&direction=short")

	if fake.asked.Limit != 25 {
		t.Errorf("queried limit %d, want 25", fake.asked.Limit)
	}
	if fake.asked.Offset != 50 {
		t.Errorf("queried offset %d, want 50", fake.asked.Offset)
	}
	if fake.asked.Direction != constants.DirectionShort {
		t.Errorf("queried direction %q, want short", fake.asked.Direction)
	}
	if fake.asked.Symbol != testSymbol {
		t.Errorf("queried symbol %q, want %q", fake.asked.Symbol, testSymbol)
	}
}

// TestAnUnfilteredListAsksForBothDirections. An empty direction must reach the
// query as empty rather than defaulting to long, which would hide half the
// history behind a parameter nobody set.
func TestAnUnfilteredListAsksForBothDirections(t *testing.T) {
	fake := &fakeSignals{}
	list(t, fake, "")

	if fake.asked.Direction != "" {
		t.Fatalf("queried direction %q with no filter, want empty for both", fake.asked.Direction)
	}
}

// TestBadParametersAreRefusedBeforeTheQuery.
func TestBadParametersAreRefusedBeforeTheQuery(t *testing.T) {
	for _, tc := range []struct {
		query string
		code  constants.APIErrorCode
	}{
		{"?limit=99999", constants.APIErrLimitExceeded},
		{"?limit=banana", constants.APIErrInvalidParameter},
		{"?offset=-1", constants.APIErrInvalidParameter},
		{"?direction=sideways", constants.APIErrInvalidParameter},
	} {
		t.Run(tc.query, func(t *testing.T) {
			fake := &fakeSignals{}
			recorder, body := list(t, fake, tc.query)

			if recorder.Code != http.StatusBadRequest {
				t.Fatalf("status %d, want 400", recorder.Code)
			}
			if fake.listCall != 0 {
				t.Error("the query ran anyway")
			}

			var failure struct {
				Error struct {
					Code string `json:"code"`
				} `json:"error"`
			}
			if err := json.Unmarshal(body, &failure); err != nil {
				t.Fatalf("decode: %v", err)
			}
			if failure.Error.Code != string(tc.code) {
				t.Errorf("code = %q, want %q", failure.Error.Code, tc.code)
			}
		})
	}
}

// TestAnIdThatIsNotAUuidIsFourHundredAndAnUnknownOneIsFourOhFour.
//
// Two different mistakes: a malformed request, and a request for something
// that is not there. Collapsing them into one status makes a client retry the
// wrong one.
func TestAnIdThatIsNotAUuidIsFourHundredAndAnUnknownOneIsFourOhFour(t *testing.T) {
	recorder, _ := byId(t, &fakeSignals{}, "not-a-uuid")
	if recorder.Code != http.StatusBadRequest {
		t.Errorf("a malformed id returned %d, want 400", recorder.Code)
	}

	recorder, _ = byId(t, &fakeSignals{oneErr: constants.ErrNotFound}, uuid.NewString())
	if recorder.Code != http.StatusNotFound {
		t.Errorf("an unknown id returned %d, want 404", recorder.Code)
	}
}

// TestAReadFailureDoesNotReachTheClient. The driver's error can carry a
// connection string; the client gets a 500 and a fixed sentence.
func TestAReadFailureDoesNotReachTheClient(t *testing.T) {
	recorder, body := list(t, &fakeSignals{err: context.DeadlineExceeded}, "")
	if recorder.Code != http.StatusInternalServerError {
		t.Fatalf("status %d, want 500", recorder.Code)
	}
	if strings.Contains(string(body), "context deadline") {
		t.Errorf("the internal error leaked: %s", body)
	}

	recorder, body = byId(t, &fakeSignals{oneErr: context.DeadlineExceeded}, uuid.NewString())
	if recorder.Code != http.StatusInternalServerError {
		t.Fatalf("status %d, want 500", recorder.Code)
	}
	if strings.Contains(string(body), "context deadline") {
		t.Errorf("the internal error leaked: %s", body)
	}
}

// TestAnEmptyPageIsAnEmptyArray rather than null, so a client handles one
// shape on a quiet week.
func TestAnEmptyPageIsAnEmptyArray(t *testing.T) {
	_, body := list(t, &fakeSignals{}, "")

	var response map[string]json.RawMessage
	if err := json.Unmarshal(body, &response); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got := string(response["signals"]); got != "[]" {
		t.Fatalf("signals = %s, want []", got)
	}
}

// TestAnUnpricedSignalRendersNullRatherThanZero.
//
// EntryPrice is not knowable when a signal is recorded — the fill is the next
// bar's open — so it is null until that bar closes. A zero would be charted,
// averaged and compared like a real price.
func TestAnUnpricedSignalRendersNullRatherThanZero(t *testing.T) {
	one := aSignal(uuid.New(), constants.DirectionLong, signalTime)
	one.EntryPrice = decimal.NullDecimal{}

	_, body := byId(t, &fakeSignals{one: one}, one.Id.String())

	var response map[string]json.RawMessage
	if err := json.Unmarshal(body, &response); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got := string(response["entry_price"]); got != "null" {
		t.Fatalf("entry_price = %s, want null", got)
	}
	if got := string(response["signal_price"]); got != `"64000"` {
		t.Fatalf("signal_price = %s, want \"64000\": the one that is known stays", got)
	}
}
