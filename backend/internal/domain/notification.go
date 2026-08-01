package domain

import (
	"fmt"
	"time"

	"github.com/google/uuid"
)

// NotificationChannel is a delivery target for a signal.
type NotificationChannel string

// Supported notification channels.
const (
	NotificationChannelFCM NotificationChannel = "fcm"
)

// String returns the wire/database representation of the channel.
func (c NotificationChannel) String() string { return string(c) }

// NotificationStatus is the delivery state of a notification attempt.
type NotificationStatus string

// Supported notification statuses.
const (
	NotificationStatusPending NotificationStatus = "pending"
	NotificationStatusSent    NotificationStatus = "sent"
	NotificationStatusFailed  NotificationStatus = "failed"
)

// Valid reports whether s is a known notification status.
func (s NotificationStatus) Valid() bool {
	switch s {
	case NotificationStatusPending, NotificationStatusSent, NotificationStatusFailed:
		return true
	default:
		return false
	}
}

// String returns the wire/database representation of the status.
func (s NotificationStatus) String() string { return string(s) }

// ParseNotificationStatus converts s into a NotificationStatus, rejecting
// unknown values.
func ParseNotificationStatus(s string) (NotificationStatus, error) {
	st := NotificationStatus(s)
	if !st.Valid() {
		return "", fmt.Errorf("unknown notification status %q", s)
	}
	return st, nil
}

// Notification is one delivery attempt of one signal to the owner's phone.
type Notification struct {
	ID        int64
	SignalID  uuid.UUID
	Channel   NotificationChannel
	Status    NotificationStatus
	Attempts  int32
	LastError string
	SentAt    *time.Time
	CreatedAt time.Time
}
