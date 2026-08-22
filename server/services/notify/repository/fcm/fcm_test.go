package fcm_test

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/spioneracorei8/btcusd-trading-platform/server/constants"
	"github.com/spioneracorei8/btcusd-trading-platform/server/services/notify"
	"github.com/spioneracorei8/btcusd-trading-platform/server/services/notify/repository/fcm"
)

// aMessage is one signal's worth of payload.
func aMessage() notify.Message {
	return notify.Message{
		Token: "device-token",
		Title: "BTCUSDT 4h LONG",
		Body:  "ref 64123.45 · stop 63900 · target 64600 — fast crossed above slow",
		Data:  map[string]string{"signal_id": "b6b7f0f4-0000-4000-8000-000000000001"},
	}
}

// senderTo builds a client pointed at a test server, with the OAuth2 exchange
// bypassed — a service account key is a private key, and inventing one to
// test an HTTP call would be testing Google's library rather than this code.
func senderTo(t *testing.T, server *httptest.Server) notify.Sender {
	t.Helper()

	sender, err := fcm.NewSenderImpl(context.Background(), fcm.Config{
		ProjectId:  "btc-signals",
		Endpoint:   server.URL,
		HTTPClient: server.Client(),
		Timeout:    5 * time.Second,
	})
	if err != nil {
		t.Fatalf("NewSenderImpl() returned error: %v", err)
	}
	return sender
}

// TestASuccessfulSendPostsTheMessageWhereFirebaseExpectsIt.
func TestASuccessfulSendPostsTheMessageWhereFirebaseExpectsIt(t *testing.T) {
	var (
		gotPath   string
		gotMethod string
		gotType   string
		body      map[string]any
	)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotMethod = r.URL.Path, r.Method
		gotType = r.Header.Get("Content-Type")
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("the request body is not JSON: %v", err)
		}
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `{"name":"projects/btc-signals/messages/1"}`)
	}))
	defer server.Close()

	if err := senderTo(t, server).Send(context.Background(), aMessage()); err != nil {
		t.Fatalf("Send() returned error: %v", err)
	}

	if want := "/v1/projects/btc-signals/messages:send"; gotPath != want {
		t.Errorf("posted to %q, want %q", gotPath, want)
	}
	if gotMethod != http.MethodPost {
		t.Errorf("method = %q, want POST", gotMethod)
	}
	if !strings.HasPrefix(gotType, "application/json") {
		t.Errorf("Content-Type = %q, want application/json", gotType)
	}

	message, ok := body["message"].(map[string]any)
	if !ok {
		t.Fatalf("the body has no message object: %v", body)
	}
	if message["token"] != "device-token" {
		t.Errorf("token = %v, want the device token", message["token"])
	}

	notification, ok := message["notification"].(map[string]any)
	if !ok {
		t.Fatalf("the message has no notification object: %v", message)
	}
	if notification["title"] != aMessage().Title || notification["body"] != aMessage().Body {
		t.Errorf("notification = %v, want the title and body it was given", notification)
	}

	data, ok := message["data"].(map[string]any)
	if !ok || data["signal_id"] != aMessage().Data["signal_id"] {
		t.Errorf("data = %v, want the signal id carried through", message["data"])
	}
}

// TestTheMessageIsSentAtHighPriority.
//
// A scalping signal that arrives when the phone next wakes is not a signal;
// it is a note about something that already happened.
func TestTheMessageIsSentAtHighPriority(t *testing.T) {
	var body map[string]any

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&body)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	if err := senderTo(t, server).Send(context.Background(), aMessage()); err != nil {
		t.Fatalf("Send() returned error: %v", err)
	}

	message, _ := body["message"].(map[string]any)
	android, ok := message["android"].(map[string]any)
	if !ok || android["priority"] != "high" {
		t.Errorf("android = %v, want high priority", message["android"])
	}

	apns, ok := message["apns"].(map[string]any)
	if !ok {
		t.Fatalf("the message carries no apns block: %v", message)
	}
	headers, ok := apns["headers"].(map[string]any)
	if !ok || headers["apns-priority"] != "10" {
		t.Errorf("apns headers = %v, want apns-priority 10", apns["headers"])
	}
}

// TestRejectionsAreSplitIntoPermanentAndTransient.
//
// The split decides whether the attempt budget is spent. A dead device token
// burning five tries delays every alert behind it and then records a reason
// that reads like a network problem.
func TestRejectionsAreSplitIntoPermanentAndTransient(t *testing.T) {
	tests := []struct {
		name      string
		status    int
		body      string
		permanent bool
	}{
		{name: "token uninstalled", status: http.StatusNotFound,
			body: `{"error":{"status":"UNREGISTERED","message":"token not registered"}}`, permanent: true},
		{name: "malformed payload", status: http.StatusBadRequest,
			body: `{"error":{"status":"INVALID_ARGUMENT","message":"bad token"}}`, permanent: true},
		{name: "credentials rejected", status: http.StatusUnauthorized, permanent: true},
		{name: "project forbidden", status: http.StatusForbidden, permanent: true},

		{name: "rate limited", status: http.StatusTooManyRequests},
		{name: "request timeout", status: http.StatusRequestTimeout},
		{name: "firebase unavailable", status: http.StatusServiceUnavailable},
		{name: "firebase broken", status: http.StatusInternalServerError},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tt.status)
				fmt.Fprint(w, tt.body)
			}))
			defer server.Close()

			err := senderTo(t, server).Send(context.Background(), aMessage())
			if err == nil {
				t.Fatalf("a %d response returned no error", tt.status)
			}
			if got := errors.Is(err, notify.ErrUndeliverable); got != tt.permanent {
				t.Errorf("%d treated as permanent = %v, want %v (%v)",
					tt.status, got, tt.permanent, err)
			}
			if !strings.Contains(err.Error(), fmt.Sprint(tt.status)) {
				t.Errorf("the error %q does not name the status", err)
			}
		})
	}
}

// TestTheRejectionReasonIsCarriedIntoTheError, because it lands in
// notifications.last_error and is the only explanation anybody gets.
func TestTheRejectionReasonIsCarriedIntoTheError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		fmt.Fprint(w, `{"error":{"status":"UNREGISTERED","message":"the token is not registered"}}`)
	}))
	defer server.Close()

	err := senderTo(t, server).Send(context.Background(), aMessage())
	if err == nil {
		t.Fatal("Send() returned no error")
	}
	for _, want := range []string{"UNREGISTERED", "not registered"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the error %q does not carry %q", err, want)
		}
	}
}

// TestAHugeRejectionBodyIsBounded.
//
// last_error is read by a person. An unbounded response would put an
// arbitrary amount of somebody else's text into a column meant to explain a
// silence.
func TestAHugeRejectionBodyIsBounded(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		fmt.Fprint(w, strings.Repeat("x", 100_000))
	}))
	defer server.Close()

	err := senderTo(t, server).Send(context.Background(), aMessage())
	if err == nil {
		t.Fatal("Send() returned no error")
	}
	if len(err.Error()) > constants.NotifyErrorBodyLimit+256 {
		t.Errorf("the error is %d bytes; it is stored and read by a person", len(err.Error()))
	}
}

// TestAMessageWithNoTokenIsRefusedWithoutAskingFirebase.
func TestAMessageWithNoTokenIsRefusedWithoutAskingFirebase(t *testing.T) {
	called := false
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		called = true
	}))
	defer server.Close()

	message := aMessage()
	message.Token = ""

	err := senderTo(t, server).Send(context.Background(), message)
	if !errors.Is(err, notify.ErrUndeliverable) {
		t.Errorf("Send() with no token returned %v, want an undeliverable error", err)
	}
	if called {
		t.Error("a message with no destination was sent to Firebase anyway")
	}
}

// TestTheChannelIsTheOneTheQueueRecords, so a queued row can be matched to
// the sender that owns it.
func TestTheChannelIsTheOneTheQueueRecords(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	defer server.Close()

	if got := senderTo(t, server).Channel(); got != constants.NotificationChannelFCM {
		t.Errorf("Channel() = %q, want fcm", got)
	}
}

// serviceAccountFile writes a real, parseable service account key.
//
// Generated rather than checked in: the file's whole point is that it holds a
// private key, and a fixture one in the repository is a fixture one in every
// clone of the repository.
func serviceAccountFile(t *testing.T, projectId string) string {
	t.Helper()

	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	der, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatalf("marshal key: %v", err)
	}
	pemKey := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})

	account, err := json.Marshal(map[string]string{
		"type":         "service_account",
		"project_id":   projectId,
		"client_email": "collector@" + projectId + ".iam.gserviceaccount.com",
		"private_key":  string(pemKey),
		"token_uri":    "https://oauth2.googleapis.com/token",
	})
	if err != nil {
		t.Fatalf("marshal account: %v", err)
	}

	path := filepath.Join(t.TempDir(), "fcm-service-account.json")
	if err := os.WriteFile(path, account, 0o600); err != nil {
		t.Fatalf("write account: %v", err)
	}
	return path
}

// TestAValidServiceAccountIsAccepted, without a network call.
//
// Validating the key must not need Google to be reachable: the collector's
// other job is storing candles, and a brief outage at Google must not stop it
// from starting.
func TestAValidServiceAccountIsAccepted(t *testing.T) {
	sender, err := fcm.NewSenderImpl(context.Background(), fcm.Config{
		ProjectId:       "btc-signals",
		CredentialsFile: serviceAccountFile(t, "btc-signals"),
	})
	if err != nil {
		t.Fatalf("a valid service account was refused: %v", err)
	}
	if sender == nil {
		t.Fatal("NewSenderImpl() returned no sender and no error")
	}
}

// TestAnUnusableCredentialsFileIsRefusedAtStartUp.
//
// Neither of the oauth2 library's parsers validates past JSON syntax — both
// accept a service account with no key in it and fail at the first token
// exchange. For this system that exchange is the first signal, days later,
// and a silent delivery path looks exactly like a strategy that has found
// nothing.
func TestAnUnusableCredentialsFileIsRefusedAtStartUp(t *testing.T) {
	valid := serviceAccountFile(t, "btc-signals")

	write := func(t *testing.T, content string) string {
		t.Helper()
		path := filepath.Join(t.TempDir(), "key.json")
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatalf("write fixture: %v", err)
		}
		return path
	}

	tests := map[string]string{
		"no file configured": "",
		"missing file":       filepath.Join(t.TempDir(), "absent.json"),
		"not json":           write(t, "nope"),
		"a stub":             write(t, `{"type":"service_account"}`),
		"no private key":     write(t, `{"type":"service_account","project_id":"p","client_email":"a@b.c"}`),
		"private key is not PEM": write(t,
			`{"type":"service_account","project_id":"p","client_email":"a@b.c","private_key":"not-a-key"}`),
		"private key will not parse": write(t, `{"type":"service_account","project_id":"p",`+
			`"client_email":"a@b.c","private_key":"-----BEGIN PRIVATE KEY-----\nAAAA\n-----END PRIVATE KEY-----\n"}`),
		"a user credential": write(t,
			`{"type":"authorized_user","client_id":"x","client_secret":"y","refresh_token":"z"}`),
	}

	for name, path := range tests {
		t.Run(name, func(t *testing.T) {
			_, err := fcm.NewSenderImpl(context.Background(), fcm.Config{
				ProjectId: "btc-signals", CredentialsFile: path,
			})
			if err == nil {
				t.Fatal("it was accepted")
			}
		})
	}

	// The control: the same call with a real key succeeds, so the cases above
	// are failing for their own reason and not because everything fails.
	if _, err := fcm.NewSenderImpl(context.Background(), fcm.Config{
		ProjectId: "btc-signals", CredentialsFile: valid,
	}); err != nil {
		t.Errorf("the control case failed: %v", err)
	}
}

// TestAKeyForAnotherProjectIsFlaggedAndNotRefused.
//
// Firebase can grant a service account cross-project access, so this is
// unusual rather than impossible. It is said loudly because the alternative
// is a 403 on the first signal that reads like a Firebase problem.
func TestAKeyForAnotherProjectIsFlaggedAndNotRefused(t *testing.T) {
	var logged strings.Builder
	log := slog.New(slog.NewTextHandler(&logged, &slog.HandlerOptions{Level: slog.LevelWarn}))

	_, err := fcm.NewSenderImpl(context.Background(), fcm.Config{
		ProjectId:       "btc-signals",
		CredentialsFile: serviceAccountFile(t, "some-other-project"),
		Log:             log,
	})
	if err != nil {
		t.Fatalf("a cross-project key was refused: %v", err)
	}

	for _, want := range []string{"some-other-project", "btc-signals"} {
		if !strings.Contains(logged.String(), want) {
			t.Errorf("the warning does not name %s: %s", want, logged.String())
		}
	}
}

// TestTheCredentialsAreNeverQuotedInAnError.
//
// The file is a private key. An error that echoed it would put it in the logs
// and everywhere the logs are shipped to.
func TestTheCredentialsAreNeverQuotedInAnError(t *testing.T) {
	const secret = "UNMISTAKABLE-PRIVATE-MATERIAL"

	path := filepath.Join(t.TempDir(), "key.json")
	key := fmt.Sprintf(
		`{"type":"service_account","project_id":"p","client_email":"a@b.c","private_key":%q}`,
		"-----BEGIN PRIVATE KEY-----\n"+secret+"\n-----END PRIVATE KEY-----\n")
	if err := os.WriteFile(path, []byte(key), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	_, err := fcm.NewSenderImpl(context.Background(), fcm.Config{
		ProjectId: "btc-signals", CredentialsFile: path,
	})
	if err == nil {
		t.Fatal("a key that will not parse was accepted")
	}
	for _, leaked := range []string{secret, "BEGIN PRIVATE KEY"} {
		if strings.Contains(err.Error(), leaked) {
			t.Errorf("the error quotes the key material: %v", err)
		}
	}
}

// TestNoProjectIdIsRefused, since the send URL is built from it.
func TestNoProjectIdIsRefused(t *testing.T) {
	if _, err := fcm.NewSenderImpl(context.Background(), fcm.Config{}); err == nil {
		t.Error("a sender with no project id was accepted")
	}
}
