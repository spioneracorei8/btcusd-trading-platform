// Package webpush delivers a signal to the owner's phone over the Web Push
// protocol.
//
// It is a repository: a client that talks to something outside the process,
// whose vendor payloads never leave its own package. The payload shape the
// service worker parses is defined in wire.go and converted at this boundary.
//
// # Why a library rather than this file doing it
//
// RFC 8291 encrypts the payload against the subscriber's key with ECDH,
// HKDF and AES-128-GCM, and RFC 8292 signs a JWT with the VAPID key to prove
// which application server is asking. Both are the kind of thing that appears
// to work when done wrong: a payload encrypted with the wrong salt is
// indistinguishable from a delivered one, right up until the phone silently
// drops it. github.com/SherClockHolmes/webpush-go is the maintained
// implementation.
package webpush

import (
	"bytes"
	"context"
	"crypto/ecdh"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	push "github.com/SherClockHolmes/webpush-go"

	"github.com/spioneracorei8/btcusd-trading-platform/server/constants"
	"github.com/spioneracorei8/btcusd-trading-platform/server/models"
	"github.com/spioneracorei8/btcusd-trading-platform/server/services/notify"
)

// Options configures the sender.
type Options struct {
	// PublicKey and PrivateKey are the VAPID pair, base64url. The public half
	// also reaches the browser, which subscribes against it; the private half
	// never leaves this host.
	PublicKey  string
	PrivateKey string

	// Subject identifies who to contact about this application server — a
	// mailto: or https: URL, required by RFC 8292. Push services use it when
	// something is wrong with the traffic rather than for delivery.
	Subject string

	// TTL is how long the push service holds a message for a phone that is
	// off or out of range.
	TTL time.Duration

	// Client is injected by tests. The library takes any Do-er.
	Client push.HTTPClient

	Logger *slog.Logger
}

type sender struct {
	options Options
	log     *slog.Logger
}

// NewSenderImpl builds a Web Push sender.
//
// The keys are validated here rather than on the first signal. A malformed
// VAPID key fails inside the JWT signing with a message about ASN.1, hours
// later, on the one alert somebody was waiting for.
func NewSenderImpl(options Options) (notify.Sender, error) {
	if strings.TrimSpace(options.PublicKey) == "" {
		return nil, fmt.Errorf("webpush: no VAPID public key")
	}
	if strings.TrimSpace(options.PrivateKey) == "" {
		return nil, fmt.Errorf("webpush: no VAPID private key")
	}
	if err := validSubject(options.Subject); err != nil {
		return nil, err
	}
	if err := validKeys(options.PublicKey, options.PrivateKey); err != nil {
		return nil, err
	}

	if options.TTL <= 0 {
		options.TTL = constants.DefaultWebPushTTL
	}
	if options.Logger == nil {
		options.Logger = slog.Default()
	}

	return &sender{options: options, log: options.Logger}, nil
}

// Channel is the delivery target this sender serves.
func (s *sender) Channel() constants.NotificationChannel {
	return constants.NotificationChannelWebPush
}

// Send delivers one message.
//
// A rejection that retrying cannot fix is wrapped with
// notify.ErrUndeliverable so the caller stops rather than spending its whole
// attempt budget on a subscription that no longer exists.
func (s *sender) Send(ctx context.Context, message notify.Message) error {
	if !message.To.Valid() {
		return fmt.Errorf("webpush: the subscription is not usable: %w", notify.ErrUndeliverable)
	}

	body, err := json.Marshal(toPayload(message))
	if err != nil {
		return fmt.Errorf("webpush: encode payload: %w", err)
	}

	res, err := push.SendNotificationWithContext(ctx, body, &push.Subscription{
		Endpoint: message.To.Endpoint,
		Keys: push.Keys{
			P256dh: message.To.P256dh,
			Auth:   message.To.Auth,
		},
	}, &push.Options{
		HTTPClient:      s.options.Client,
		Subscriber:      s.options.Subject,
		VAPIDPublicKey:  s.options.PublicKey,
		VAPIDPrivateKey: s.options.PrivateKey,
		TTL:             int(s.options.TTL.Seconds()),
		// Urgency high: this is a market signal with a stop and a target, and
		// a push service that batches it until the phone next wakes has
		// delivered it too late to act on.
		Urgency: push.UrgencyHigh,
	})
	if err != nil {
		// Encryption failures land here alongside transport ones. Both are
		// reported as transient, which is wrong for the first and right for
		// the second — but a key that cannot encrypt was validated at
		// construction, so what actually reaches here is the network.
		return fmt.Errorf("webpush: send to %s: %w", models.MaskEndpoint(message.To.Endpoint), err)
	}
	defer func() { _ = res.Body.Close() }()

	if res.StatusCode >= 200 && res.StatusCode < 300 {
		return nil
	}
	return classify(res.StatusCode, readError(res.Body), message.To)
}

// classify decides whether a rejection is worth repeating.
//
// The split follows what the status means rather than a list of codes: 4xx is
// the push service saying the request is wrong, and sending the same wrong
// request again will be wrong again. The exceptions are about timing rather
// than content.
func classify(status int, detail string, to models.PushSubscription) error {
	if detail == "" {
		detail = http.StatusText(status)
	}
	where := models.MaskEndpoint(to.Endpoint)

	switch {
	case status == http.StatusNotFound, status == http.StatusGone:
		// The subscription is gone: an uninstall, a browser that cleared site
		// data, or an expiry. This is the case the whole re-registration
		// mechanism exists for, so it is named rather than left as a code —
		// somebody reading notifications.last_error should be told to open
		// the app, not to check the network.
		return fmt.Errorf(
			"webpush: %d %s: the subscription at %s is gone; the phone must open the app "+
				"to register again: %w",
			status, detail, where, notify.ErrUndeliverable)

	case status == http.StatusRequestEntityTooLarge:
		// The payload is over the push service's limit. It will be over it
		// next time too.
		return fmt.Errorf("webpush: %d %s: the payload is too large: %w",
			status, detail, notify.ErrUndeliverable)

	case status == http.StatusTooManyRequests, status == http.StatusRequestTimeout:
		return fmt.Errorf("webpush: %d %s", status, detail)

	case status >= 400 && status < 500:
		return fmt.Errorf("webpush: %d %s to %s: %w",
			status, detail, where, notify.ErrUndeliverable)

	default:
		return fmt.Errorf("webpush: %d %s", status, detail)
	}
}

// readError reads a bounded amount of the error body.
//
// Bounded because it is stored in notifications.last_error and shown to a
// person: an unbounded response would put an arbitrary amount of somebody
// else's text into a column meant to hold an explanation.
func readError(r io.Reader) string {
	raw, err := io.ReadAll(io.LimitReader(r, constants.NotifyErrorBodyLimit))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(raw))
}

// validSubject checks the VAPID subject.
//
// RFC 8292 requires a mailto: or https: URL. Push services differ in how
// strictly they enforce it, and the ones that do reject with a 403 that reads
// like a key problem.
func validSubject(subject string) error {
	subject = strings.TrimSpace(subject)
	if subject == "" {
		return errors.New("webpush: no VAPID subject; RFC 8292 wants a mailto: or https: URL " +
			"saying who to contact about this application server")
	}
	if !strings.HasPrefix(subject, "mailto:") && !strings.HasPrefix(subject, "https://") {
		return fmt.Errorf("webpush: the VAPID subject %q must be a mailto: or https: URL", subject)
	}
	return nil
}

// validKeys proves the pair is a pair, at start-up.
//
// # Why not just hand them to the library
//
// It decodes them lazily, inside the send path, so a malformed key runs fine
// for a week and then fails the first alert with a message about elliptic
// curves — days after the deploy that caused it, and looking like the strategy
// having gone quiet.
//
// The check that matters most is the last one. Two keys that are individually
// well-formed but not a pair produce a JWT signed with a key the subscription
// was not made against, and the push service answers 403 — which reads as a
// permissions problem and sends whoever investigates to look at the wrong
// thing entirely. Nothing else in the system can notice that.
func validKeys(public, private string) error {
	publicBytes, err := decodeKey(public)
	if err != nil {
		return fmt.Errorf("webpush: the VAPID public key is not base64url: %w", err)
	}
	privateBytes, err := decodeKey(private)
	if err != nil {
		return fmt.Errorf("webpush: the VAPID private key is not base64url: %w", err)
	}

	// P-256, which is what RFC 8292 specifies and the only curve any push
	// service accepts.
	key, err := ecdh.P256().NewPrivateKey(privateBytes)
	if err != nil {
		return fmt.Errorf("webpush: the VAPID private key is not a P-256 scalar: %w", err)
	}

	if !bytes.Equal(key.PublicKey().Bytes(), publicBytes) {
		return errors.New("webpush: VAPID_PUBLIC_KEY is not the public half of " +
			"VAPID_PRIVATE_KEY; every push would be signed with a key the browser did " +
			"not subscribe against, and the push service would answer 403")
	}
	return nil
}

// decodeKey accepts a VAPID key with or without base64 padding.
//
// The specification says unpadded, generators mostly agree, and a key pasted
// out of some other tool occasionally arrives padded. Refusing that would be
// correct and unhelpful.
func decodeKey(value string) ([]byte, error) {
	value = strings.TrimSpace(value)
	if decoded, err := base64.RawURLEncoding.DecodeString(value); err == nil {
		return decoded, nil
	}
	return base64.URLEncoding.DecodeString(value)
}
