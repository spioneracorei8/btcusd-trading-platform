package webpush

import (
	"github.com/spioneracorei8/btcusd-trading-platform/server/services/notify"
)

// payload is what the service worker receives and renders.
//
// # Why this shape and not the Message struct
//
// It is a wire format read by public/sw.js, so it is defined where it is sent
// from and converted at the boundary — the same rule the Binance client
// follows in the other direction. The field names are what the worker reads;
// renaming one here silently stops notifications rendering, which is why
// there is a test on both sides.
type payload struct {
	Title string `json:"title"`
	Body  string `json:"body"`

	// Data is the signal in a form the app can act on, including the id the
	// notification click navigates to.
	Data map[string]string `json:"data"`
}

// toPayload renders a message for the wire.
func toPayload(m notify.Message) payload {
	return payload{
		Title: m.Title,
		Body:  m.Body,
		Data:  m.Data,
	}
}
