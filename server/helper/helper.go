// Package helper holds small, dependency-free utilities shared by the other
// packages. Anything here must be pure: no I/O, no global state.
package helper

import "strings"

// LookupFunc resolves an environment variable by name, mirroring os.LookupEnv.
type LookupFunc func(key string) (string, bool)

// LookupENV reads a variable through lookup and reports whether it holds a
// non-blank value.
//
// A variable set to whitespace is treated as unset: an empty value in a .env
// file almost always means "I have not filled this in yet", and silently
// accepting it is how a deployment ends up pointed at an empty DSN.
//
// Taking the lookup as a parameter lets tests supply an environment without
// mutating the process one.
func LookupENV(lookup LookupFunc, key string) (string, bool) {
	raw, ok := lookup(key)
	if !ok {
		return "", false
	}
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return "", false
	}
	return trimmed, true
}
