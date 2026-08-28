package notify

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/shopspring/decimal"

	"github.com/spioneracorei8/btcusd-trading-platform/server/constants"
	"github.com/spioneracorei8/btcusd-trading-platform/server/models"
	"github.com/spioneracorei8/btcusd-trading-platform/server/services/signal"
)

// ErrUndeliverable marks a rejection that retrying cannot fix.
//
// # Why permanent and transient are told apart
//
// A subscription the push service has expired is gone, and a malformed payload
// will be malformed on every attempt. Spending five tries and several minutes
// of backoff on either one delays every alert queued behind it, and then
// records "gave up after 5 attempts" — which reads like a network problem and
// sends whoever investigates to the wrong place.
var ErrUndeliverable = errors.New("notify: the message cannot be delivered")

// Sender delivers one message to the owner's device.
//
// It is a repository: a client that talks to something outside the process,
// whose vendor payloads never leave its own package.
type Sender interface {
	// Send delivers one message. It returns an error wrapping
	// ErrUndeliverable when retrying would not help.
	Send(ctx context.Context, message Message) error

	// Channel is the delivery target this sender serves, so a queue row can
	// be matched to the sender that owns it.
	Channel() constants.NotificationChannel
}

// Message is what the owner sees, built from a signal.
type Message struct {
	// To is the subscription to deliver to.
	To models.PushSubscription

	Title string
	Body  string

	// Data travels alongside the text so the app can act on the signal
	// without parsing a sentence written for a human.
	Data map[string]string
}

// BuildMessage renders one signal for the owner's phone.
//
// # Why the price shown is a reference and not an entry
//
// A signal is decided on a bar's close, and a position could not have opened
// at that close — the entry is the next bar's open plus slippage, which this
// moment does not know. The alert still goes out now, because the owner needs
// the news immediately and holding it until the next bar closed would delay it
// by a whole timeframe. So the number quoted is labelled as a reference: it is
// what the strategy saw, not what anything was bought at.
//
// A reason that will not parse does not stop the alert. The owner losing a
// notification because a jsonb column disagreed with this struct would be the
// wrong trade — the trigger line is dropped and the rest is sent.
func BuildMessage(to models.PushSubscription, s models.Signal) Message {
	trigger := ""
	if reason, err := signal.DecodeReason(s.Reason); err == nil {
		trigger = reason.Trigger
	}

	return Message{
		To: to,
		Title: fmt.Sprintf("%s %s %s",
			s.Symbol, s.Timeframe, strings.ToUpper(s.Direction.String())),
		Body: messageBody(s, trigger),
		Data: messageData(s, trigger),
	}
}

// messageBody is the one line the owner reads on a lock screen.
func messageBody(s models.Signal, trigger string) string {
	parts := make([]string, 0, 4)
	if s.SignalPrice.Valid {
		parts = append(parts, "ref "+display(s.SignalPrice.Decimal))
	}
	if s.StopLoss.Valid {
		parts = append(parts, "stop "+display(s.StopLoss.Decimal))
	}
	if s.TakeProfit.Valid {
		parts = append(parts, "target "+display(s.TakeProfit.Decimal))
	}

	line := strings.Join(parts, " · ")
	if trigger == "" {
		return line
	}
	if line == "" {
		return trigger
	}
	return line + " — " + trigger
}

// messageData is the same signal in a form the app can act on.
//
// Prices here are exact. The body rounds for a human to read at a glance, and
// a number shown to a person is not one anything should compute with.
func messageData(s models.Signal, trigger string) map[string]string {
	data := map[string]string{
		"signal_id":        s.Id.String(),
		"symbol":           s.Symbol,
		"market_type":      s.MarketType.String(),
		"timeframe":        s.Timeframe.String(),
		"direction":        s.Direction.String(),
		"signal_time":      s.SignalTime.UTC().Format(time.RFC3339),
		"strategy":         s.StrategyName,
		"strategy_version": s.StrategyVersion,
	}
	if trigger != "" {
		data["trigger"] = trigger
	}

	// The price the strategy decided on, named so nothing downstream mistakes
	// it for a fill.
	for key, level := range map[string]decimal.NullDecimal{
		"signal_price": s.SignalPrice,
		"stop_loss":    s.StopLoss,
		"take_profit":  s.TakeProfit,
		"entry_price":  s.EntryPrice,
	} {
		if level.Valid {
			data[key] = level.Decimal.String()
		}
	}
	return data
}

// display rounds a price for a person reading one line on a lock screen.
func display(d decimal.Decimal) string { return d.Round(2).String() }
