package helper

import "time"

// UTC normalises t to UTC.
//
// Every timestamp in this system is UTC. Local time is never stored, logged
// or compared: a candle's open time has to mean the same instant on the VPS,
// on the phone and in a backtest run months later. Every value crossing the
// database boundary goes through here, so that rule lives in one place.
func UTC(t time.Time) time.Time { return t.UTC() }
