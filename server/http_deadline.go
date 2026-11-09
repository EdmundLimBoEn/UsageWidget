package server

import (
	"net/http"
	"time"
)

// WithManualPollWriteDeadline allows a manual poll to finish its shared collection
// budget and write the response without extending deadlines for other endpoints.
func WithManualPollWriteDeadline(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && r.URL.Path == "/v1/poll" {
			// The daemon's net/http ResponseWriter supports write deadlines.
			_ = http.NewResponseController(w).SetWriteDeadline(time.Now().Add(sharedPollTimeout + 15*time.Second))
		}
		next.ServeHTTP(w, r)
	})
}
