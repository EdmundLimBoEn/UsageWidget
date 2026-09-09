package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os/exec"
	"strings"
	"sync"
	"time"
)

const collectorMaxResponseBytes = 4 << 20

type Collector struct {
	Binary  string
	Args    []string
	Timeout time.Duration

	mu sync.Mutex
}

func NewCollector(binary string) *Collector {
	return NewCollectorWithArgs(binary, nil)
}

func NewCollectorWithArgs(binary string, args []string) *Collector {
	return &Collector{Binary: binary, Args: args, Timeout: 240 * time.Second}
}

func (c *Collector) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /usage", c.handleUsage)
	return mux
}

func (c *Collector) handleUsage(w http.ResponseWriter, r *http.Request) {
	c.mu.Lock()
	defer c.mu.Unlock()

	timeout := c.Timeout
	if timeout <= 0 {
		timeout = 240 * time.Second
	}
	ctx, cancel := context.WithTimeout(r.Context(), timeout)
	defer cancel()

	if len(c.Args) == 0 {
		body, err := CollectCrossUsageLimits(ctx, c.Binary)
		if err != nil {
			if ctx.Err() != nil {
				http.Error(w, "collector timeout", http.StatusGatewayTimeout)
				return
			}
			http.Error(w, truncateDiagnostic(err.Error(), 180), http.StatusServiceUnavailable)
			return
		}
		writeCollectedJSON(w, body)
		return
	}

	var stdout, stderr bytes.Buffer
	cmd := exec.CommandContext(ctx, c.Binary, c.Args...)
	configureCommandCancellation(cmd)
	cmd.WaitDelay = 250 * time.Millisecond
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	runErr := cmd.Run()
	if runErr != nil {
		if ctx.Err() != nil {
			http.Error(w, "collector timeout", http.StatusGatewayTimeout)
			return
		}
	}
	if stdout.Len() > collectorMaxResponseBytes {
		http.Error(w, "collector response too large", http.StatusBadGateway)
		return
	}
	body, ok := extractJSONDocument(stdout.Bytes())
	if !ok {
		if runErr != nil {
			http.Error(w, classifyCollectorFailure(stderr.String()), http.StatusServiceUnavailable)
			return
		}
		http.Error(w, "collector returned invalid JSON", http.StatusBadGateway)
		return
	}
	writeCollectedJSON(w, body)
}

func writeCollectedJSON(w http.ResponseWriter, body []byte) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("X-UsageWidget-Collected-At", time.Now().UTC().Format(time.RFC3339))
	_, _ = w.Write(body)
}

func classifyCollectorFailure(stderr string) string {
	lower := strings.ToLower(stderr)
	switch {
	case strings.Contains(lower, "rate limit"), strings.Contains(lower, "overload"), strings.Contains(lower, "too many requests"):
		return "collector rate limited"
	case strings.Contains(lower, "unauthorized"), strings.Contains(lower, "authentication"), strings.Contains(lower, "login"):
		return "collector authentication required"
	case strings.TrimSpace(stderr) == "":
		return "collector command failed"
	default:
		return fmt.Sprintf("collector command failed: %s", truncateDiagnostic(stderr, 180))
	}
}

func truncateDiagnostic(value string, limit int) string {
	value = strings.Join(strings.Fields(value), " ")
	if len(value) <= limit {
		return value
	}
	return value[:limit] + "…"
}

func extractJSONDocument(raw []byte) ([]byte, bool) {
	s := bytes.TrimSpace(raw)
	if len(s) == 0 {
		return nil, false
	}
	if json.Valid(s) {
		return s, true
	}
	iObj := bytes.IndexByte(s, '{')
	iArr := bytes.IndexByte(s, '[')
	start := -1
	switch {
	case iObj >= 0 && (iArr < 0 || iObj < iArr):
		start = iObj
	case iArr >= 0:
		start = iArr
	default:
		return nil, false
	}
	for end := len(s); end > start; end-- {
		chunk := bytes.TrimSpace(s[start:end])
		if json.Valid(chunk) {
			return chunk, true
		}
	}
	return nil, false
}
