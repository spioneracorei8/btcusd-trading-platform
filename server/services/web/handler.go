// Package web serves the built web app from the same origin as the API.
//
// # Why this is a handler with nothing under it
//
// Every other service here has a usecase holding a rule — "an unclosed candle
// is never stored", "an outcome only moves out of open". Serving bytes off
// disk has no such rule, so there is nothing for a usecase to hold and no
// repository to read through. The interface is declared here for the same
// reason as the others: routes/ names this and never learns what implements
// it.
package web

import "net/http"

// AppHandler serves the built web app.
type AppHandler interface {
	// App serves one file, or the app's entry document for a path that
	// belongs to the app's own routing.
	App(w http.ResponseWriter, r *http.Request)
}
