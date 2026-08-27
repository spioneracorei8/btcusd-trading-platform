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
}

// NewDeviceHandlerImpl builds the device registration handler.
func NewDeviceHandlerImpl(
	usecase notify.DeviceUsecase, logger *slog.Logger, mode constants.SignalMode,
) notify.DeviceHandler {
	return &deviceHandler{usecase: usecase, logger: logger, mode: mode}
}

// RegisterDevice answers POST /api/v1/device.
//
// # Why this exists rather than an environment variable
//
// FCM issues the token to the app on the phone and rotates it without asking.
// A value pasted into a file on the VPS is the previous one from the moment
// Firebase decides otherwise, and the deployment goes on looking configured
// while delivering nothing. See ADR 0026.
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

	platform := constants.DevicePlatformAndroid
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
		Token: request.Token, Platform: platform, Label: request.Label,
	})
	if errors.Is(err, constants.ErrInvalidDevice) {
		// The caller's mistake, and safe to repeat back: the usecase's
		// messages describe the shape of what was wrong and never quote the
		// token.
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
			Note:         h.deliveryNote(false),
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
		// The token is never returned. The app already has it, and this
		// endpoint has no authentication in front of it (ADR 0024) — echoing
		// the one credential that can push to the owner's phone would be the
		// worst thing on the page.
		Token:        d.MaskedToken(),
		Platform:     d.Platform.String(),
		Label:        d.Label,
		RegisteredAt: helper.NullableTime(d.RegisteredAt),
		RefreshedAt:  helper.NullableTime(d.RefreshedAt),
		DeliveryMode: h.mode.String(),
		Note:         h.deliveryNote(true),
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
type registerRequest struct {
	// Token is the FCM registration token from the app.
	Token string `json:"token"`

	// Platform defaults to android when absent, which is the only platform
	// phase 09 builds for.
	Platform string `json:"platform"`

	// Label is free-form and optional, for a person reading the table.
	Label string `json:"label"`
}

type deviceResponse struct {
	// Registered is the question the app is actually asking.
	Registered bool `json:"registered"`

	// Token is masked to a prefix. It identifies which registration this is
	// without being usable.
	Token        string     `json:"token,omitempty"`
	Platform     string     `json:"platform,omitempty"`
	Label        string     `json:"label,omitempty"`
	RegisteredAt *time.Time `json:"registered_at,omitempty"`
	RefreshedAt  *time.Time `json:"refreshed_at,omitempty"`

	// DeliveryMode is silent or notify: whether anything is sent at all.
	DeliveryMode string `json:"delivery_mode"`

	Note string `json:"note"`
}

type forgetResponse struct {
	// Removed is false when there was nothing registered, which is not an
	// error — the caller asked for a state that already held.
	Removed      bool   `json:"removed"`
	DeliveryMode string `json:"delivery_mode"`
	Note         string `json:"note"`
}
