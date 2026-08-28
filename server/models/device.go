package models

import (
	"net/url"
	"time"

	"github.com/spioneracorei8/btcusd-trading-platform/server/constants"
)

// PushSubscription is what a browser hands over when somebody allows alerts.
//
// Three values rather than one token, because Web Push encrypts end to end
// (RFC 8291): the payload is sealed against P256dh and Auth before it leaves
// this host, so the push service forwards ciphertext it cannot read. That is
// what makes it acceptable for a signal's entry, stop and target to travel
// through somebody else's infrastructure at all.
type PushSubscription struct {
	// Endpoint is the URL the push service listens on for this subscriber.
	// It is the subscription's identity: the keys rotate with it.
	Endpoint string

	// P256dh is the subscriber's public key, base64url, as the browser
	// produced it.
	P256dh string

	// Auth is the shared authentication secret, base64url.
	Auth string
}

// Valid reports whether this could be delivered to.
//
// Checked at the edge rather than at send time. A subscription missing a key
// fails inside the encryption with a message about elliptic curves, which is
// a long way from "the app sent half a registration".
func (s PushSubscription) Valid() bool {
	if s.Endpoint == "" || s.P256dh == "" || s.Auth == "" {
		return false
	}
	parsed, err := url.Parse(s.Endpoint)
	// https only: the endpoint is where an encrypted payload is posted, and a
	// scheme-less or plain-http value is either a mistake or somebody's idea
	// of an interesting one.
	return err == nil && parsed.Scheme == "https" && parsed.Host != ""
}

// Host is the push service this subscription belongs to, for a log line.
//
// Apple's, Google's or Mozilla's, depending on the browser. Worth recording
// when delivery starts failing, and safe to record: it is the operator of the
// service, not the subscription.
func (s PushSubscription) Host() string {
	parsed, err := url.Parse(s.Endpoint)
	if err != nil || parsed.Host == "" {
		return "unknown"
	}
	return parsed.Host
}

// Device is the phone alerts are delivered to.
//
// There is one. The delivery queue is unique on (signal_id, channel), so it
// can record that a signal was delivered over Web Push but not which of
// several devices received it — a second device is a schema change rather than
// a second row, and the table says so with a constraint.
type Device struct {
	// Subscription is where alerts go. It is stored rather than configured:
	// the browser issues it to the installed app, replaces it on a reinstall,
	// and expires it whenever the push service likes, so a deployment holding
	// the previous one delivers nothing while looking configured.
	Subscription PushSubscription

	Platform constants.DevicePlatform

	// Label is free-form, for a person reading the table. "iPhone 14" beats an
	// endpoint prefix when deciding whether a registration is the phone in
	// your hand.
	Label string

	// RegisteredAt survives a re-registration of the same endpoint;
	// RefreshedAt does not. The pair says both "this phone has been registered
	// since March" and "the app checked in an hour ago", and a subscription
	// that has not refreshed in weeks is either an app nobody opens or a stale
	// registration.
	RegisteredAt time.Time
	RefreshedAt  time.Time
}

// MaskedEndpoint renders the subscription safely for a log line or an error.
//
// A push endpoint is not a password, but it is the one thing in this system
// that lets anything push to the owner's phone, and a value that appears in
// logs eventually appears in a screenshot or a pasted trace. The push
// service's host, which is public, and enough of the rest to tell two
// registrations apart.
func (d Device) MaskedEndpoint() string { return MaskEndpoint(d.Subscription.Endpoint) }

// MaskEndpoint renders any push endpoint as its host and a short prefix.
func MaskEndpoint(endpoint string) string {
	const shown = 6

	parsed, err := url.Parse(endpoint)
	if err != nil || parsed.Host == "" {
		return MaskSecret(endpoint)
	}

	id := parsed.Path
	if len(id) > shown {
		id = id[:shown]
	}
	return parsed.Host + id + "…"
}

// MaskSecret renders any opaque credential as a prefix.
func MaskSecret(value string) string {
	const shown = 6
	if len(value) <= shown {
		// Short enough that a prefix would be most of it.
		return "…"
	}
	return value[:shown] + "…"
}
