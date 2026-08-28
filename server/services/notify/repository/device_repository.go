package repository

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/spioneracorei8/btcusd-trading-platform/server/constants"
	"github.com/spioneracorei8/btcusd-trading-platform/server/database"
	"github.com/spioneracorei8/btcusd-trading-platform/server/database/db"
	"github.com/spioneracorei8/btcusd-trading-platform/server/models"
	"github.com/spioneracorei8/btcusd-trading-platform/server/services/notify"
)

type deviceRepository struct {
	queries *db.Queries
}

// NewDeviceRepoImpl builds the device registration repository on a pgx pool.
func NewDeviceRepoImpl(pool *pgxpool.Pool) notify.DeviceRepository {
	return &deviceRepository{queries: db.New(pool)}
}

// RegisterDevice records the phone to deliver to, replacing whatever was there.
func (r *deviceRepository) RegisterDevice(
	ctx context.Context, d models.Device,
) (models.Device, error) {
	row, err := r.queries.RegisterDevice(ctx, db.RegisterDeviceParams{
		Endpoint: d.Subscription.Endpoint,
		P256dh:   d.Subscription.P256dh,
		Auth:     d.Subscription.Auth,
		Platform: d.Platform.String(),
		Label:    d.Label,
	})
	if err != nil {
		// The subscription itself must not reach the message. A failed insert
		// is usually a constraint, and a constraint error from pgx quotes the
		// row it refused — which here would be the keys anything could push
		// to this phone with.
		return models.Device{}, fmt.Errorf("register device %s: %w", d.MaskedEndpoint(), err)
	}
	return toDeviceModel(row)
}

// FetchDevice returns the registered device.
func (r *deviceRepository) FetchDevice(ctx context.Context) (models.Device, error) {
	row, err := r.queries.FetchDevice(ctx)
	if errors.Is(err, pgx.ErrNoRows) {
		// No phone has registered. Ordinary before the app is first opened,
		// so it is a sentinel the caller can recognise rather than an error
		// string it has to match on.
		return models.Device{}, constants.ErrNotFound
	}
	if err != nil {
		return models.Device{}, fmt.Errorf("fetch device: %w", err)
	}
	return toDeviceModel(row)
}

// DeleteDevice forgets the registration, reporting whether there was one.
func (r *deviceRepository) DeleteDevice(ctx context.Context) (bool, error) {
	removed, err := r.queries.DeleteDevice(ctx)
	if err != nil {
		return false, fmt.Errorf("delete device: %w", err)
	}
	return removed > 0, nil
}

// toDeviceModel converts the stored row.
func toDeviceModel(row db.Device) (models.Device, error) {
	platform, err := constants.ParseDevicePlatform(row.Platform)
	if err != nil {
		// The check constraint should make this unreachable. It is reported
		// rather than defaulted because a platform nothing recognises means
		// the table and this code have diverged, and guessing android would
		// hide that.
		return models.Device{}, fmt.Errorf("stored device: %w", err)
	}

	return models.Device{
		Subscription: models.PushSubscription{
			Endpoint: row.Endpoint,
			P256dh:   row.P256dh,
			Auth:     row.Auth,
		},
		Platform:     platform,
		Label:        row.Label,
		RegisteredAt: database.TimeFromTimestamptz(row.RegisteredAt),
		RefreshedAt:  database.TimeFromTimestamptz(row.RefreshedAt),
	}, nil
}
