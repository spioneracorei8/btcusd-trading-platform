package webpush_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"testing"

	"github.com/spioneracorei8/btcusd-trading-platform/server/constants"
	"github.com/spioneracorei8/btcusd-trading-platform/server/models"
	"github.com/spioneracorei8/btcusd-trading-platform/server/services/notify"
	_webpush "github.com/spioneracorei8/btcusd-trading-platform/server/services/notify/repository/webpush"
)

// A real VAPID pair, generated once. The library decodes both, so a made-up
// value would fail construction for the wrong reason.
const (
	publicKey  = "BDLFBrIHg9mGNteU0m9p-FKeovhMbMUR4dBwQf3kd1P7LtzaQ4qtDFr66_2fG2835RU7WcSSOSv5lwdTKjWFl1g"
	privateKey = "3QJb7AN5amxyn9y5Km0vBZli05ioT1qkLh3qqwRFFRk"
)

// A subscription with real keys, because the payload is genuinely encrypted
// against them before any of this reaches a fake transport.
func subscription() models.PushSubscription {
	return models.PushSubscription{
		Endpoint: "https://web.push.apple.com/QCyaSPqNfMVEP0vJqSk6abcdefghij0123456789",
		// A real P-256 point and a real 16-byte secret. A made-up value fails
		// inside the encryption, which would make every case below fail for a
		// reason that has nothing to do with what it is testing.
		P256dh: "BHsyaeZ2K9xnLEUVeeHVSge06_JSjrE4x_tFcKSm99czt7yWBGhFbswhvSv9Rrxqw0p0KSl0XYkVYPVWllVGHw4",
		Auth:   "Sq0g3ecLTCzvxiGcpV19kw",
	}
}

func quiet() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// answering is an HTTP client that returns one canned response and records
// what it was asked to send.
type answering struct {
	status int
	body   string

	requests []*http.Request
}

func (a *answering) Do(r *http.Request) (*http.Response, error) {
	a.requests = append(a.requests, r)
	return &http.Response{
		StatusCode: a.status,
		Body:       io.NopCloser(strings.NewReader(a.body)),
		Header:     http.Header{},
	}, nil
}

func senderAnswering(t *testing.T, status int, body string) (notify.Sender, *answering) {
	t.Helper()
	client := &answering{status: status, body: body}

	sender, err := _webpush.NewSenderImpl(_webpush.Options{
		PublicKey:  publicKey,
		PrivateKey: privateKey,
		Subject:    "mailto:owner@example.com",
		Client:     client,
		Logger:     quiet(),
	})
	if err != nil {
		t.Fatalf("NewSenderImpl() returned error: %v", err)
	}
	return sender, client
}

func message() notify.Message {
	return notify.Message{
		To:    subscription(),
		Title: "BTCUSDT 4h LONG",
		Body:  "ref 30,200 · stop 29,900 · target 30,500",
		Data:  map[string]string{"signal_id": "3f2504e0-4f89-11d3-9a0c-0305e82c3301"},
	}
}

/*
TestAGoneSubscriptionIsPermanentAndSaysWhatToDo.

# What this prevents

404 and 410 are the push service saying this subscription no longer exists — an
uninstall, cleared site data, or an expiry. It is the single most likely
delivery failure this system will ever see, and it is the one the whole
re-registration mechanism exists for.

Two things have to be right. Retrying must stop, or five attempts and eight
minutes of backoff are spent on something that cannot succeed, delaying every
alert queued behind it. And the reason stored in notifications.last_error must
say to open the app — because the default reading of a failed delivery is a
network problem, and that sends whoever investigates to the wrong place
entirely.
*/
func TestAGoneSubscriptionIsPermanentAndSaysWhatToDo(t *testing.T) {
	for _, status := range []int{http.StatusNotFound, http.StatusGone} {
		sender, _ := senderAnswering(t, status, "")

		err := sender.Send(context.Background(), message())
		if err == nil {
			t.Fatalf("%d was reported as delivered", status)
		}
		if !errors.Is(err, notify.ErrUndeliverable) {
			t.Errorf("%d is retried; it cannot succeed: %v", status, err)
		}
		if !strings.Contains(err.Error(), "open the app") {
			t.Errorf("%d does not say what to do about it: %v", status, err)
		}
	}
}

/*
TestARateLimitIsRetriedRatherThanAbandoned.

The mirror of the case above, and the one that costs a real alert if it is got
wrong. 429 and 408 are the push service saying "not now", which is exactly what
the retry budget is for. Treating them as permanent would drop a signal because
the service was briefly busy.
*/
func TestARateLimitIsRetriedRatherThanAbandoned(t *testing.T) {
	for _, status := range []int{http.StatusTooManyRequests, http.StatusRequestTimeout} {
		sender, _ := senderAnswering(t, status, "")

		err := sender.Send(context.Background(), message())
		if err == nil {
			t.Fatalf("%d was reported as delivered", status)
		}
		if errors.Is(err, notify.ErrUndeliverable) {
			t.Errorf("%d was given up on; it is worth another attempt: %v", status, err)
		}
	}
}

/*
TestAServerErrorIsRetried.

5xx is the push service's own problem, and it will very likely be over by the
next attempt.
*/
func TestAServerErrorIsRetried(t *testing.T) {
	sender, _ := senderAnswering(t, http.StatusInternalServerError, "")

	err := sender.Send(context.Background(), message())
	if errors.Is(err, notify.ErrUndeliverable) {
		t.Errorf("a 500 from the push service was given up on: %v", err)
	}
}

/*
TestAnOversizedPayloadIsPermanent.

413 will be 413 again with the same payload. Retrying it spends the budget on
an outcome that cannot change, and then records a reason that reads like a
network problem.
*/
func TestAnOversizedPayloadIsPermanent(t *testing.T) {
	sender, _ := senderAnswering(t, http.StatusRequestEntityTooLarge, "")

	err := sender.Send(context.Background(), message())
	if !errors.Is(err, notify.ErrUndeliverable) {
		t.Errorf("a 413 is retried; the payload will be the same size next time: %v", err)
	}
}

/*
TestAnErrorNeverQuotesTheSubscription.

notifications.last_error is shown to a person, stored indefinitely, and copied
into issues. The subscription is what lets anything push to the owner's phone,
and the push service's own error body may well echo the endpoint back.
*/
func TestAnErrorNeverQuotesTheSubscription(t *testing.T) {
	to := subscription()
	// The push service quoting the endpoint back at us is exactly the case
	// that would leak it if the body were used unfiltered.
	sender, _ := senderAnswering(t, http.StatusGone,
		`{"reason":"subscription `+to.Endpoint+` is gone"}`)

	err := sender.Send(context.Background(), message())
	if err == nil {
		t.Fatal("no error")
	}
	if strings.Contains(err.Error(), to.P256dh) || strings.Contains(err.Error(), to.Auth) {
		t.Errorf("the error carries a subscription key: %v", err)
	}
}

/*
TestAnUnusableSubscriptionIsRefusedWithoutASend.

A subscription that cannot be delivered to should not consume an attempt or a
round trip. It also should not reach the encryption, which fails with a message
about elliptic curves — a long way from what actually happened.
*/
func TestAnUnusableSubscriptionIsRefusedWithoutASend(t *testing.T) {
	for name, to := range map[string]models.PushSubscription{
		"no endpoint":    {P256dh: "k", Auth: "a"},
		"no key":         {Endpoint: "https://web.push.apple.com/Q", Auth: "a"},
		"no auth":        {Endpoint: "https://web.push.apple.com/Q", P256dh: "k"},
		"plain http":     {Endpoint: "http://web.push.apple.com/Q", P256dh: "k", Auth: "a"},
		"not a url":      {Endpoint: "nonsense", P256dh: "k", Auth: "a"},
		"empty entirely": {},
	} {
		t.Run(name, func(t *testing.T) {
			sender, client := senderAnswering(t, http.StatusCreated, "")

			m := message()
			m.To = to

			err := sender.Send(context.Background(), m)
			if !errors.Is(err, notify.ErrUndeliverable) {
				t.Errorf("error = %v, want undeliverable", err)
			}
			if len(client.requests) != 0 {
				t.Errorf("it was sent anyway (%d requests)", len(client.requests))
			}
		})
	}
}

/*
TestTheChannelIsWebPush.

The queue row records which sender owns it, and the usecase takes that from
here rather than assuming. A mismatch would write rows nothing picks up.
*/
func TestTheChannelIsWebPush(t *testing.T) {
	sender, _ := senderAnswering(t, http.StatusCreated, "")

	if got := sender.Channel(); got != constants.NotificationChannelWebPush {
		t.Errorf("Channel() = %q, want webpush", got)
	}
}

/*
TestAMalformedVAPIDPairRefusesToStart.

The library decodes the keys lazily, inside the send path. Without a check at
construction a bad key runs fine for a week and then fails the first alert with
an ASN.1 parse error — days after the deploy that caused it, and looking like
the strategy having gone quiet.
*/
func TestAMalformedVAPIDPairRefusesToStart(t *testing.T) {
	for name, options := range map[string]_webpush.Options{
		"no public key":  {PrivateKey: privateKey, Subject: "mailto:a@b.c"},
		"no private key": {PublicKey: publicKey, Subject: "mailto:a@b.c"},
		"no subject":     {PublicKey: publicKey, PrivateKey: privateKey},
		"a subject that is not a URL": {
			PublicKey: publicKey, PrivateKey: privateKey, Subject: "owner@example.com",
		},
		"a private key that is not base64url": {
			PublicKey: publicKey, PrivateKey: "not a key", Subject: "mailto:a@b.c",
		},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := _webpush.NewSenderImpl(options); err == nil {
				t.Error("it was accepted")
			}
		})
	}
}

/*
TestTheEncryptedPayloadCarriesTheSignal.

The service worker reads title, body and data.signal_id — the last is what a
notification click navigates to. This decodes what actually went on the wire,
rather than trusting that the wire type and the worker agree.
*/
func TestTheEncryptedPayloadCarriesTheSignal(t *testing.T) {
	sender, client := senderAnswering(t, http.StatusCreated, "")
	if err := sender.Send(context.Background(), message()); err != nil {
		t.Fatalf("Send() returned error: %v", err)
	}
	if len(client.requests) != 1 {
		t.Fatalf("%d requests, want 1", len(client.requests))
	}

	request := client.requests[0]
	if request.URL.String() != subscription().Endpoint {
		t.Errorf("posted to %q, want the subscription endpoint", request.URL)
	}
	// aes128gcm is the RFC 8291 content encoding. Its absence would mean the
	// payload went out in a form no browser will decrypt.
	if got := request.Header.Get("Content-Encoding"); got != "aes128gcm" {
		t.Errorf("Content-Encoding = %q, want aes128gcm", got)
	}
	if got := request.Header.Get("Authorization"); !strings.HasPrefix(got, "vapid") {
		t.Errorf("Authorization = %q, want a VAPID signature", got)
	}
	if request.Header.Get("TTL") == "" {
		t.Error("no TTL; the push service would use its own idea of how long to hold this")
	}

	// The body is ciphertext, so the readable check is that it is not the
	// plaintext: a payload that went out unencrypted would be readable here.
	raw, err := io.ReadAll(request.Body)
	if err != nil {
		t.Fatalf("read the body: %v", err)
	}
	if strings.Contains(string(raw), "BTCUSDT") {
		t.Error("the payload went out unencrypted")
	}
	if strings.Contains(string(raw), "3f2504e0") {
		t.Error("the signal id went out unencrypted")
	}
	if len(raw) == 0 {
		t.Error("nothing was sent")
	}
}

/*
TestAMismatchedVAPIDPairIsRefused.

# What this prevents

Two keys that are each well-formed but not a pair. Every push is then signed
with a key the browser did not subscribe against, the push service answers 403,
and the delivery worker records a 4xx as permanent — so alerts stop, on the
first signal, with a reason that reads like a permissions problem.

Nothing else in the system can notice this. The keys decode, the JWT signs, the
request is well-formed; only the push service knows, and only at send time,
which is days after the deploy that caused it.

It is a plausible mistake rather than an exotic one: two `make vapid-keys` runs
and one line copied from each.
*/
func TestAMismatchedVAPIDPairIsRefused(t *testing.T) {
	// A valid public key from a different pair.
	const otherPublic = "BHsyaeZ2K9xnLEUVeeHVSge06_JSjrE4x_tFcKSm99czt7yWBGhFbswhvSv9Rrxqw0p0KSl0XYkVYPVWllVGHw4"

	_, err := _webpush.NewSenderImpl(_webpush.Options{
		PublicKey:  otherPublic,
		PrivateKey: privateKey,
		Subject:    "mailto:owner@example.com",
	})
	if err == nil {
		t.Fatal("a mismatched VAPID pair was accepted")
	}
	if !strings.Contains(err.Error(), "VAPID_PUBLIC_KEY") {
		t.Errorf("the error does not name the variable to fix: %v", err)
	}
}

/*
TestAPaddedVAPIDKeyIsAccepted.

The specification says unpadded base64url and generators mostly agree, but a
key pasted out of another tool occasionally arrives padded. Refusing that would
be correct and unhelpful — the value is the same key.
*/
func TestAPaddedVAPIDKeyIsAccepted(t *testing.T) {
	// The same pair, padded to a multiple of four.
	padded := publicKey
	for len(padded)%4 != 0 {
		padded += "="
	}
	paddedPrivate := privateKey
	for len(paddedPrivate)%4 != 0 {
		paddedPrivate += "="
	}

	if _, err := _webpush.NewSenderImpl(_webpush.Options{
		PublicKey:  padded,
		PrivateKey: paddedPrivate,
		Subject:    "mailto:owner@example.com",
	}); err != nil {
		t.Fatalf("a padded key pair was refused: %v", err)
	}
}
