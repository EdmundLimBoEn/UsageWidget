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

// OpenUsageClient polls OpenUsage via CLI export, hub HTTP, or daemon socket.
type OpenUsageClient struct {
	URL        string
	Cmd        []string
	Source     string
	httpClient *http.Client
}

func NewOpenUsageHTTPClient(url string) *OpenUsageClient {
	return &OpenUsageClient{
		URL:        url,
		Source:     "openusage-hub",
		httpClient: &http.Client{Timeout: 60 * time.Second},
	}
}

func NewOpenUsageBinaryClient(binary string) *OpenUsageClient {
	return &OpenUsageClient{
		Cmd:    []string{binary},
		Source: "openusage-cli",
	}
}

func NewOpenUsageCommandClient(command string) *OpenUsageClient {
	return &OpenUsageClient{Cmd: strings.Fields(command), Source: "openusage-command"}
}

func NewOpenUsageUnixClient(socketPath string) *OpenUsageClient {
	return newOpenUsageUnixClient(socketPath, "http://openusage/v1/read-model", "openusage-daemon")
}

// NewOpenUsageCollectorClient talks to UsageWidget's collector sidecar, which
// exposes OpenUsage export JSON (or CodexBar JSON) at GET /usage.
func NewOpenUsageCollectorClient(socketPath string) *OpenUsageClient {
	return newOpenUsageUnixClient(socketPath, "http://collector/usage", "openusage-collector")
}

func newOpenUsageUnixClient(socketPath, url, source string) *OpenUsageClient {
	dialer := &net.Dialer{Timeout: 5 * time.Second}
	transport := &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return dialer.DialContext(ctx, "unix", socketPath)
		},
	}
	return &OpenUsageClient{
		URL:    url,
		Source: source,
		httpClient: &http.Client{
			Transport: transport,
			Timeout:   95 * time.Second,
		},
	}
}

func (c *OpenUsageClient) SourceName() string { return c.Source }

func (c *OpenUsageClient) Fetch(ctx context.Context) ([]byte, error) {
	if len(c.Cmd) == 1 {
		return CollectOpenUsageLimits(ctx, c.Cmd[0])
	}
	if len(c.Cmd) > 0 {
		return c.fetchCmd(ctx)
	}
	if c.httpClient == nil || c.URL == "" {
		return nil, fmt.Errorf("openusage: no command or HTTP transport configured")
	}
	return c.fetchHTTP(ctx)
}

func (c *OpenUsageClient) fetchCmd(ctx context.Context) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, 120*time.Second)
	defer cancel()

	var stdout, stderr bytes.Buffer
	cmd := exec.CommandContext(ctx, c.Cmd[0], c.Cmd[1:]...)
	configureCommandCancellation(cmd)
	cmd.WaitDelay = 250 * time.Millisecond
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("openusage: run %q: %w: %s", strings.Join(c.Cmd, " "), err, strings.TrimSpace(stderr.String()))
	}
	return stdout.Bytes(), nil
}

func (c *OpenUsageClient) fetchHTTP(ctx context.Context) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.URL, nil)
	if err != nil {
		return nil, fmt.Errorf("openusage: build request: %w", err)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("openusage: fetch: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, collectorMaxResponseBytes+1))
	if err != nil {
		return nil, fmt.Errorf("openusage: read body: %w", err)
	}
	if len(body) > collectorMaxResponseBytes {
		return nil, fmt.Errorf("openusage: response too large")
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("openusage: unexpected status %d: %s", resp.StatusCode, string(body))
	}
	return body, nil
}
