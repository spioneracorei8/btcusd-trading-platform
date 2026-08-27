package notify

import "net/http"

// DeviceHandler serves device registration.
type DeviceHandler interface {
	// RegisterDevice answers POST /api/v1/device.
	RegisterDevice(w http.ResponseWriter, r *http.Request)

	// Device answers GET /api/v1/device, so the app can tell "registered"
	// from "the POST silently went nowhere" without waiting for a signal.
	Device(w http.ResponseWriter, r *http.Request)

	// ForgetDevice answers DELETE /api/v1/device.
	ForgetDevice(w http.ResponseWriter, r *http.Request)
}
