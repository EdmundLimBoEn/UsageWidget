package server

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os/exec"
	"strings"
	"time"
)

const crossUsageFetchTimeout = 250 * time.Second

type CrossUsageClient struct {
	URL        string
	Cmd        []string
	Source     string
	httpClient *http.Client
}

func NewCrossUsageHTTPClient(url string) *CrossUsageClient {
	return &CrossUsageClient{
		URL:        url,
		Source:     "crossusage-http",
		httpClient: &http.Client{Timeout: crossUsageFetchTimeout},
	}
}

func NewCrossUsageBinaryClient(binary string) *CrossUsageClient {
	return &CrossUsageClient{
		Cmd:    []string{binary},
		Source: "crossusage-cli",
	}
}

func NewCrossUsageCommandClient(command string) *CrossUsageClient {
	return &CrossUsageClient{Cmd: strings.Fields(command), Source: "crossusage-command"}
}

func NewCrossUsageCollectorClient(socketPath string) *CrossUsageClient {
	dialer := &net.Dialer{Timeout: 5 * time.Second}
	transport := &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return dialer.DialContext(ctx, "unix", socketPath)
		},
	}
	return &CrossUsageClient{
		URL:    "http://collector/usage",
		Source: "crossusage-collector",
		httpClient: &http.Client{
			Transport: transport,
			Timeout:   crossUsageFetchTimeout,
		},
	}
}

func (c *CrossUsageClient) SourceName() string { return c.Source }

func (c *CrossUsageClient) Fetch(ctx context.Context) ([]byte, error) {
	if len(c.Cmd) == 1 {
		body, err := CollectCrossUsageLimits(ctx, c.Cmd[0])
		if err != nil {
			return nil, c.failure("command failed", err.Error(), err)
		}
		return body, nil
	}
	if len(c.Cmd) > 0 {
		return c.fetchCmd(ctx)
	}
	if c.httpClient == nil || c.URL == "" {
		return nil, fmt.Errorf("crossusage: no command or HTTP transport configured")
	}
	return c.fetchHTTP(ctx)
}

func (c *CrossUsageClient) fetchCmd(ctx context.Context) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, crossUsageFetchTimeout)
	defer cancel()

	var stdout, stderr bytes.Buffer
	cmd := exec.CommandContext(ctx, c.Cmd[0], c.Cmd[1:]...)
	configureCommandCancellation(cmd)
	cmd.WaitDelay = 250 * time.Millisecond
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		if body, ok := extractJSONDocument(stdout.Bytes()); ok {
			return attachDevinLimits(ctx, body), nil
		}
		if ctx.Err() != nil {
			err = ctx.Err()
		}
		return nil, c.failure("command failed", stderr.String(), err)
	}
	body, ok := extractJSONDocument(stdout.Bytes())
	if !ok {
		return nil, fmt.Errorf("crossusage: invalid JSON")
	}
	return attachDevinLimits(ctx, body), nil
}

func (c *CrossUsageClient) fetchHTTP(ctx context.Context) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.URL, nil)
	if err != nil {
		return nil, c.failure("build request failed", "invalid request URL", nil)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, c.failure("fetch failed", "HTTP transport failed", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, collectorMaxResponseBytes+1))
	if err != nil {
		return nil, c.failure("read body failed", "HTTP response read failed", err)
	}
	if len(body) > collectorMaxResponseBytes {
		return nil, fmt.Errorf("crossusage: response too large")
	}
	if resp.StatusCode != http.StatusOK {
		failure := c.failure("unexpected HTTP status", fmt.Sprintf("status %d: %s", resp.StatusCode, body), nil)
		failure.retryable = resp.StatusCode == http.StatusRequestTimeout || resp.StatusCode == http.StatusGatewayTimeout
		return nil, failure
	}
	if extracted, ok := extractJSONDocument(body); ok {
		return attachDevinLimits(ctx, extracted), nil
	}
	return attachDevinLimits(ctx, body), nil
}

// crossUsageError carries retry policy without exposing the underlying diagnostic.
type crossUsageError struct {
	message   string
	retryable bool
}

func (e *crossUsageError) Error() string { return e.message }

func isRetryablePollError(err error) bool {
	var fetchErr *crossUsageError
	if errors.As(err, &fetchErr) {
		return fetchErr.retryable
	}
	if err == nil || errors.Is(err, context.Canceled) {
		return false
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
		return true
	}
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return true
	}
	return retryablePollError(err.Error())
}

// Diagnostics stay in server logs; returned errors are safe for poll outcomes and APIs.
func (c *CrossUsageClient) failure(category, diagnostic string, cause error) *crossUsageError {
	retryable := isRetryablePollError(cause)
	// Command stderr can describe transient failures even when exit status cannot.
	if category == "command failed" && !errors.Is(cause, context.Canceled) {
		retryable = retryable || retryablePollError(diagnostic)
	}
	for _, arg := range c.Cmd {
		if arg != "" {
			diagnostic = strings.ReplaceAll(diagnostic, arg, "[redacted]")
		}
	}
	log.Printf("crossusage: %s: %s", category, truncateDiagnostic(diagnostic, 180))
	return &crossUsageError{message: "crossusage: " + category, retryable: retryable}
}
