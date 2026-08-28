package usecase_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/spioneracorei8/btcusd-trading-platform/server/constants"
	"github.com/spioneracorei8/btcusd-trading-platform/server/models"
	"github.com/spioneracorei8/btcusd-trading-platform/server/services/notify"
	_notify_us "github.com/spioneracorei8/btcusd-trading-platform/server/services/notify/usecase"
)

// aRealisticEndpoint is the shape Apple's push service actually issues, so a
// length bound written against something shorter would show up here.
const aRealisticEndpoint = "https://web.push.apple.com/" +
	"QCyaSPqNfMVEP0vJqSk6abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ" +
	"0123456789abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"

// A realistic pair of subscription keys: base64url, fixed length.
const (
	aRealisticP256dh = "BFtx1cJ8xVQ7Zo3PZ5Vv0qKQpXqZ5RmH8t3wQ2sK9LmN4pR7yTvW1xYzA2bC3dE4fG5hI6jK7lM8nO9pQ0rS1tU"
	aRealisticAuth   = "kZ8xQvN3mLp7RtY2wS5dFg"
)

// registration is a complete, valid device, which each case below then breaks
// in exactly one way.
func registration() models.Device {
	return models.Device{
		Subscription: models.PushSubscription{
			Endpoint: aRealisticEndpoint,
			P256dh:   aRealisticP256dh,
			Auth:     aRealisticAuth,
		},
		Platform: constants.DevicePlatformWeb,
	}
}

// withEndpoint is a registration pointing somewhere else.
func withEndpoint(endpoint string) models.Device {
	d := registration()
	d.Subscription.Endpoint = endpoint
	return d
}

func deviceUsecase(t *testing.T, store *registeredDevices) notify.DeviceUsecase {
	t.Helper()
	return deviceUsecaseOver(t, store)
}

// TestRegisteringTheSameTokenAgainIsNotAConflict.
//
// # What this prevents
//
// The app calls this on every launch. If a second registration were an error
// the app would either treat it as a failure and retry forever, or stop
// calling — and the second is worse, because that call is the mechanism that
// keeps a replaced subscription from silently ending delivery.
func TestRegisteringTheSameSubscriptionAgainIsNotAConflict(t *testing.T) {
	store := &registeredDevices{}
	usecase := deviceUsecase(t, store)

	first, err := usecase.RegisterDevice(context.Background(), models.Device{
		Subscription: registration().Subscription,
		Platform:     constants.DevicePlatformWeb,
		Label:        "iPhone 14",
	})
	if err != nil {
		t.Fatalf("the first registration failed: %v", err)
	}

	second, err := usecase.RegisterDevice(context.Background(), models.Device{
		Subscription: registration().Subscription,
		Platform:     constants.DevicePlatformWeb,
		Label:        "iPhone 14",
	})
	if err != nil {
		t.Fatalf("re-registering the same subscription failed: %v", err)
	}
	if second.Subscription != first.Subscription {
		t.Errorf("the subscription changed from %q to %q",
			first.MaskedEndpoint(), second.MaskedEndpoint())
	}
}

// TestAReplacedSubscriptionReplacesTheOldOne, so the deployment is never
// holding one the push service has already retired.
func TestAReplacedSubscriptionReplacesTheOldOne(t *testing.T) {
	store := &registeredDevices{}
	usecase := deviceUsecase(t, store)

	if _, err := usecase.RegisterDevice(context.Background(), models.Device{
		Subscription: registration().Subscription,
		Platform:     constants.DevicePlatformWeb,
	}); err != nil {
		t.Fatalf("register: %v", err)
	}
	if _, err := usecase.RegisterDevice(context.Background(), models.Device{
		Subscription: withEndpoint(aRealisticEndpoint + "-rotated").Subscription,
		Platform:     constants.DevicePlatformWeb,
	}); err != nil {
		t.Fatalf("re-register: %v", err)
	}

	current, err := usecase.FetchDevice(context.Background())
	if err != nil {
		t.Fatalf("FetchDevice() returned error: %v", err)
	}
	if current.Subscription.Endpoint != aRealisticEndpoint+"-rotated" {
		t.Fatalf("the stored endpoint is %q, want the replacement", current.MaskedEndpoint())
	}
}

// TestAMalformedRegistrationIsRefusedBeforeItIsStored.
//
// Each of these reaches Firebase as an UNREGISTERED rejection if it is stored,
// which the delivery worker correctly treats as permanent — so a stray newline
// from a copy-paste would look exactly like an uninstalled app, and alerts
// would stop with a reason that pointed at the phone.
func TestAMalformedRegistrationIsRefusedBeforeItIsStored(t *testing.T) {
	noKey := registration()
	noKey.Subscription.P256dh = ""

	noAuth := registration()
	noAuth.Subscription.Auth = ""

	unknownPlatform := registration()
	unknownPlatform.Platform = constants.DevicePlatform("blackberry")

	noPlatform := registration()
	noPlatform.Platform = ""

	for name, device := range map[string]models.Device{
		"empty endpoint":   withEndpoint(""),
		"whitespace only":  withEndpoint("   "),
		"a newline inside": withEndpoint(aRealisticEndpoint[:30] + "\n" + aRealisticEndpoint[30:]),
		"a space inside":   withEndpoint(aRealisticEndpoint[:30] + " " + aRealisticEndpoint[30:]),

		// The two that only Web Push has, and the two that fail furthest from
		// the cause if they get through: a subscription missing a key fails
		// inside RFC 8291 encryption with a message about elliptic curves.
		"no encryption key": noKey,
		"no auth secret":    noAuth,

		// Not a push endpoint at all. A plain-http one would fail as a
		// transport error hours later, on a signal.
		"an http endpoint":    withEndpoint("http://web.push.apple.com/QNotSecure"),
		"a scheme-less host":  withEndpoint("web.push.apple.com/QNoScheme"),
		"not a URL":           withEndpoint("this is not a url"),
		"an unknown platform": unknownPlatform,
		"no platform":         noPlatform,
		"an absurd endpoint":  withEndpoint("https://web.push.apple.com/" + strings.Repeat("a", 8193)),
	} {
		t.Run(name, func(t *testing.T) {
			store := &registeredDevices{}

			_, err := deviceUsecase(t, store).RegisterDevice(context.Background(), device)
			if err == nil {
				t.Fatal("it was accepted")
			}
			if !errors.Is(err, constants.ErrInvalidDevice) {
				t.Errorf("error = %v, want ErrInvalidDevice so the handler answers 400", err)
			}
			if store.device != nil {
				t.Error("it reached the store anyway")
			}
		})
	}
}

// TestSurroundingWhitespaceIsTrimmedAndInternalWhitespaceIsRefused.
//
// The two are different mistakes. Padding around the value is how a token
// arrives when somebody registers by hand — `--data "{\"token\": \"$(cat
// token.txt)\"}"` picks up a trailing newline every time — and the trimmed
// value is unambiguous, so accepting it costs nothing.
//
// Whitespace *inside* the token is not that. It is a value that was corrupted
// on the way in, and storing it means Firebase rejects the send as
// UNREGISTERED — which the delivery worker correctly treats as permanent, and
// which reads as an uninstalled app rather than a mangled token. Alerts stop,
// and the reason points at the phone.
func TestSurroundingWhitespaceIsTrimmedAndInternalWhitespaceIsRefused(t *testing.T) {
	for name, endpoint := range map[string]string{
		"leading and trailing spaces": "  " + aRealisticEndpoint + "  ",
		"a trailing newline":          aRealisticEndpoint + "\n",
		"a trailing carriage return":  aRealisticEndpoint + "\r\n",
		"a leading tab":               "\t" + aRealisticEndpoint,
	} {
		t.Run(name+" is trimmed", func(t *testing.T) {
			store := &registeredDevices{}

			registered, err := deviceUsecase(t, store).RegisterDevice(
				context.Background(), withEndpoint(endpoint))
			if err != nil {
				t.Fatalf("a padded endpoint was refused: %v", err)
			}
			if registered.Subscription.Endpoint != aRealisticEndpoint {
				t.Errorf("stored an endpoint of %d characters, want the %d-character one "+
					"without its padding",
					len(registered.Subscription.Endpoint), len(aRealisticEndpoint))
			}
		})
	}

	for name, endpoint := range map[string]string{
		"a newline inside": aRealisticEndpoint[:30] + "\n" + aRealisticEndpoint[30:],
		"a space inside":   aRealisticEndpoint[:30] + " " + aRealisticEndpoint[30:],
		"a tab inside":     aRealisticEndpoint[:30] + "\t" + aRealisticEndpoint[30:],
	} {
		t.Run(name+" is refused", func(t *testing.T) {
			store := &registeredDevices{}

			_, err := deviceUsecase(t, store).RegisterDevice(
				context.Background(), withEndpoint(endpoint))
			if !errors.Is(err, constants.ErrInvalidDevice) {
				t.Fatalf("error = %v, want ErrInvalidDevice", err)
			}
			if store.device != nil {
				t.Error("it reached the store anyway")
			}
		})
	}
}

// TestAnOversizedLabelIsTruncatedRatherThanRefused.
//
// The label is decoration for a person reading the table. Refusing a
// registration over it would mean the phone cannot register because its model
// name is long, which is the wrong thing to be strict about.
func TestAnOversizedLabelIsTruncatedRatherThanRefused(t *testing.T) {
	store := &registeredDevices{}

	oversized := registration()
	oversized.Label = strings.Repeat("x", 500)

	registered, err := deviceUsecase(t, store).RegisterDevice(context.Background(), oversized)
	if err != nil {
		t.Fatalf("a long label was refused: %v", err)
	}
	if len(registered.Label) != 128 {
		t.Errorf("label is %d characters, want it truncated to 128", len(registered.Label))
	}
}

// TestNothingRegisteredIsNotFoundRatherThanAnEmptyDevice.
//
// The delivery worker branches on this, and it is the difference between an
// alert that waits for a phone and one that is sent to the empty string.
func TestNothingRegisteredIsNotFoundRatherThanAnEmptyDevice(t *testing.T) {
	_, err := deviceUsecase(t, &registeredDevices{}).FetchDevice(context.Background())
	if !errors.Is(err, constants.ErrNotFound) {
		t.Fatalf("error = %v, want ErrNotFound", err)
	}
}

// TestForgettingReportsWhetherThereWasAnything, so a caller can tell "removed"
// from "there was nothing to remove" without a second lookup.
func TestForgettingReportsWhetherThereWasAnything(t *testing.T) {
	store := &registeredDevices{}
	usecase := deviceUsecase(t, store)

	if removed, err := usecase.ForgetDevice(context.Background()); err != nil || removed {
		t.Fatalf("ForgetDevice() on an empty store = %v, %v; want false, nil", removed, err)
	}

	if _, err := usecase.RegisterDevice(context.Background(), models.Device{
		Subscription: registration().Subscription,
		Platform:     constants.DevicePlatformWeb,
	}); err != nil {
		t.Fatalf("register: %v", err)
	}

	if removed, err := usecase.ForgetDevice(context.Background()); err != nil || !removed {
		t.Fatalf("ForgetDevice() = %v, %v; want true, nil", removed, err)
	}
	if _, err := usecase.FetchDevice(context.Background()); !errors.Is(err, constants.ErrNotFound) {
		t.Errorf("the device survived being forgotten: %v", err)
	}
}

// TestTheSubscriptionIsNeverRenderedInFull.
//
// It is the one thing in this system that lets anything push to the owner's
// phone, and a value that appears in a log line eventually appears in a
// screenshot. The push service's host is fine — it is Apple, or Google, and
// says nothing about which subscriber this is — but the identifier after it is
// the part that must not travel.
func TestTheSubscriptionIsNeverRenderedInFull(t *testing.T) {
	device := models.Device{Subscription: registration().Subscription}

	masked := device.MaskedEndpoint()
	if masked == aRealisticEndpoint {
		t.Fatal("MaskedEndpoint returned the endpoint")
	}
	if strings.Contains(masked, aRealisticEndpoint[len(aRealisticEndpoint)-20:]) {
		t.Error("the mask reveals the end of the endpoint, which is the part that varies most")
	}
	if !strings.HasPrefix(masked, "web.push.apple.com") {
		t.Errorf("the mask %q does not name the push service, which is the useful half", masked)
	}
	if !strings.HasPrefix(aRealisticEndpoint, "https://"+strings.TrimSuffix(masked, "…")) {
		t.Errorf("the mask %q is not a prefix of the endpoint, so it cannot identify one", masked)
	}
}

/*
TestTheKeysAreNeverRenderedAtAll.

The endpoint alone cannot be pushed to: the payload has to be encrypted against
p256dh and signed for auth. Those two are the actual secret, so unlike the
endpoint there is no useful half to show, and nothing renders them.
*/
func TestTheKeysAreNeverRenderedAtAll(t *testing.T) {
	device := models.Device{Subscription: registration().Subscription}

	rendered := device.MaskedEndpoint()
	for name, key := range map[string]string{
		"p256dh": aRealisticP256dh,
		"auth":   aRealisticAuth,
	} {
		if strings.Contains(rendered, key) {
			t.Errorf("the rendered device contains the %s key", name)
		}
	}
}

// TestAShortValueIsMaskedEntirely. A six-character value is not a real
// credential, but if one ever reaches a log the mask must not simply print it.
func TestAShortValueIsMaskedEntirely(t *testing.T) {
	if got := models.MaskSecret("abc"); got != "…" {
		t.Errorf("MaskSecret(%q) = %q, want the value hidden entirely", "abc", got)
	}
	// An endpoint that is not a URL falls back to the same rule.
	if got := models.MaskEndpoint("abc"); got != "…" {
		t.Errorf("MaskEndpoint(%q) = %q, want the value hidden entirely", "abc", got)
	}
}

// TestTheUsecaseRefusesToBuildWithoutWhatItNeeds.
func TestTheUsecaseRefusesToBuildWithoutWhatItNeeds(t *testing.T) {
	if _, err := _notify_us.NewDeviceUsecaseImpl(nil, silentLog()); err == nil {
		t.Error("built with no repository")
	}
	if _, err := _notify_us.NewDeviceUsecaseImpl(&registeredDevices{}, nil); err == nil {
		t.Error("built with no logger")
	}
}
