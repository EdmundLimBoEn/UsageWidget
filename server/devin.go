package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"strings"
	"time"
)

// GetTeamCreditBalance is the documented Cognition team remaining-credits API:
// POST https://server.codeium.com/api/v1/GetTeamCreditBalance
// with {"service_key": "..."} and Billing Read permission.
const devinCreditBalanceURL = "https://server.codeium.com/api/v1/GetTeamCreditBalance"

const (
	devinServiceKeyEnv = "DEVIN_SERVICE_KEY"
	devinFetchTimeout  = 30 * time.Second
)

type devinCollector struct {
	key        string
	url        string
	httpClient *http.Client
}

func newDevinCollectorFromEnv() *devinCollector {
	key := strings.TrimSpace(os.Getenv(devinServiceKeyEnv))
	if key == "" {
		return nil
	}
	return newDevinCollector(key, devinCreditBalanceURL, nil)
}

func newDevinCollector(key, url string, client *http.Client) *devinCollector {
	if strings.TrimSpace(key) == "" {
		return nil
	}
	if url == "" {
		url = devinCreditBalanceURL
	}
	if client == nil {
		client = &http.Client{Timeout: devinFetchTimeout}
	}
	return &devinCollector{key: key, url: url, httpClient: client}
}

func attachDevinLimits(ctx context.Context, body []byte) []byte {
	return attachDevinLimitsWith(ctx, body, newDevinCollectorFromEnv())
}

func attachDevinLimitsWith(ctx context.Context, body []byte, col *devinCollector) []byte {
	if col == nil {
		return body
	}
	env, ok := limitsEnvelopeFromBody(body)
	if !ok {
		return body
	}
	provider, errMsg, err := col.fetch(ctx)
	stripCatalogProvider(&env, "devin")
	if provider != nil {
		env.Providers["devin"] = *provider
	} else {
		msg := strings.TrimSpace(errMsg)
		if msg == "" && err != nil {
			msg = err.Error()
		}
		if msg == "" {
			msg = "authentication required"
		}
		env.Errors = append(env.Errors, limitsV1Error{ProviderID: "devin", Message: msg})
	}
	out, err := json.Marshal(env)
	if err != nil {
		return body
	}
	return out
}

func limitsEnvelopeFromBody(body []byte) (limitsV1Envelope, bool) {
	if looksLikeUsageLines(body) {
		converted, err := usageLinesToLimits(body)
		if err != nil {
			return limitsV1Envelope{}, false
		}
		body = converted
	}
	if !looksLikeLimitsV1(body) {
		return limitsV1Envelope{}, false
	}
	var env limitsV1Envelope
	if err := json.Unmarshal(body, &env); err != nil {
		return limitsV1Envelope{}, false
	}
	if env.Providers == nil {
		env.Providers = map[string]limitsV1Provider{}
	}
	env.Schema = limitsSchemaV1
	return env, true
}

func stripCatalogProvider(env *limitsV1Envelope, id string) {
	canon := canonicalProviderID(id)
	for rawID := range env.Providers {
		if canonicalProviderID(rawID) == canon {
			delete(env.Providers, rawID)
		}
	}
	kept := make([]limitsV1Error, 0, len(env.Errors))
	for _, e := range env.Errors {
		if canonicalProviderID(e.ProviderID) == canon {
			continue
		}
		kept = append(kept, e)
	}
	env.Errors = kept
}

func (c *devinCollector) fetch(ctx context.Context) (*limitsV1Provider, string, error) {
	ctx, cancel := context.WithTimeout(ctx, devinFetchTimeout)
	defer cancel()

	payload, err := json.Marshal(map[string]string{"service_key": c.key})
	if err != nil {
		return nil, "failed to encode Devin request", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.url, bytes.NewReader(payload))
	if err != nil {
		return nil, "failed to build Devin request", err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, "Devin credit balance request failed", err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, collectorMaxResponseBytes+1))
	if err != nil {
		return nil, "failed to read Devin credit balance", err
	}
	if len(body) > collectorMaxResponseBytes {
		return nil, "Devin credit balance response too large", nil
	}
	if resp.StatusCode != http.StatusOK {
		return nil, classifyDevinStatus(resp.StatusCode, body), nil
	}

	var bal teamCreditBalance
	if err := json.Unmarshal(body, &bal); err != nil {
		return nil, "invalid Devin credit balance response", nil
	}
	provider, errMsg := creditBalanceToLimitsProvider(bal)
	if provider == nil {
		if errMsg == "" {
			errMsg = "Devin credit balance did not include remaining credits"
		}
		return nil, errMsg, nil
	}
	return provider, "", nil
}

func classifyDevinStatus(status int, body []byte) string {
	switch status {
	case http.StatusUnauthorized, http.StatusForbidden:
		return "authentication required"
	case http.StatusTooManyRequests:
		return "rate limited"
	default:
		detail := truncateDiagnostic(string(body), 120)
		if detail == "" {
			return fmt.Sprintf("unexpected status %d", status)
		}
		return fmt.Sprintf("unexpected status %d: %s", status, detail)
	}
}

type teamCreditBalance struct {
	PromptCreditsPerSeat  *float64
	NumSeats              *float64
	AddOnCreditsAvailable *float64
	AddOnCreditsUsed      *float64
	BillingCycleStart     string
	BillingCycleEnd       string
}

func (b *teamCreditBalance) UnmarshalJSON(data []byte) error {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	b.PromptCreditsPerSeat = floatPtrFromAnyJSON(raw, "promptCreditsPerSeat", "prompt_credits_per_seat")
	b.NumSeats = floatPtrFromAnyJSON(raw, "numSeats", "num_seats")
	b.AddOnCreditsAvailable = floatPtrFromAnyJSON(raw, "addOnCreditsAvailable", "add_on_credits_available")
	b.AddOnCreditsUsed = floatPtrFromAnyJSON(raw, "addOnCreditsUsed", "add_on_credits_used")
	b.BillingCycleStart = stringFromAnyJSON(raw, "billingCycleStart", "billing_cycle_start")
	b.BillingCycleEnd = stringFromAnyJSON(raw, "billingCycleEnd", "billing_cycle_end")
	return nil
}

func floatPtrFromAnyJSON(raw map[string]json.RawMessage, keys ...string) *float64 {
	for _, key := range keys {
		msg, ok := raw[key]
		if !ok || len(bytes.TrimSpace(msg)) == 0 || bytes.Equal(bytes.TrimSpace(msg), []byte("null")) {
			continue
		}
		var n float64
		if err := json.Unmarshal(msg, &n); err == nil {
			v := n
			return &v
		}
		var s string
		if err := json.Unmarshal(msg, &s); err == nil {
			if parsed, err := json.Number(strings.TrimSpace(s)).Float64(); err == nil {
				v := parsed
				return &v
			}
		}
	}
	return nil
}

func stringFromAnyJSON(raw map[string]json.RawMessage, keys ...string) string {
	for _, key := range keys {
		msg, ok := raw[key]
		if !ok {
			continue
		}
		var s string
		if err := json.Unmarshal(msg, &s); err == nil {
			return strings.TrimSpace(s)
		}
	}
	return ""
}

func creditBalanceToLimitsProvider(bal teamCreditBalance) (*limitsV1Provider, string) {
	used := 0.0
	available := 0.0
	hasUsed := bal.AddOnCreditsUsed != nil
	hasAvailable := bal.AddOnCreditsAvailable != nil
	if hasUsed {
		used = math.Max(0, *bal.AddOnCreditsUsed)
	}
	if hasAvailable {
		available = math.Max(0, *bal.AddOnCreditsAvailable)
	}
	if !hasUsed && !hasAvailable {
		return nil, "Devin credit balance did not include remaining credits"
	}
	limit := used + available
	if limit <= 0 {
		return nil, "Devin credit balance did not include remaining credits"
	}
	utilization := used / limit
	resetsAt := bal.BillingCycleEnd
	var windowSeconds *float64
	if start, ok := parseLimitsTime(bal.BillingCycleStart); ok {
		if end, ok := parseLimitsTime(bal.BillingCycleEnd); ok && end.After(start) {
			seconds := end.Sub(start).Seconds()
			windowSeconds = &seconds
			if resetsAt == "" {
				resetsAt = end.UTC().Format(time.RFC3339)
			}
		}
	}

	resources := map[string]limitsV1Resource{
		"credits": {
			Kind:          "consumption",
			Unit:          "percent",
			Used:          floatPtr(used),
			Limit:         floatPtr(limit),
			Remaining:     floatPtr(available),
			Utilization:   floatPtr(utilization),
			ResetsAt:      resetsAt,
			WindowSeconds: windowSeconds,
			Label:         "Credits",
		},
	}
	if hasAvailable {
		resources["credit-value"] = limitsV1Resource{
			Kind:      "balance",
			Unit:      "credits",
			Available: floatPtr(available),
		}
	}
	return &limitsV1Provider{
		DisplayName: "Devin",
		Resources:   resources,
	}, ""
}

func floatPtr(v float64) *float64 {
	return &v
}
