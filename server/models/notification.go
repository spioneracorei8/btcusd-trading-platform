package models

import (
	"time"

	"github.com/google/uuid"

	"github.com/spioneracorei8/btcusd-trading-platform/server/constants"
)

// Notification is one delivery attempt of one signal to the owner's phone.
type Notification struct {
	Id        int64
	SignalId  uuid.UUID
	Channel   constants.NotificationChannel
	Status    constants.NotificationStatus
	Attempts  int32
	LastError string
	SentAt    *time.Time
	CreatedAt time.Time

	// NextAttemptAt is when a failed delivery may be tried again. It is
	// stored rather than held by the worker so the backoff outlives the
	// process that decided it — a restart that forgot would retry at once.
	NextAttemptAt time.Time
}
