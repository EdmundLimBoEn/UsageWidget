package server

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"os/exec"
	"strings"
	"time"
)

type CodexBarClient struct {
	URL        string
	Cmd        []string
	httpClient *http.Client
}

func NewCodexBarClient(url string) *CodexBarClient {
	// Prefer all enabled providers when the caller only passed the bare /usage path.
	if !strings.Contains(url, "?") {
		url = strings.TrimRight(url, "/") + "?provider=all"
	}
	return &CodexBarClient{
		URL: url,
		// CodexBar can take a while when providers re-auth / scrape dashboards.
		httpClient: &http.Client{Timeout: 60 * time.Second},
	}
}

// NewCodexBarCommandClient runs the CodexBar CLI (e.g. "codexbar usage --json")
// instead of hitting the serve endpoint. The CLI honors in-app provider
// toggles, so only enabled providers appear in the output.
func NewCodexBarCommandClient(command string) *CodexBarClient {
	return &CodexBarClient{Cmd: strings.Fields(command)}
}

func (c *CodexBarClient) Fetch(ctx context.Context) ([]byte, error) {
	if len(c.Cmd) > 0 {
		return c.fetchCmd(ctx)
	}
	return c.fetchHTTP(ctx)
}

func (c *CodexBarClient) fetchCmd(ctx context.Context) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, 120*time.Second)
	defer cancel()

	var stdout, stderr bytes.Buffer
	cmd := exec.CommandContext(ctx, c.Cmd[0], c.Cmd[1:]...)
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("codexbar: run %q: %w: %s", strings.Join(c.Cmd, " "), err, strings.TrimSpace(stderr.String()))
	}
	return stdout.Bytes(), nil
}

func (c *CodexBarClient) fetchHTTP(ctx context.Context) ([]byte, error) {
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
