package fcm

import "github.com/spioneracorei8/btcusd-trading-platform/server/services/notify"

// The FCM v1 send API's request and response shapes.
//
// These are Google's, not this system's. They stay unexported and inside this
// package so that a change at Google is a change to one file, and so nothing
// upstream ends up shaped by somebody else's JSON.

type sendRequest struct {
	Message fcmMessage `json:"message"`
}

type fcmMessage struct {
	Token        string            `json:"token"`
	Notification fcmNotification   `json:"notification"`
	Data         map[string]string `json:"data,omitempty"`
	Android      *fcmAndroid       `json:"android,omitempty"`
	APNS         *fcmAPNS          `json:"apns,omitempty"`
}

type fcmNotification struct {
	Title string `json:"title"`
	Body  string `json:"body"`
}

type fcmAndroid struct {
	Priority string `json:"priority"`
}

type fcmAPNS struct {
	Headers map[string]string `json:"headers"`
}

type errorResponse struct {
	Error struct {
		Status  string `json:"status"`
		Message string `json:"message"`
	} `json:"error"`
}

// toFCM converts the system's message into Google's shape.
//
// Both platforms are told the message is time-critical. A scalping signal that
// arrives when the phone next wakes is not a signal; it is a note about
// something that already happened.
func toFCM(m notify.Message) fcmMessage {
	return fcmMessage{
		Token:        m.Token,
		Notification: fcmNotification{Title: m.Title, Body: m.Body},
		Data:         m.Data,
		Android:      &fcmAndroid{Priority: "high"},
		APNS:         &fcmAPNS{Headers: map[string]string{"apns-priority": "10"}},
	}
}
