package repository_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/spioneracorei8/btcusd-trading-platform/server/constants"
	"github.com/spioneracorei8/btcusd-trading-platform/server/models"
	"github.com/spioneracorei8/btcusd-trading-platform/server/services/notify"
	_notify_repo "github.com/spioneracorei8/btcusd-trading-platform/server/services/notify/repository"
	"github.com/spioneracorei8/btcusd-trading-platform/server/testhelper"
)

const (
	firstToken  = "fMEP0vJqSk6:APA91bHfirst_registration_token_0123456789ABCDEFGH"
	secondToken = "cXqR2mNpTz9:APA91bHsecond_registration_token_0123456789ABCDEFG"
)

// deviceRepo returns a repository over an empty devices table.
//
// The table holds one row for the whole deployment, so unlike every other
// integration test here there is no symbol to scope to: these tests share
// state and clear it up front.
func deviceRepo(t *testing.T) (notify.DeviceRepository, *pgxpool.Pool) {
	t.Helper()

	pool := testhelper.NewTestPool(t)
	repo := _notify_repo.NewDeviceRepoImpl(pool)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if _, err := repo.DeleteDevice(ctx); err != nil {
		t.Fatalf("clear the devices table: %v", err)
	}
	return repo, pool
}

// TestOnlyOneDeviceCanBeRegistered.
//
// # What this prevents
//
// The delivery queue is unique on (signal_id, channel): it can record that a
// signal was delivered over FCM, and not which of several devices received it.
// A second registration would therefore mean either that the second phone
// silently never gets anything, or that one signal needs two queue rows — a
// design change rather than a configuration.
//
// So the second registration replaces the first rather than joining it, and
// the table enforces that with a constraint rather than leaving it to the
// upsert to be written correctly.
func TestOnlyOneDeviceCanBeRegistered(t *testing.T) {
	repo, pool := deviceRepo(t)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if _, err := repo.RegisterDevice(ctx, models.Device{
		Token: firstToken, Platform: constants.DevicePlatformAndroid, Label: "old phone",
	}); err != nil {
		t.Fatalf("the first registration failed: %v", err)
	}
	if _, err := repo.RegisterDevice(ctx, models.Device{
		Token: secondToken, Platform: constants.DevicePlatformAndroid, Label: "new phone",
	}); err != nil {
		t.Fatalf("the second registration failed: %v", err)
	}

	var rows int
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM devices").Scan(&rows); err != nil {
		t.Fatalf("count devices: %v", err)
	}
	if rows != 1 {
		t.Fatalf("the devices table holds %d rows, want exactly 1", rows)
	}

	current, err := repo.FetchDevice(ctx)
	if err != nil {
		t.Fatalf("FetchDevice() returned error: %v", err)
	}
	if current.Token != secondToken {
		t.Errorf("the stored token is %q, want the second registration", current.MaskedToken())
	}
	if current.Label != "new phone" {
		t.Errorf("label = %q, want the second registration's", current.Label)
	}
}

// TestASecondRowIsRefusedByTheDatabase.
//
// The upsert above targets id 1, so it can only ever produce one row. This is
// the other half: a statement that did not go through the repository — a
// migration, a fix applied by hand, a future second-device feature written
// without reading the comment — is refused by the table itself.
func TestASecondRowIsRefusedByTheDatabase(t *testing.T) {
	repo, pool := deviceRepo(t)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if _, err := repo.RegisterDevice(ctx, models.Device{
		Token: firstToken, Platform: constants.DevicePlatformAndroid,
	}); err != nil {
		t.Fatalf("register: %v", err)
	}

	_, err := pool.Exec(ctx,
		"INSERT INTO devices (id, token, platform) VALUES (2, $1, 'android')", secondToken)
	if err == nil {
		t.Fatal("a second device row was accepted")
	}
}

// TestReRegisteringTheSameTokenKeepsWhenItFirstArrived.
//
// The app re-registers on every launch. If registered_at moved each time, it
// would only ever say "a moment ago" and the one question it can answer —
// how long this phone has been the registered one — would be unanswerable.
// refreshed_at is what moves.
func TestReRegisteringTheSameTokenKeepsWhenItFirstArrived(t *testing.T) {
	repo, _ := deviceRepo(t)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	first, err := repo.RegisterDevice(ctx, models.Device{
		Token: firstToken, Platform: constants.DevicePlatformAndroid,
	})
	if err != nil {
		t.Fatalf("register: %v", err)
	}

	// now() is transaction-scoped in PostgreSQL, so two statements in one
	// session still differ — but only by microseconds. Waiting a moment makes
	// the assertion about the rule rather than about clock resolution.
	time.Sleep(10 * time.Millisecond)

	again, err := repo.RegisterDevice(ctx, models.Device{
		Token: firstToken, Platform: constants.DevicePlatformAndroid,
	})
	if err != nil {
		t.Fatalf("re-register: %v", err)
	}

	if !again.RegisteredAt.Equal(first.RegisteredAt) {
		t.Errorf("registered_at moved from %s to %s on a refresh of the same token",
			first.RegisteredAt, again.RegisteredAt)
	}
	if !again.RefreshedAt.After(first.RefreshedAt) {
		t.Errorf("refreshed_at did not move: %s then %s", first.RefreshedAt, again.RefreshedAt)
	}
}

// TestADifferentTokenIsANewRegistration.
//
// The counterpart: a rotated token is a new registration, not a refresh of the
// old one, and registered_at says so. Otherwise a phone replaced last week
// would claim to have been registered since March.
func TestADifferentTokenIsANewRegistration(t *testing.T) {
	repo, _ := deviceRepo(t)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	first, err := repo.RegisterDevice(ctx, models.Device{
		Token: firstToken, Platform: constants.DevicePlatformAndroid,
	})
	if err != nil {
		t.Fatalf("register: %v", err)
	}

	time.Sleep(10 * time.Millisecond)

	rotated, err := repo.RegisterDevice(ctx, models.Device{
		Token: secondToken, Platform: constants.DevicePlatformAndroid,
	})
	if err != nil {
		t.Fatalf("re-register: %v", err)
	}

	if !rotated.RegisteredAt.After(first.RegisteredAt) {
		t.Errorf("registered_at did not move for a different token: %s then %s",
			first.RegisteredAt, rotated.RegisteredAt)
	}
}

// TestNothingRegisteredIsNotFound, which the delivery worker branches on: it
// is the difference between an alert that waits for a phone and one sent to
// the empty string.
func TestNothingRegisteredIsNotFound(t *testing.T) {
	repo, _ := deviceRepo(t)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if _, err := repo.FetchDevice(ctx); !errors.Is(err, constants.ErrNotFound) {
		t.Fatalf("error = %v, want ErrNotFound", err)
	}
}

// TestDeletingReportsWhetherThereWasAnything.
func TestDeletingReportsWhetherThereWasAnything(t *testing.T) {
	repo, _ := deviceRepo(t)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if removed, err := repo.DeleteDevice(ctx); err != nil || removed {
		t.Fatalf("DeleteDevice() on an empty table = %v, %v; want false, nil", removed, err)
	}

	if _, err := repo.RegisterDevice(ctx, models.Device{
		Token: firstToken, Platform: constants.DevicePlatformAndroid,
	}); err != nil {
		t.Fatalf("register: %v", err)
	}
	if removed, err := repo.DeleteDevice(ctx); err != nil || !removed {
		t.Fatalf("DeleteDevice() = %v, %v; want true, nil", removed, err)
	}
	if _, err := repo.FetchDevice(ctx); !errors.Is(err, constants.ErrNotFound) {
		t.Errorf("the device survived deletion: %v", err)
	}
}

// TestTheStoredTimestampsAreUTC. Every timestamp in this system is UTC, and
// pgx hands one back in whatever zone the connection reports.
func TestTheStoredTimestampsAreUTC(t *testing.T) {
	repo, _ := deviceRepo(t)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	registered, err := repo.RegisterDevice(ctx, models.Device{
		Token: firstToken, Platform: constants.DevicePlatformAndroid,
	})
	if err != nil {
		t.Fatalf("register: %v", err)
	}

	for name, at := range map[string]time.Time{
		"registered_at": registered.RegisteredAt,
		"refreshed_at":  registered.RefreshedAt,
	} {
		if at.Location() != time.UTC {
			t.Errorf("%s is in %s, want UTC", name, at.Location())
		}
	}
}

// TestAnEmptyTokenIsRefusedByTheDatabase.
//
// The usecase refuses it first. This is the layer underneath: a statement that
// did not go through the usecase cannot store a row that would be sent to
// Firebase as an empty registration.
func TestAnEmptyTokenIsRefusedByTheDatabase(t *testing.T) {
	repo, _ := deviceRepo(t)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if _, err := repo.RegisterDevice(ctx, models.Device{
		Token: "", Platform: constants.DevicePlatformAndroid,
	}); err == nil {
		t.Fatal("an empty token was stored")
	}
}

// TestAnUnknownPlatformIsRefusedByTheDatabase.
func TestAnUnknownPlatformIsRefusedByTheDatabase(t *testing.T) {
	repo, _ := deviceRepo(t)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if _, err := repo.RegisterDevice(ctx, models.Device{
		Token: firstToken, Platform: constants.DevicePlatform("blackberry"),
	}); err == nil {
		t.Fatal("an unknown platform was stored")
	}
}

// TestAFailedRegistrationDoesNotQuoteTheToken.
//
// pgx quotes the row it refused in a constraint error, and that error is
// wrapped and logged. The token is the one credential here that can push to
// the owner's phone.
func TestAFailedRegistrationDoesNotQuoteTheToken(t *testing.T) {
	repo, _ := deviceRepo(t)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	_, err := repo.RegisterDevice(ctx, models.Device{
		Token: firstToken, Platform: constants.DevicePlatform("blackberry"),
	})
	if err == nil {
		t.Fatal("it was accepted")
	}
	if got := err.Error(); strings.Contains(got, firstToken) {
		t.Errorf("the error quotes the token:\n%s", got)
	}
}
