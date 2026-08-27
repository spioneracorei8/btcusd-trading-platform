package models

import (
	"time"

	"github.com/spioneracorei8/btcusd-trading-platform/server/constants"
)

// Device is the phone alerts are delivered to.
//
// There is one. The delivery queue is unique on (signal_id, channel), so it
// can record that a signal was delivered over FCM but not which of several
// devices received it — a second device is a schema change rather than a
// second row, and the table says so with a constraint.
type Device struct {
	// Token is the FCM registration token. Firebase rotates it without
	// asking, so it is stored rather than configured: the app re-registers on
	// every refresh, and a deployment holding the previous one delivers
	// nothing while looking configured.
	Token string

	Platform constants.DevicePlatform

	// Label is free-form, for a person reading the table. "Pixel 7a" beats a
	// token prefix when deciding whether a registration is the phone in your
	// hand.
	Label string

	// RegisteredAt survives a re-registration of the same token; RefreshedAt
	// does not. The pair says both "this phone has been registered since
	// March" and "the app checked in an hour ago", and a token that has not
	// refreshed in weeks is either an app nobody opens or a stale
	// registration.
	RegisteredAt time.Time
	RefreshedAt  time.Time
}

// MaskedToken renders a token safely for a log line or an error.
//
// A registration token is not a password, but it is the one credential in
// this system that lets anything push to the owner's phone, and a value that
// appears in logs eventually appears in a screenshot or a pasted trace.
// Enough characters to tell two registrations apart, and no more.
func (d Device) MaskedToken() string { return MaskToken(d.Token) }

// MaskToken renders any registration token as a prefix and a length.
func MaskToken(token string) string {
	const shown = 6
	if len(token) <= shown {
		// Short enough that a prefix would be most of it. Anything this short
		// is not a real FCM token anyway.
		return "…"
	}
	return token[:shown] + "…"
}
