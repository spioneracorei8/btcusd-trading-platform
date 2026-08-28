package usecase

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"github.com/spioneracorei8/btcusd-trading-platform/server/constants"
	"github.com/spioneracorei8/btcusd-trading-platform/server/models"
	"github.com/spioneracorei8/btcusd-trading-platform/server/services/notify"
)

// maxFieldBytes bounds each part of a subscription.
//
// An endpoint runs to a couple of hundred characters and the keys are fixed
// length, but no specification promises a ceiling, so this is generous rather
// than exact. Its job is to stop an unbounded body reaching the database, not
// to validate the format — a length check that tried to be precise would
// reject a subscription the day a push service lengthened them, and the
// symptom would be alerts stopping.
const maxFieldBytes = 4096

// maxLabelBytes bounds the human label.
const maxLabelBytes = 128

type deviceUsecase struct {
	repo notify.DeviceRepository
	log  *slog.Logger
}

// NewDeviceUsecaseImpl builds the device registration rules.
func NewDeviceUsecaseImpl(
	repo notify.DeviceRepository, log *slog.Logger,
) (notify.DeviceUsecase, error) {
	if repo == nil {
		return nil, fmt.Errorf("notify: no device repository")
	}
	if log == nil {
		return nil, fmt.Errorf("notify: no logger")
	}
	return &deviceUsecase{repo: repo, log: log}, nil
}

// RegisterDevice records the phone to deliver to, replacing whatever was there.
//
// # Why re-registering is not a conflict
//
// The app calls this on every launch. A push subscription expires whenever the
// push service decides, and a reinstall or a cleared site data replaces it
// outright — a deployment holding the previous one is the permanent-failure
// case the delivery worker gives up on, which looks like a strategy that
// stopped producing rather than a subscription that expired. Making
// re-registration ordinary is what keeps that from happening silently.
func (u *deviceUsecase) RegisterDevice(
	ctx context.Context, d models.Device,
) (models.Device, error) {
	subscription, err := cleanSubscription(d.Subscription)
	if err != nil {
		return models.Device{}, err
	}

	if !d.Platform.Valid() {
		return models.Device{}, fmt.Errorf("%w: %q is not a device platform",
			constants.ErrInvalidDevice, d.Platform)
	}

	label := strings.TrimSpace(d.Label)
	if len(label) > maxLabelBytes {
		label = label[:maxLabelBytes]
	}

	previous, err := u.repo.FetchDevice(ctx)
	hadPrevious := err == nil

	registered, err := u.repo.RegisterDevice(ctx, models.Device{
		Subscription: subscription, Platform: d.Platform, Label: label,
	})
	if err != nil {
		return models.Device{}, err
	}

	// Said out loud because this is the moment delivery becomes possible, and
	// because a subscription that changed is the one fact that explains alerts
	// having stopped before it. Never the keys, and never the endpoint whole.
	switch {
	case !hadPrevious:
		u.log.InfoContext(ctx, "a device registered for the first time",
			"endpoint", registered.MaskedEndpoint(), "platform", registered.Platform.String(),
			"label", registered.Label)
	case previous.Subscription.Endpoint != registered.Subscription.Endpoint:
		u.log.InfoContext(ctx, "the registered push subscription changed",
			"was", previous.MaskedEndpoint(), "now", registered.MaskedEndpoint(),
			"note", "alerts queued against the previous subscription will have failed as gone")
	default:
		u.log.DebugContext(ctx, "the registered device refreshed",
			"endpoint", registered.MaskedEndpoint())
	}
	return registered, nil
}

// cleanSubscription trims a submitted subscription and refuses an unusable one.
//
// # Why each field is checked here rather than at send time
//
// A missing key fails inside RFC 8291 encryption with a message about elliptic
// curves; a plain-http endpoint fails as a transport error hours later, on a
// signal, in the delivery worker's log. Both are a long way from "the app sent
// half a registration", which is what actually happened and what the app can
// still be told about while somebody is looking at it.
func cleanSubscription(s models.PushSubscription) (models.PushSubscription, error) {
	fields := map[string]*string{
		"endpoint": &s.Endpoint,
		"p256dh":   &s.P256dh,
		"auth":     &s.Auth,
	}

	// Sorted so the message names the same field every time for the same
	// input; ranging a map would report whichever came out first.
	for _, name := range []string{"endpoint", "p256dh", "auth"} {
		value := strings.TrimSpace(*fields[name])
		switch {
		case value == "":
			return models.PushSubscription{}, fmt.Errorf("%w: the subscription has no %s",
				constants.ErrInvalidDevice, name)
		case len(value) > maxFieldBytes:
			return models.PushSubscription{}, fmt.Errorf(
				"%w: the subscription %s is %d bytes, over the %d limit",
				constants.ErrInvalidDevice, name, len(value), maxFieldBytes)
		case strings.ContainsAny(value, " \t\r\n"):
			// Not something a browser produces. A value carrying a stray
			// newline from a copy-paste would be stored, sent, and rejected
			// by the push service — which reads as an uninstalled app rather
			// than a malformed value.
			return models.PushSubscription{}, fmt.Errorf(
				"%w: the subscription %s contains whitespace",
				constants.ErrInvalidDevice, name)
		}
		*fields[name] = value
	}

	if !s.Valid() {
		return models.PushSubscription{}, fmt.Errorf(
			"%w: %q is not an https push endpoint", constants.ErrInvalidDevice, s.Endpoint)
	}
	return s, nil
}

// FetchDevice returns the registered device, or constants.ErrNotFound.
func (u *deviceUsecase) FetchDevice(ctx context.Context) (models.Device, error) {
	return u.repo.FetchDevice(ctx)
}

// ForgetDevice removes the registration, reporting whether there was one.
func (u *deviceUsecase) ForgetDevice(ctx context.Context) (bool, error) {
	removed, err := u.repo.DeleteDevice(ctx)
	if err != nil {
		return false, err
	}
	if removed {
		u.log.WarnContext(ctx, "the registered device was removed",
			"note", "signals will be recorded and not delivered until a device registers")
	}
	return removed, nil
}
