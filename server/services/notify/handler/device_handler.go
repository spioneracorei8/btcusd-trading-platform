package handler

import (
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"time"

	"github.com/spioneracorei8/btcusd-trading-platform/server/constants"
	"github.com/spioneracorei8/btcusd-trading-platform/server/helper"
	"github.com/spioneracorei8/btcusd-trading-platform/server/models"
	"github.com/spioneracorei8/btcusd-trading-platform/server/services/notify"
)

// maxBodyBytes bounds the request body. A registration is a token, a platform
// and a short label; anything larger is not one.
const maxBodyBytes = 8 << 10

type deviceHandler struct {
	usecase notify.DeviceUsecase
	logger  *slog.Logger

	// mode is what this deployment does with a signal. The registration
	// response says so, because "registered" and "will actually be delivered"
	// are different facts and the app should not have to infer the second.
	mode constants.SignalMode

	// vapidPublicKey is what the browser subscribes against. Served rather
	// than built into the app, so rotating the pair does not need a rebuild.
	vapidPublicKey string
}

// NewDeviceHandlerImpl builds the device registration handler.
//
// vapidPublicKey is served to the app, which cannot subscribe without it. It
// is the public half of the pair and is not a secret: the browser sends it to
// the push service, which uses it to check that a push came from the server
// the subscription was made against.
func NewDeviceHandlerImpl(
	usecase notify.DeviceUsecase, logger *slog.Logger, mode constants.SignalMode,
	vapidPublicKey string,
) notify.DeviceHandler {
	return &deviceHandler{
		usecase: usecase, logger: logger, mode: mode, vapidPublicKey: vapidPublicKey,
	}
}

// RegisterDevice answers POST /api/v1/device.
//
// # Why this exists rather than an environment variable
//
// The browser issues the subscription to the installed app and replaces it
// whenever it likes — an expiry, a reinstall, cleared site data. A value
// pasted into a file on the VPS is the previous one from that moment on, and
// the deployment goes on looking configured while delivering nothing. See
// ADR 0026.
func (h *deviceHandler) RegisterDevice(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxBodyBytes))
	if err != nil {
		helper.WriteAPIError(w, h.logger, http.StatusBadRequest,
			constants.APIErrInvalidParameter, "the request body could not be read or is too large")
		return
	}

	var request registerRequest
	if err := json.Unmarshal(body, &request); err != nil {
		helper.WriteAPIError(w, h.logger, http.StatusBadRequest,
			constants.APIErrInvalidParameter, "the request body is not JSON")
		return
	}

	platform := constants.DevicePlatformWeb
	if request.Platform != "" {
		parsed, err := constants.ParseDevicePlatform(request.Platform)
		if err != nil {
			helper.WriteAPIError(w, h.logger, http.StatusBadRequest,
				constants.APIErrInvalidParameter, err.Error())
			return
		}
		platform = parsed
	}

	registered, err := h.usecase.RegisterDevice(r.Context(), models.Device{
		Subscription: models.PushSubscription{
			Endpoint: request.Endpoint,
			P256dh:   request.Keys.P256dh,
			Auth:     request.Keys.Auth,
		},
		Platform: platform,
		Label:    request.Label,
	})
	if errors.Is(err, constants.ErrInvalidDevice) {
		// The caller's mistake, and safe to repeat back: the usecase's
		// messages describe the shape of what was wrong and never quote the
		// endpoint or the keys.
		helper.WriteAPIError(w, h.logger, http.StatusBadRequest,
			constants.APIErrInvalidParameter, err.Error())
		return
	}
	if err != nil {
		h.logger.ErrorContext(r.Context(), "could not register a device", "error", err)
		helper.WriteAPIError(w, h.logger, http.StatusInternalServerError,
			constants.APIErrInternal, "the device could not be registered")
		return
	}

	helper.WriteAPIJSON(w, h.logger, http.StatusOK, h.describe(registered))
}

// Device answers GET /api/v1/device.
//
// The app needs to be able to tell "registered" from "the POST went nowhere"
// without waiting for a signal that may be ten days off.
func (h *deviceHandler) Device(w http.ResponseWriter, r *http.Request) {
	registered, err := h.usecase.FetchDevice(r.Context())
	if errors.Is(err, constants.ErrNotFound) {
		helper.WriteAPIJSON(w, h.logger, http.StatusOK, deviceResponse{
			Registered:   false,
			DeliveryMode: h.mode.String(),
			// The one call the app makes before it has anything to register,
			// and the one that has to carry the key it subscribes with.
			VAPIDPublicKey: h.vapidPublicKey,
			Note:           h.deliveryNote(false),
		})
		return
	}
	if err != nil {
		h.logger.ErrorContext(r.Context(), "could not read the registered device", "error", err)
		helper.WriteAPIError(w, h.logger, http.StatusInternalServerError,
			constants.APIErrInternal, "the registered device could not be read")
		return
	}

	helper.WriteAPIJSON(w, h.logger, http.StatusOK, h.describe(registered))
}

// ForgetDevice answers DELETE /api/v1/device.
func (h *deviceHandler) ForgetDevice(w http.ResponseWriter, r *http.Request) {
	removed, err := h.usecase.ForgetDevice(r.Context())
	if err != nil {
		h.logger.ErrorContext(r.Context(), "could not remove the registered device", "error", err)
		helper.WriteAPIError(w, h.logger, http.StatusInternalServerError,
			constants.APIErrInternal, "the registered device could not be removed")
		return
	}

	helper.WriteAPIJSON(w, h.logger, http.StatusOK, forgetResponse{
		Removed:      removed,
		DeliveryMode: h.mode.String(),
		Note:         h.deliveryNote(false),
	})
}

// describe renders a registration.
func (h *deviceHandler) describe(d models.Device) deviceResponse {
	return deviceResponse{
		Registered: true,
		// The subscription is never returned whole. The app already has it,
		// and this endpoint has no authentication in front of it (ADR 0024) —
		// echoing the keys anything could push to the owner's phone with
		// would be the worst thing on the page. The masked endpoint says
		// which registration this is without being usable.
		Endpoint:       d.MaskedEndpoint(),
		Platform:       d.Platform.String(),
		Label:          d.Label,
		RegisteredAt:   helper.NullableTime(d.RegisteredAt),
		RefreshedAt:    helper.NullableTime(d.RefreshedAt),
		DeliveryMode:   h.mode.String(),
		VAPIDPublicKey: h.vapidPublicKey,
		Note:           h.deliveryNote(true),
	}
}

// deliveryNote says what will actually happen to the next signal.
//
// Registration and delivery are two different facts, and a phone that has
// registered against a deployment in silent mode will never hear anything. The
// app should not have to work that out from two fields.
func (h *deviceHandler) deliveryNote(registered bool) string {
	switch {
	case !h.mode.Delivers():
		return "This deployment is in silent mode: signals are recorded and nothing is sent. " +
			"Registering does not change that; SIGNAL_MODE=notify does."
	case !registered:
		return "No device is registered. Signals will be recorded and queued, and nothing " +
			"will be delivered until one registers."
	default:
		return "Signals will be delivered to this device."
	}
}

// registerRequest is the body of POST /api/v1/device.
//
// The shape is the browser's own: `PushSubscription.toJSON()` produces exactly
// this, so the app posts what it was handed rather than unpacking and
// reassembling it. One less place for a key to be dropped.
type registerRequest struct {
	// Endpoint is where the push service listens for this subscriber.
	Endpoint string `json:"endpoint"`

	// Keys carries the two values the payload is encrypted against.
	Keys struct {
		P256dh string `json:"p256dh"`
		Auth   string `json:"auth"`
	} `json:"keys"`

	// Platform defaults to web when absent, which is what an installed PWA is
	// and the only thing this deployment produces.
	Platform string `json:"platform"`

	// Label is free-form and optional, for a person reading the table.
	Label string `json:"label"`
}

type deviceResponse struct {
	// Registered is the question the app is actually asking.
	Registered bool `json:"registered"`

	// Endpoint is masked to the push service's host and a short prefix. It
	// identifies which registration this is without being usable.
	Endpoint     string     `json:"endpoint,omitempty"`
	Platform     string     `json:"platform,omitempty"`
	Label        string     `json:"label,omitempty"`
	RegisteredAt *time.Time `json:"registered_at,omitempty"`
	RefreshedAt  *time.Time `json:"refreshed_at,omitempty"`

	// DeliveryMode is silent or notify: whether anything is sent at all.
	DeliveryMode string `json:"delivery_mode"`

	// VAPIDPublicKey is what the browser subscribes against, so it is served
	// on every response including the unregistered one — that is the call the
	// app makes before it has anything to register.
	//
	// Empty in silent mode, where no key is configured. The app reads that as
	// "there is nothing to subscribe to here", which is true.
	VAPIDPublicKey string `json:"vapid_public_key,omitempty"`

	Note string `json:"note"`
}

type forgetResponse struct {
	// Removed is false when there was nothing registered, which is not an
	// error — the caller asked for a state that already held.
	Removed      bool   `json:"removed"`
	DeliveryMode string `json:"delivery_mode"`
	Note         string `json:"note"`
}
