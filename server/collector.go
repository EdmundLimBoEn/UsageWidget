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
	"syscall"
	"time"
)

const collectorMaxResponseBytes = 4 << 20

// Collector executes exactly one configured binary with fixed CodexBar usage
// arguments. Requests serialize so browser/app refreshes cannot fan out into
// concurrent provider scrapes.
type Collector struct {
	Binary  string
	Timeout time.Duration

	mu sync.Mutex
}

func NewCollector(binary string) *Collector {
	return &Collector{Binary: binary, Timeout: 90 * time.Second}
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

	var stdout, stderr bytes.Buffer
	cmd := exec.CommandContext(ctx, c.Binary, "usage", "--json")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
	cmd.WaitDelay = 2 * time.Second
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
