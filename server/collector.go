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

// Collector executes one configured binary with fixed usage/export arguments.
// Requests serialize so browser/app refreshes cannot fan out into concurrent scrapes.
type Collector struct {
	Binary  string
	Source  string
	Args    []string
	Timeout time.Duration

	mu sync.Mutex
}

func NewCollector(binary string) *Collector {
	return NewCollectorWithArgs(binary, nil)
}

func NewCollectorWithArgs(binary string, args []string) *Collector {
	return NewCollectorForSource(binary, "", args)
}

func NewCollectorForSource(binary, source string, args []string) *Collector {
	if len(args) == 0 {
		args = defaultCollectorArgs(binary, source)
	}
	return &Collector{Binary: binary, Source: source, Args: args, Timeout: 90 * time.Second}
}

func defaultCollectorArgs(binary, source string) []string {
	src := strings.ToLower(strings.TrimSpace(source))
	switch src {
	case "openusage":
		return nil
	case "codexbar":
		return []string{"usage", "--format", "json"}
	}
	if strings.Contains(strings.ToLower(binary), "openusage") {
		return nil
	}
	return []string{"usage", "--format", "json"}
}

func (c *Collector) collectOpenUsageLimits() bool {
	if len(c.Args) > 0 {
		return false
	}
	src := strings.ToLower(strings.TrimSpace(c.Source))
	if src == "openusage" {
		return true
	}
	return src == "" && strings.Contains(strings.ToLower(c.Binary), "openusage")
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
		timeout = 90 * time.Second
	}
	ctx, cancel := context.WithTimeout(r.Context(), timeout)
	defer cancel()

	if c.collectOpenUsageLimits() {
		body, err := CollectOpenUsageLimits(ctx, c.Binary)
		if err != nil {
			if ctx.Err() != nil {
				http.Error(w, "collector timeout", http.StatusGatewayTimeout)
				return
			}
			http.Error(w, truncateDiagnostic(err.Error(), 180), http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-UsageWidget-Collected-At", time.Now().UTC().Format(time.RFC3339))
		_, _ = w.Write(body)
		return
	}

	args := c.Args
	if len(args) == 0 {
		args = defaultCollectorArgs(c.Binary, c.Source)
	}

	var stdout, stderr bytes.Buffer
	cmd := exec.CommandContext(ctx, c.Binary, args...)
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
	if !json.Valid(stdout.Bytes()) {
		if runErr != nil {
			detail := classifyCollectorFailure(stderr.String())
			http.Error(w, detail, http.StatusServiceUnavailable)
			return
		}
		http.Error(w, "collector returned invalid JSON", http.StatusBadGateway)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("X-UsageWidget-Collected-At", time.Now().UTC().Format(time.RFC3339))
	_, _ = w.Write(stdout.Bytes())
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
