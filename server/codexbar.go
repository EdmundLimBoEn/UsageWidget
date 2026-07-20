package server

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"os/exec"
	"strings"
	"time"
)

type CodexBarClient struct {
	URL        string
	Cmd        []string
	Source     string
	httpClient *http.Client
}

// NewCodexBarClient polls a CodexBar serve endpoint. The bare /usage path
// honors in-app provider toggles — do not force ?provider=all.
func NewCodexBarClient(url string) *CodexBarClient {
	return &CodexBarClient{
		URL:    url,
		Source: "http",
		// CodexBar can take a while when providers re-auth / scrape dashboards.
		httpClient: &http.Client{Timeout: 60 * time.Second},
	}
}

// NewCodexBarCommandClient runs the CodexBar CLI (e.g. "codexbar usage --json")
// instead of hitting the serve endpoint. The CLI honors in-app provider
// toggles, so only enabled providers appear in the output.
func NewCodexBarCommandClient(command string) *CodexBarClient {
	return &CodexBarClient{Cmd: strings.Fields(command), Source: "command"}
}

// NewCodexBarBinaryClient executes one exact binary path with fixed arguments.
// Unlike the legacy CODEXBAR_CMD form, this works with spaces in macOS and
// Windows paths and does not involve a shell.
func NewCodexBarBinaryClient(binary string) *CodexBarClient {
	return &CodexBarClient{
		Cmd:    []string{binary, "usage", "--format", "json"},
		Source: "codexbar-cli",
	}
}

// NewCodexBarUnixClient reads fresh CodexBar CLI output from the isolated
// collector sidecar. The socket is never exposed over TCP.
func NewCodexBarUnixClient(socketPath string) *CodexBarClient {
	dialer := &net.Dialer{Timeout: 5 * time.Second}
	transport := &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return dialer.DialContext(ctx, "unix", socketPath)
		},
	}
	return &CodexBarClient{
		URL:    "http://collector/usage",
		Source: "codexbar-cli-sidecar",
		httpClient: &http.Client{
			Transport: transport,
			Timeout:   95 * time.Second,
		},
	}
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
	configureCommandCancellation(cmd)
	cmd.WaitDelay = 250 * time.Millisecond
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
