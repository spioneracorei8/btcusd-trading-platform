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

// aRealisticToken is the shape and length FCM actually issues, so a length
// bound written against something shorter would show up here.
const aRealisticToken = "fMEP0vJqSk6" + "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789" +
	":APA91bH" + "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789" +
	"abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"

func deviceUsecase(t *testing.T, store *registeredDevices) notify.DeviceUsecase {
	t.Helper()
	return deviceUsecaseOver(t, store)
}

// TestRegisteringTheSameTokenAgainIsNotAConflict.
//
// # What this prevents
//
// The app calls this on every launch and on every FCM refresh. If a second
// registration were an error the app would either treat it as a failure and
// retry forever, or stop calling — and the second is worse, because that call
// is the mechanism that keeps a rotated token from silently ending delivery.
func TestRegisteringTheSameTokenAgainIsNotAConflict(t *testing.T) {
	store := &registeredDevices{}
	usecase := deviceUsecase(t, store)

	first, err := usecase.RegisterDevice(context.Background(), models.Device{
		Token: aRealisticToken, Platform: constants.DevicePlatformAndroid, Label: "Pixel 7a",
	})
	if err != nil {
		t.Fatalf("the first registration failed: %v", err)
	}

	second, err := usecase.RegisterDevice(context.Background(), models.Device{
		Token: aRealisticToken, Platform: constants.DevicePlatformAndroid, Label: "Pixel 7a",
	})
	if err != nil {
		t.Fatalf("re-registering the same token failed: %v", err)
	}
	if second.Token != first.Token {
		t.Errorf("token changed from %q to %q", first.MaskedToken(), second.MaskedToken())
	}
}

// TestARotatedTokenReplacesTheOldOne, so the deployment is never holding a
// token Firebase has already retired.
func TestARotatedTokenReplacesTheOldOne(t *testing.T) {
	store := &registeredDevices{}
	usecase := deviceUsecase(t, store)

	if _, err := usecase.RegisterDevice(context.Background(), models.Device{
		Token: aRealisticToken, Platform: constants.DevicePlatformAndroid,
	}); err != nil {
		t.Fatalf("register: %v", err)
	}
	if _, err := usecase.RegisterDevice(context.Background(), models.Device{
		Token: "rotated-" + aRealisticToken, Platform: constants.DevicePlatformAndroid,
	}); err != nil {
		t.Fatalf("re-register: %v", err)
	}

	current, err := usecase.FetchDevice(context.Background())
	if err != nil {
		t.Fatalf("FetchDevice() returned error: %v", err)
	}
	if current.Token != "rotated-"+aRealisticToken {
		t.Fatalf("the stored token is %q, want the rotated one", current.MaskedToken())
	}
}

// TestAMalformedRegistrationIsRefusedBeforeItIsStored.
//
// Each of these reaches Firebase as an UNREGISTERED rejection if it is stored,
// which the delivery worker correctly treats as permanent — so a stray newline
// from a copy-paste would look exactly like an uninstalled app, and alerts
// would stop with a reason that pointed at the phone.
func TestAMalformedRegistrationIsRefusedBeforeItIsStored(t *testing.T) {
	for name, device := range map[string]models.Device{
		"empty token": {
			Token: "", Platform: constants.DevicePlatformAndroid,
		},
		"whitespace only": {
			Token: "   ", Platform: constants.DevicePlatformAndroid,
		},
		"a newline inside": {
			Token:    aRealisticToken[:20] + "\n" + aRealisticToken[20:],
			Platform: constants.DevicePlatformAndroid,
		},
		"a space inside": {
			Token:    aRealisticToken[:20] + " " + aRealisticToken[20:],
			Platform: constants.DevicePlatformAndroid,
		},
		"an unknown platform": {
			Token: aRealisticToken, Platform: constants.DevicePlatform("blackberry"),
		},
		"no platform": {
			Token: aRealisticToken,
		},
		"an absurd token": {
			Token: strings.Repeat("a", 8193), Platform: constants.DevicePlatformAndroid,
		},
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
	for name, token := range map[string]string{
		"leading and trailing spaces": "  " + aRealisticToken + "  ",
		"a trailing newline":          aRealisticToken + "\n",
		"a trailing carriage return":  aRealisticToken + "\r\n",
		"a leading tab":               "\t" + aRealisticToken,
	} {
		t.Run(name+" is trimmed", func(t *testing.T) {
			store := &registeredDevices{}

			registered, err := deviceUsecase(t, store).RegisterDevice(
				context.Background(), models.Device{
					Token: token, Platform: constants.DevicePlatformAndroid,
				})
			if err != nil {
				t.Fatalf("a padded token was refused: %v", err)
			}
			if registered.Token != aRealisticToken {
				t.Errorf("stored a token of %d characters, want the %d-character token "+
					"without its padding", len(registered.Token), len(aRealisticToken))
			}
		})
	}

	for name, token := range map[string]string{
		"a newline inside": aRealisticToken[:20] + "\n" + aRealisticToken[20:],
		"a space inside":   aRealisticToken[:20] + " " + aRealisticToken[20:],
		"a tab inside":     aRealisticToken[:20] + "\t" + aRealisticToken[20:],
	} {
		t.Run(name+" is refused", func(t *testing.T) {
			store := &registeredDevices{}

			_, err := deviceUsecase(t, store).RegisterDevice(
				context.Background(), models.Device{
					Token: token, Platform: constants.DevicePlatformAndroid,
				})
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

	registered, err := deviceUsecase(t, store).RegisterDevice(context.Background(), models.Device{
		Token: aRealisticToken, Platform: constants.DevicePlatformAndroid,
		Label: strings.Repeat("x", 500),
	})
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
		Token: aRealisticToken, Platform: constants.DevicePlatformAndroid,
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

// TestTheTokenIsNeverRenderedInFull.
//
// It is the one credential in this system that can push to the owner's phone,
// and a value that appears in a log line eventually appears in a screenshot.
func TestTheTokenIsNeverRenderedInFull(t *testing.T) {
	device := models.Device{Token: aRealisticToken}

	masked := device.MaskedToken()
	if masked == aRealisticToken {
		t.Fatal("MaskedToken returned the token")
	}
	if strings.Contains(aRealisticToken[len(aRealisticToken)-20:], masked) {
		t.Error("the mask reveals the end of the token, which is the part that varies most")
	}
	if len(masked) > 12 {
		t.Errorf("the mask is %d characters (%q); enough to tell two apart is enough", len(masked), masked)
	}
	if !strings.HasPrefix(aRealisticToken, strings.TrimSuffix(masked, "…")) {
		t.Errorf("the mask %q is not a prefix of the token, so it cannot identify one", masked)
	}
}

// TestAShortTokenIsMaskedEntirely. A six-character value is not an FCM token,
// but if one ever reaches a log the mask must not simply print it.
func TestAShortTokenIsMaskedEntirely(t *testing.T) {
	if got := models.MaskToken("abc"); got != "…" {
		t.Errorf("MaskToken(%q) = %q, want the value hidden entirely", "abc", got)
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
