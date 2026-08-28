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

const (
	anEndpoint = "https://web.push.apple.com/QCyaSPqNfMVEP0vJqSk6abcdefghijklmnop0123456789"
	aP256dh    = "BFtx1cJ8xVQ7Zo3PZ5Vv0qKQpXqZ5RmH8t3wQ2sK9LmN4pR7yTvW1xYzA2bC3dE4fG5hI6jK7lM8nO9pQ0rS1tU"
	anAuth     = "kZ8xQvN3mLp7RtY2wS5dFg"

	// aVAPIDKey is what the app subscribes against. Public, and served.
	aVAPIDKey = "BDLFBrIHg9mGNteU0m9p-FKeovhMbMUR4dBwQf3kd1P7LtzaQ4qtDFr66"
)

// aSubscription is the body a browser's PushSubscription.toJSON() produces.
func aSubscription() string {
	return `{"endpoint":"` + anEndpoint + `","keys":{"p256dh":"` + aP256dh +
		`","auth":"` + anAuth + `"}}`
}

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
	return _notify_handler.NewDeviceHandlerImpl(
		mustDeviceUsecase(nil, store), quiet(), mode, aVAPIDKey)
}

// mustDeviceUsecase layers the real usecase over the fake store, so a handler
// test cannot accept a registration production would refuse.
func mustDeviceUsecase(_ *testing.T, store *deviceStore) notify.DeviceUsecase {
	usecase, err := _notify_us.NewDeviceUsecaseImpl(store, quiet())
	if err != nil {
		panic(err)
	}
	return usecase
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

// carriesNothingSecret fails if a response body contains any part of the
// registered subscription.
func carriesNothingSecret(t *testing.T, what, body string) {
	t.Helper()
	for name, secret := range map[string]string{
		"endpoint": anEndpoint, "p256dh": aP256dh, "auth": anAuth,
	} {
		if strings.Contains(body, secret) {
			t.Errorf("%s carries the %s:\n%s", what, name, body)
		}
	}
}

// TestTheSubscriptionIsNeverEchoedBack.
//
// # What this prevents
//
// There is no authentication in front of this endpoint — the network is the
// boundary (ADR 0024). The subscription is the one thing in this
// system that lets anything push to the owner's phone, and an endpoint that
// returned it would hand it to anything that could reach the port. The app
// already has it; nothing needs it back.
func TestTheSubscriptionIsNeverEchoedBack(t *testing.T) {
	store := &deviceStore{}
	h := handlerFor(store, constants.SignalModeNotify)

	recorder, _ := post(t, h,
		`{"endpoint":"`+anEndpoint+`","keys":{"p256dh":"`+aP256dh+`","auth":"`+anAuth+
			`"},"platform":"web","label":"iPhone 14"}`)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status %d, want 200: %s", recorder.Code, recorder.Body)
	}
	carriesNothingSecret(t, "the registration response", recorder.Body.String())

	// And the same on the read and the delete.
	read := httptest.NewRecorder()
	h.Device(read, httptest.NewRequest(http.MethodGet, "/api/v1/device", nil))
	carriesNothingSecret(t, "GET", read.Body.String())

	removed := httptest.NewRecorder()
	h.ForgetDevice(removed, httptest.NewRequest(http.MethodDelete, "/api/v1/device", nil))
	carriesNothingSecret(t, "DELETE", removed.Body.String())
}

// TestSilentModeSaysSoOnRegistration.
//
// Registering and being delivered to are different facts. A phone that
// registers against a deployment in silent mode will never hear anything, and
// the app should not have to work that out by noticing an absence over the
// following fortnight.
func TestSilentModeSaysSoOnRegistration(t *testing.T) {
	_, body := post(t, handlerFor(&deviceStore{}, constants.SignalModeSilent),
		aSubscription())

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
		aSubscription())

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

// TestPlatformDefaultsToWeb, which is what an installed PWA is and the only
// thing this deployment produces. An app that omits the field must not be
// refused — the browser's PushSubscription.toJSON() does not include one, and
// posting exactly what the browser handed over is the point.
func TestPlatformDefaultsToWeb(t *testing.T) {
	store := &deviceStore{}
	post(t, handlerFor(store, constants.SignalModeNotify), aSubscription())

	if len(store.registered) != 1 {
		t.Fatalf("registered %d devices, want 1", len(store.registered))
	}
	if got := store.registered[0].Platform; got != constants.DevicePlatformWeb {
		t.Errorf("platform = %q, want web", got)
	}
}

/*
TestTheVAPIDKeyIsServedEvenWithNothingRegistered.

# What this prevents

The app cannot subscribe without the public key, and it cannot register before
it has subscribed. So the one call it makes first — GET, with nothing
registered yet — is the call that has to carry it. Serving it only alongside an
existing registration would be a loop with no way in: no key, so no
subscription, so no registration, so no key.

It is served rather than built into the app so that rotating the pair does not
need a rebuild.
*/
func TestTheVAPIDKeyIsServedEvenWithNothingRegistered(t *testing.T) {
	h := handlerFor(&deviceStore{}, constants.SignalModeNotify)

	read := httptest.NewRecorder()
	h.Device(read, httptest.NewRequest(http.MethodGet, "/api/v1/device", nil))

	var body map[string]json.RawMessage
	if err := json.Unmarshal(read.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}

	var key string
	if err := json.Unmarshal(body["vapid_public_key"], &key); err != nil {
		t.Fatalf("nothing to subscribe against on an unregistered GET: %v\n%s", err, read.Body)
	}
	if key != aVAPIDKey {
		t.Errorf("vapid_public_key = %q, want the configured key", key)
	}
}

/*
TestSilentModeServesNoKeyToSubscribeWith.

A deployment that sends nothing has no VAPID pair configured, and the empty
field is the honest answer: there is nothing to subscribe to here. The app
reads that rather than subscribing against an empty string and getting an error
from the browser about an invalid application server key.
*/
func TestSilentModeServesNoKeyToSubscribeWith(t *testing.T) {
	h := _notify_handler.NewDeviceHandlerImpl(
		mustDeviceUsecase(t, &deviceStore{}), quiet(), constants.SignalModeSilent, "")

	read := httptest.NewRecorder()
	h.Device(read, httptest.NewRequest(http.MethodGet, "/api/v1/device", nil))

	if strings.Contains(read.Body.String(), "vapid_public_key") {
		t.Errorf("silent mode offered a key to subscribe with:\n%s", read.Body)
	}
}

// TestABadRequestIsFourHundredAndNotFiveHundred.
//
// The distinction decides whether the app retries. A malformed body is the
// app's bug and retrying repeats it; a 500 is the server's and is worth trying
// again.
func TestABadRequestIsFourHundredAndNotFiveHundred(t *testing.T) {
	for name, body := range map[string]string{
		"not json":              `{`,
		"no subscription":       `{"platform":"web"}`,
		"empty endpoint":        `{"endpoint":"","keys":{"p256dh":"k","auth":"a"}}`,
		"no keys":               `{"endpoint":"` + anEndpoint + `"}`,
		"only one key":          `{"endpoint":"` + anEndpoint + `","keys":{"p256dh":"k"}}`,
		"unknown platform":      `{"endpoint":"` + anEndpoint + `","keys":{"p256dh":"k","auth":"a"},"platform":"blackberry"}`,
		"endpoint with a space": `{"endpoint":"https://push/a b","keys":{"p256dh":"k","auth":"a"}}`,
		"a plain http endpoint": `{"endpoint":"http://web.push.apple.com/Q","keys":{"p256dh":"k","auth":"a"}}`,
		"an oversized body":     `{"endpoint":"` + strings.Repeat("a", 9000) + `"}`,
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
		`{"endpoint":"`+anEndpoint+`","keys":{"p256dh":"`+aP256dh+`","auth":"`+anAuth+
			`"},"platform":"blackberry"}`)

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
// The endpoint has its own length bound in the usecase, so a huge *endpoint* is
// refused whether or not the body is capped. What the cap is for is everything
// else: a body that is mostly label, or mostly fields nothing reads, would
// otherwise be read into memory in full and then quietly truncated into a
// successful registration.
//
// The label here is well over the body cap and the subscription is ordinary, so this
// fails only if the body is bounded before it is parsed.
func TestAnOversizedBodyIsRefusedRatherThanRead(t *testing.T) {
	store := &deviceStore{}
	recorder, _ := post(t, handlerFor(store, constants.SignalModeNotify),
		`{"endpoint":"`+anEndpoint+`","keys":{"p256dh":"`+aP256dh+`","auth":"`+anAuth+
			`"},"label":"`+strings.Repeat("x", 9000)+`"}`)

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
	recorder, _ := post(t, handlerFor(store, constants.SignalModeNotify), aSubscription())

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
