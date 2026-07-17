package server

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"time"
)

type CodexBarClient struct {
	URL        string
	httpClient *http.Client
}

func NewCodexBarClient(url string) *CodexBarClient {
	return &CodexBarClient{
		URL:        url,
		httpClient: &http.Client{Timeout: 10 * time.Second},
	}
}

func (c *CodexBarClient) Fetch(ctx context.Context) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.URL, nil)
	if err != nil {
		return nil, fmt.Errorf("codexbar: build request: %w", err)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("codexbar: fetch: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("codexbar: read body: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("codexbar: unexpected status %d: %s", resp.StatusCode, string(body))
	}

	return body, nil
}
