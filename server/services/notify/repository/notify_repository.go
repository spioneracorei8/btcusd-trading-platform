package repository

import (
	"context"
	"errors"
	"fmt"
	"time"

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

// FetchDueNotifications returns queued deliveries that may be attempted now.
func (r *notifyRepository) FetchDueNotifications(
	ctx context.Context, asOf time.Time, limit int32,
) ([]models.Notification, error) {
	if limit <= 0 {
		return nil, fmt.Errorf("fetch due notifications: limit %d is not positive", limit)
	}

	rows, err := r.queries.FetchDueNotifications(ctx, db.FetchDueNotificationsParams{
		AsOf:     database.TimestamptzFromTime(asOf),
		RowLimit: limit,
	})
	if err != nil {
		return nil, fmt.Errorf("fetch due notifications: %w", err)
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

// MarkNotificationSent records a delivery that worked.
func (r *notifyRepository) MarkNotificationSent(
	ctx context.Context, id int64, sentAt time.Time,
) (models.Notification, error) {
	row, err := r.queries.MarkNotificationSent(ctx, db.MarkNotificationSentParams{
		ID:     id,
		SentAt: database.TimestamptzFromTime(sentAt),
	})
	return finished(row, err, "mark notification sent", id)
}

// RescheduleNotification records a failed attempt worth repeating.
func (r *notifyRepository) RescheduleNotification(
	ctx context.Context, id int64, lastError string, nextAttemptAt time.Time,
) (models.Notification, error) {
	row, err := r.queries.RescheduleNotification(ctx, db.RescheduleNotificationParams{
		ID:            id,
		LastError:     truncateError(lastError),
		NextAttemptAt: database.TimestamptzFromTime(nextAttemptAt),
	})
	return finished(row, err, "reschedule notification", id)
}

// FailNotification gives up on a delivery.
func (r *notifyRepository) FailNotification(
	ctx context.Context, id int64, lastError string,
) (models.Notification, error) {
	row, err := r.queries.FailNotification(ctx, db.FailNotificationParams{
		ID:        id,
		LastError: truncateError(lastError),
	})
	return finished(row, err, "fail notification", id)
}

// finished converts the row one of the three updates returned.
//
// An update that matched nothing is reported rather than returned as a zero
// notification: the row was deleted, or the id was never real, and either way
// the caller believes it just recorded an outcome that nothing recorded.
func finished(
	row db.Notification, err error, operation string, id int64,
) (models.Notification, error) {
	if errors.Is(err, pgx.ErrNoRows) {
		return models.Notification{}, fmt.Errorf("%s: no notification %d", operation, id)
	}
	if err != nil {
		return models.Notification{}, fmt.Errorf("%s %d: %w", operation, id, err)
	}
	return toNotificationModel(row)
}

// truncateError bounds what is written to last_error.
//
// The column is read by a person trying to understand a silence. A rejection
// body from a third party has no length limit, and one long enough to be
// unreadable explains nothing.
func truncateError(text string) string {
	if len(text) <= constants.NotifyErrorBodyLimit {
		return text
	}
	return text[:constants.NotifyErrorBodyLimit] + "…"
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
		Id:            row.ID,
		SignalId:      signalId,
		Channel:       constants.NotificationChannel(row.Channel),
		Status:        status,
		Attempts:      row.Attempts,
		LastError:     row.LastError,
		SentAt:        database.TimePtrFromTimestamptz(row.SentAt),
		CreatedAt:     database.TimeFromTimestamptz(row.CreatedAt),
		NextAttemptAt: database.TimeFromTimestamptz(row.NextAttemptAt),
	}, nil
}
