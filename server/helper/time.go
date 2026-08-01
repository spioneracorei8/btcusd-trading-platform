package helper

import "time"

// UTC normalises t to UTC.
//
// Every timestamp in this system is UTC. Local time is never stored, logged
// or compared: a candle's open time has to mean the same instant on the VPS,
// on the phone and in a backtest run months later.
func UTC(t time.Time) time.Time { return t.UTC() }

// NowUTC returns the current time in UTC.
func NowUTC() time.Time { return time.Now().UTC() }
