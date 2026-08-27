package handler_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/spioneracorei8/btcusd-trading-platform/server/constants"
	"github.com/spioneracorei8/btcusd-trading-platform/server/models"
	"github.com/spioneracorei8/btcusd-trading-platform/server/services/notify"
	_notify_handler "github.com/spioneracorei8/btcusd-trading-platform/server/services/notify/handler"
	_notify_us "github.com/spioneracorei8/btcusd-trading-platform/server/services/notify/usecase"
)

const aToken = "fMEP0vJqSk6:APA91bHabcdefghijklmnopqrstuvwxyz0123456789ABCDEFGHIJKLMNOP"

func quiet() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

// deviceStore is the registration, in a field. It stands in for
// notify.DeviceRepository and the real usecase is layered over it by
// handlerFor, so these tests exercise the validation the 400s actually depend
// on rather than a fake of it.
type deviceStore struct {
	device *models.Device
	err    error

	registered []models.Device
}

func (d *deviceStore) RegisterDevice(_ context.Context, in models.Device) (models.Device, error) {
	if d.err != nil {
		return models.Device{}, d.err
	}
	d.registered = append(d.registered, in)
	d.device = &in
	return in, nil
}

func (d *deviceStore) FetchDevice(context.Context) (models.Device, error) {
	if d.err != nil {
		return models.Device{}, d.err
	}
	if d.device == nil {
		return models.Device{}, constants.ErrNotFound
	}
	return *d.device, nil
}

func (d *deviceStore) DeleteDevice(context.Context) (bool, error) {
	if d.err != nil {
		return false, d.err
	}
	had := d.device != nil
	d.device = nil
	return had, nil
}

func handlerFor(store *deviceStore, mode constants.SignalMode) notify.DeviceHandler {
	usecase, err := _notify_us.NewDeviceUsecaseImpl(store, quiet())
	if err != nil {
		panic(err)
	}
	return _notify_handler.NewDeviceHandlerImpl(usecase, quiet(), mode)
}

func post(t *testing.T, h notify.DeviceHandler, body string) (*httptest.ResponseRecorder, map[string]json.RawMessage) {
	t.Helper()

	recorder := httptest.NewRecorder()
	h.RegisterDevice(recorder, httptest.NewRequest(
		http.MethodPost, "/api/v1/device", bytes.NewBufferString(body)))
	return recorder, decode(t, recorder)
}

func decode(t *testing.T, recorder *httptest.ResponseRecorder) map[string]json.RawMessage {
	t.Helper()

	var body map[string]json.RawMessage
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode %s: %v", recorder.Body.String(), err)
	}
	return body
}

// TestTheTokenIsNeverEchoedBack.
//
// # What this prevents
//
// There is no authentication in front of this endpoint — the network is the
// boundary (ADR 0024). The registration token is the one credential in this
// system that lets anything push to the owner's phone, and an endpoint that
// returned it would hand it to anything that could reach the port. The app
// already has the token; nothing needs it back.
func TestTheTokenIsNeverEchoedBack(t *testing.T) {
	store := &deviceStore{}
	h := handlerFor(store, constants.SignalModeNotify)

	recorder, _ := post(t, h, `{"token":"`+aToken+`","platform":"android","label":"Pixel 7a"}`)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status %d, want 200: %s", recorder.Code, recorder.Body)
	}
	if strings.Contains(recorder.Body.String(), aToken) {
		t.Errorf("the registration response carries the token:\n%s", recorder.Body)
	}

	// And the same on the read and the delete.
	read := httptest.NewRecorder()
	h.Device(read, httptest.NewRequest(http.MethodGet, "/api/v1/device", nil))
	if strings.Contains(read.Body.String(), aToken) {
		t.Errorf("GET carries the token:\n%s", read.Body)
	}

	removed := httptest.NewRecorder()
	h.ForgetDevice(removed, httptest.NewRequest(http.MethodDelete, "/api/v1/device", nil))
	if strings.Contains(removed.Body.String(), aToken) {
		t.Errorf("DELETE carries the token:\n%s", removed.Body)
	}
}

// TestSilentModeSaysSoOnRegistration.
//
// Registering and being delivered to are different facts. A phone that
// registers against a deployment in silent mode will never hear anything, and
// the app should not have to work that out by noticing an absence over the
// following fortnight.
func TestSilentModeSaysSoOnRegistration(t *testing.T) {
	_, body := post(t, handlerFor(&deviceStore{}, constants.SignalModeSilent),
		`{"token":"`+aToken+`"}`)

	var mode, note string
	if err := json.Unmarshal(body["delivery_mode"], &mode); err != nil {
		t.Fatalf("decode delivery_mode: %v", err)
	}
	if err := json.Unmarshal(body["note"], &note); err != nil {
		t.Fatalf("decode note: %v", err)
	}

	if mode != constants.SignalModeSilent.String() {
		t.Errorf("delivery_mode = %q, want silent", mode)
	}
	if !strings.Contains(note, "silent mode") || !strings.Contains(note, "nothing is sent") {
		t.Errorf("the note does not say that nothing will be sent: %q", note)
	}
}

// TestNotifyModeWithARegistrationSaysAlertsWillArrive.
func TestNotifyModeWithARegistrationSaysAlertsWillArrive(t *testing.T) {
	_, body := post(t, handlerFor(&deviceStore{}, constants.SignalModeNotify),
		`{"token":"`+aToken+`"}`)

	var note string
	if err := json.Unmarshal(body["note"], &note); err != nil {
		t.Fatalf("decode note: %v", err)
	}
	if !strings.Contains(note, "will be delivered") {
		t.Errorf("note = %q, want it to say alerts will be delivered", note)
	}
}

// TestNothingRegisteredIsAnAnswerRatherThanAFourOhFour.
//
// The app asks this to tell "registered" from "the POST went nowhere". A 404
// would be ambiguous with the endpoint not existing on an older server, which
// is exactly the case the app is trying to rule out.
func TestNothingRegisteredIsAnAnswerRatherThanAFourOhFour(t *testing.T) {
	recorder := httptest.NewRecorder()
	handlerFor(&deviceStore{}, constants.SignalModeNotify).
		Device(recorder, httptest.NewRequest(http.MethodGet, "/api/v1/device", nil))

	if recorder.Code != http.StatusOK {
		t.Fatalf("status %d, want 200", recorder.Code)
	}

	body := decode(t, recorder)
	var registered bool
	if err := json.Unmarshal(body["registered"], &registered); err != nil {
		t.Fatalf("decode registered: %v", err)
	}
	if registered {
		t.Error("registered is true with nothing registered")
	}

	var note string
	if err := json.Unmarshal(body["note"], &note); err != nil {
		t.Fatalf("decode note: %v", err)
	}
	if !strings.Contains(note, "No device is registered") {
		t.Errorf("note = %q, want it to say no device is registered", note)
	}
}

// TestPlatformDefaultsToAndroid, which is the only one phase 09 builds for. An
// app that omits the field must not be refused.
func TestPlatformDefaultsToAndroid(t *testing.T) {
	store := &deviceStore{}
	post(t, handlerFor(store, constants.SignalModeNotify), `{"token":"`+aToken+`"}`)

	if len(store.registered) != 1 {
		t.Fatalf("registered %d devices, want 1", len(store.registered))
	}
	if got := store.registered[0].Platform; got != constants.DevicePlatformAndroid {
		t.Errorf("platform = %q, want android", got)
	}
}

// TestABadRequestIsFourHundredAndNotFiveHundred.
//
// The distinction decides whether the app retries. A malformed body is the
// app's bug and retrying repeats it; a 500 is the server's and is worth trying
// again.
func TestABadRequestIsFourHundredAndNotFiveHundred(t *testing.T) {
	for name, body := range map[string]string{
		"not json":           `{`,
		"no token":           `{"platform":"android"}`,
		"empty token":        `{"token":""}`,
		"unknown platform":   `{"token":"` + aToken + `","platform":"blackberry"}`,
		"token with a space": `{"token":"abc def"}`,
		"an oversized body":  `{"token":"` + strings.Repeat("a", 9000) + `"}`,
	} {
		t.Run(name, func(t *testing.T) {
			store := &deviceStore{}
			recorder, _ := post(t, handlerFor(store, constants.SignalModeNotify), body)

			if recorder.Code != http.StatusBadRequest {
				t.Fatalf("status %d, want 400: %s", recorder.Code, recorder.Body)
			}
			if len(store.registered) != 0 {
				t.Error("it was stored anyway")
			}

			var failure struct {
				Error struct {
					Code string `json:"code"`
				} `json:"error"`
			}
			if err := json.Unmarshal(recorder.Body.Bytes(), &failure); err != nil {
				t.Fatalf("decode: %v", err)
			}
			if failure.Error.Code != string(constants.APIErrInvalidParameter) {
				t.Errorf("code = %q, want %q", failure.Error.Code, constants.APIErrInvalidParameter)
			}
		})
	}
}

// TestARejectedPlatformNamesWhatWasSent.
//
// Both the handler and the usecase refuse an unknown platform, so the status
// is 400 either way and the layering is not what this pins. What it pins is
// the message: the handler has the value the caller actually sent and the
// usecase does not, so letting the check fall through to the second one
// answers "" is not a device platform — which tells somebody debugging their
// app nothing about what they sent.
func TestARejectedPlatformNamesWhatWasSent(t *testing.T) {
	recorder, _ := post(t, handlerFor(&deviceStore{}, constants.SignalModeNotify),
		`{"token":"`+aToken+`","platform":"blackberry"}`)

	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status %d, want 400", recorder.Code)
	}
	if !strings.Contains(recorder.Body.String(), "blackberry") {
		t.Errorf("the error does not name the platform that was sent:\n%s", recorder.Body)
	}
	if !strings.Contains(recorder.Body.String(), "android") {
		t.Errorf("the error does not list the platforms that would work:\n%s", recorder.Body)
	}
}

// TestAnOversizedBodyIsRefusedRatherThanRead.
//
// The token has its own length bound in the usecase, so a huge *token* is
// refused whether or not the body is capped. What the cap is for is everything
// else: a body that is mostly label, or mostly fields nothing reads, would
// otherwise be read into memory in full and then quietly truncated into a
// successful registration.
//
// The label here is well over the body cap and the token is ordinary, so this
// fails only if the body is bounded before it is parsed.
func TestAnOversizedBodyIsRefusedRatherThanRead(t *testing.T) {
	store := &deviceStore{}
	recorder, _ := post(t, handlerFor(store, constants.SignalModeNotify),
		`{"token":"`+aToken+`","label":"`+strings.Repeat("x", 9000)+`"}`)

	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status %d, want 400: a 9KB body was accepted", recorder.Code)
	}
	if len(store.registered) != 0 {
		t.Error("the oversized body was registered anyway")
	}
	if !strings.Contains(recorder.Body.String(), "too large") {
		t.Errorf("the error does not say the body was too large:\n%s", recorder.Body)
	}
}

// TestAStoreFailureIsFiveHundredAndSaysNothingElse.
func TestAStoreFailureIsFiveHundredAndSaysNothingElse(t *testing.T) {
	store := &deviceStore{err: context.DeadlineExceeded}
	recorder, _ := post(t, handlerFor(store, constants.SignalModeNotify), `{"token":"`+aToken+`"}`)

	if recorder.Code != http.StatusInternalServerError {
		t.Fatalf("status %d, want 500", recorder.Code)
	}
	if strings.Contains(recorder.Body.String(), "context deadline") {
		t.Errorf("the internal error leaked:\n%s", recorder.Body)
	}
}

// TestForgettingNothingIsNotAnError. The caller asked for a state that already
// held, which is a success with removed=false rather than a 404.
func TestForgettingNothingIsNotAnError(t *testing.T) {
	recorder := httptest.NewRecorder()
	handlerFor(&deviceStore{}, constants.SignalModeNotify).
		ForgetDevice(recorder, httptest.NewRequest(http.MethodDelete, "/api/v1/device", nil))

	if recorder.Code != http.StatusOK {
		t.Fatalf("status %d, want 200", recorder.Code)
	}

	var removed bool
	if err := json.Unmarshal(decode(t, recorder)["removed"], &removed); err != nil {
		t.Fatalf("decode removed: %v", err)
	}
	if removed {
		t.Error("removed is true with nothing registered")
	}
}
