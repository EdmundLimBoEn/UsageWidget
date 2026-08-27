package server

import "context"

// UsageSource fetches raw provider usage JSON (OpenUsage export or CodexBar).
type UsageSource interface {
	Fetch(ctx context.Context) ([]byte, error)
	SourceName() string
}
