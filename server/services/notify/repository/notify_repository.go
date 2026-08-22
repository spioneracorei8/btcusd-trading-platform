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

type notifyRepository struct {
	queries *db.Queries
}

// NewNotifyRepoImpl builds the notification repository on a pgx pool.
func NewNotifyRepoImpl(pool *pgxpool.Pool) notify.NotifyRepository {
	return &notifyRepository{
		queries: db.New(pool),
	}
}

// InsertNotification queues one signal for delivery.
//
// The insert does nothing when the signal is already queued for the channel,
// and pgx reports a statement that returned no row as ErrNoRows. That is the
// idempotent case, not a failure: it is translated into queued=false so a
// caller offering the same signal twice — after a retry, or after a restart
// re-walked a bar — gets silence rather than a second alert or an error.
func (r *notifyRepository) InsertNotification(
	ctx context.Context, n models.Notification,
) (models.Notification, bool, error) {
	row, err := r.queries.InsertNotification(ctx, db.InsertNotificationParams{
		SignalID: database.PgtypeFromUUID(n.SignalId),
		Channel:  n.Channel.String(),
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return models.Notification{}, false, nil
	}
	if err != nil {
		return models.Notification{}, false, fmt.Errorf("queue notification: %w", err)
	}

	queued, err := toNotificationModel(row)
	if err != nil {
		return models.Notification{}, false, err
	}
	return queued, true, nil
}

// FetchPendingNotifications returns queued deliveries, oldest first.
func (r *notifyRepository) FetchPendingNotifications(
	ctx context.Context, limit int32,
) ([]models.Notification, error) {
	if limit <= 0 {
		return nil, fmt.Errorf("fetch pending notifications: limit %d is not positive", limit)
	}

	rows, err := r.queries.FetchPendingNotifications(ctx, limit)
	if err != nil {
		return nil, fmt.Errorf("fetch pending notifications: %w", err)
	}

	out := make([]models.Notification, 0, len(rows))
	for _, row := range rows {
		n, err := toNotificationModel(row)
		if err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, nil
}

// toNotificationModel converts one row, refusing a value the enums do not
// know rather than carrying it up as a string nothing will match on.
func toNotificationModel(row db.Notification) (models.Notification, error) {
	signalId, err := database.UUIDFromPgtype(row.SignalID)
	if err != nil {
		return models.Notification{}, fmt.Errorf("notification %d: signal_id: %w", row.ID, err)
	}
	status, err := constants.ParseNotificationStatus(row.Status)
	if err != nil {
		return models.Notification{}, fmt.Errorf("notification %d: %w", row.ID, err)
	}

	return models.Notification{
		Id:        row.ID,
		SignalId:  signalId,
		Channel:   constants.NotificationChannel(row.Channel),
		Status:    status,
		Attempts:  row.Attempts,
		LastError: row.LastError,
		SentAt:    database.TimePtrFromTimestamptz(row.SentAt),
		CreatedAt: database.TimeFromTimestamptz(row.CreatedAt),
	}, nil
}
