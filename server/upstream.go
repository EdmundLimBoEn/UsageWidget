package server

import "context"

// UsageSource fetches raw CrossUsage limits JSON.
type UsageSource interface {
	Fetch(ctx context.Context) ([]byte, error)
	SourceName() string
}
