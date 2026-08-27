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

// maxTokenBytes bounds what will be accepted as a registration token.
//
// FCM tokens run to roughly 160-200 characters and Google does not promise a
// ceiling, so this is generous rather than exact. Its job is to stop an
// unbounded body reaching the database, not to validate the format — a
// length check that tried to be precise would reject a token the day Firebase
// lengthened them, and the symptom would be alerts stopping.
const maxTokenBytes = 4096

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
// The app calls this on every launch and on every FCM token refresh. Firebase
// rotates tokens on its own schedule, and a deployment holding the previous
// one is the permanent-failure case the delivery worker gives up on — which
// looks like a strategy that stopped producing rather than a token that
// expired. Making re-registration ordinary is what keeps that from happening
// silently.
func (u *deviceUsecase) RegisterDevice(
	ctx context.Context, d models.Device,
) (models.Device, error) {
	token := strings.TrimSpace(d.Token)
	switch {
	case token == "":
		return models.Device{}, fmt.Errorf("%w: the registration token is empty",
			constants.ErrInvalidDevice)
	case len(token) > maxTokenBytes:
		return models.Device{}, fmt.Errorf("%w: the registration token is %d bytes, over the %d limit",
			constants.ErrInvalidDevice, len(token), maxTokenBytes)
	}

	// Whitespace inside a token is not something FCM produces, and a token
	// carrying a stray newline from a copy-paste would be stored, sent, and
	// rejected by Firebase as unregistered — which reads as an uninstalled
	// app rather than a malformed value.
	if strings.ContainsAny(token, " \t\r\n") {
		return models.Device{}, fmt.Errorf("%w: the registration token contains whitespace",
			constants.ErrInvalidDevice)
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
		Token: token, Platform: d.Platform, Label: label,
	})
	if err != nil {
		return models.Device{}, err
	}

	// Said out loud because this is the moment delivery becomes possible, and
	// because a token that changed is the one fact that explains alerts having
	// stopped before it. Never the token itself.
	switch {
	case !hadPrevious:
		u.log.InfoContext(ctx, "a device registered for the first time",
			"token", registered.MaskedToken(), "platform", registered.Platform.String(),
			"label", registered.Label)
	case previous.Token != registered.Token:
		u.log.InfoContext(ctx, "the registered device token changed",
			"was", previous.MaskedToken(), "now", registered.MaskedToken(),
			"note", "alerts queued against the previous token will have failed as unregistered")
	default:
		u.log.DebugContext(ctx, "the registered device refreshed",
			"token", registered.MaskedToken())
	}
	return registered, nil
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
