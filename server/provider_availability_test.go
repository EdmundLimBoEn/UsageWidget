package server

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"slices"
	"testing"
	"time"
)

func availabilitySnapshot(t *testing.T, api *API, token string) Snapshot {
	t.Helper()
	response := doRequest(t, api, http.MethodGet, "/v1/snapshot", token, nil)
	if response.Code != http.StatusOK {
		t.Fatalf("snapshot status: %d", response.Code)
	}
	var snapshot Snapshot
	if err := json.Unmarshal(response.Body.Bytes(), &snapshot); err != nil {
		t.Fatal(err)
	}
	return snapshot
}

func catalogRow(t *testing.T, snapshot Snapshot, id string) ProviderAvailability {
	t.Helper()
	for _, row := range snapshot.ProviderCatalog {
		if row.ID == id {
			return row
		}
	}
	t.Fatalf("missing catalog provider %s", id)
	return ProviderAvailability{}
}

func TestMissingProviderCatalogDoesNotAddUsage(t *testing.T) {
	api, store := newTestAPI(t)
	snapshot, err := Normalize([]byte(`{"schema":"crossusage.limits.v1","providers":{}}`), 5, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	payload, _ := json.Marshal(snapshot)
	if err := store.SaveSnapshot(snapshot.FetchedAt, payload); err != nil {
		t.Fatal(err)
	}
	got := availabilitySnapshot(t, api, "secret-token")
	if len(got.Providers) != 0 {
		t.Fatal("catalog must not create usage bars")
	}
	ids := []string{}
	for _, row := range got.ProviderCatalog {
		ids = append(ids, row.ID)
		if row.Available || row.Status != "missing" || row.Name == "" {
			t.Fatalf("unexpected missing row: %+v", row)
		}
	}
	if !slices.Equal(ids, defaultProviderOrder) {
		t.Fatalf("catalog IDs: %v", ids)
	}
}

func TestHiddenProviderRemainsDiscoverableAndAvailable(t *testing.T) {
	api, store := newTestAPI(t)
	if err := store.SetSetting("hidden_providers", `["claude"]`); err != nil {
		t.Fatal(err)
	}
	snapshot := Snapshot{FetchedAt: time.Now().UTC(), Providers: []Provider{
		{ID: "claude", Windows: []Window{{ID: "claude.session", UsedPercent: 12}}},
		{ID: "claude_code", Windows: []Window{{ID: "claude_code.session", UsedPercent: 12}}},
	}}
	payload, _ := json.Marshal(snapshot)
	if err := store.SaveSnapshot(snapshot.FetchedAt, payload); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		got := availabilitySnapshot(t, api, "secret-token")
		if len(got.Providers) != 0 {
			t.Fatal("hidden usage leaked")
		}
		if len(got.ProviderCatalog) != len(providerCatalog) {
			t.Fatal("aliases duplicated catalog")
		}
		if row := catalogRow(t, got, "claude_code"); !row.Available || row.Status != "available" {
			t.Fatalf("hidden row: %+v", row)
		}
	}
	settings, err := loadSettings(store)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(settings.HiddenProviders, []string{"claude_code"}) {
		t.Fatalf("hidden preference changed: %v", settings.HiddenProviders)
	}
}

func TestAvailabilityTransitionsWithBackendAndRetainsHiddenChoice(t *testing.T) {
	poller, store, _, client := newPollerHarness(t)
	if err := store.SetSetting("hidden_providers", `["codex"]`); err != nil {
		t.Fatal(err)
	}
	body := `{"schema":"crossusage.limits.v1","providers":{},"errors":[{"providerId":"codex","message":"authentication required"}]}`
	client.httpClient = &http.Client{Transport: roundTripFunc(func(_ *http.Request) (*http.Response, error) {
		return testHTTPResponse(http.StatusOK, body), nil
	})}
	check := func(available bool, status string) Snapshot {
		t.Helper()
		if result := poller.PollNow(context.Background()); !result.Success {
			t.Fatalf("poll: %+v", result)
		}
		got := availabilitySnapshot(t, poller.api, "x")
		if row := catalogRow(t, got, "codex"); row.Available != available || row.Status != status {
			t.Fatalf("row: %+v", row)
		}
		if len(got.Providers) != 0 {
			t.Fatal("hidden provider was automatically enabled")
		}
		return got
	}
	check(false, "error")
	body = crossUsageBody
	check(true, "available")
	body = `{"schema":"crossusage.limits.v1","providers":{},"errors":[{"providerId":"codex","message":"authentication required"}]}`
	check(false, "stale")
	cached := latestSnap(t, store)
	if len(cached.Providers[0].Windows) == 0 || !cached.Providers[0].Stale {
		t.Fatal("last-known usage lost")
	}
	body = crossUsageBody
	check(true, "available")
	settings, err := loadSettings(store)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(settings.HiddenProviders, []string{"codex"}) {
		t.Fatal("login changed hidden preference")
	}
}

func TestCachedErrorAndAgedUsageCannotUnlockProvider(t *testing.T) {
	for _, tc := range []struct {
		name          string
		stale         bool
		providerStale bool
		providerError string
		age           time.Duration
		failure       bool
	}{
		{name: "snapshot stale", stale: true},
		{name: "provider stale", providerStale: true},
		{name: "provider error", providerError: "login expired"},
		{name: "expired", age: 11 * time.Minute},
		{name: "collector failure", failure: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			api, store := newTestAPI(t)
			snap := Snapshot{FetchedAt: time.Now().UTC().Add(-tc.age), Stale: tc.stale, Providers: []Provider{{ID: "codex", Stale: tc.providerStale, Error: tc.providerError, Windows: []Window{{ID: "codex.session"}}}}}
			payload, _ := json.Marshal(snap)
			if err := store.SaveSnapshot(snap.FetchedAt, payload); err != nil {
				t.Fatal(err)
			}
			api.RecordPollOutcome(PollResult{PolledAt: time.Now(), Success: !tc.failure})
			if catalogRow(t, availabilitySnapshot(t, api, "secret-token"), "codex").Available {
				t.Fatal("cached/error usage unlocked provider")
			}
		})
	}
}

func TestNormalizeRejectsCachedAvailabilityEvidence(t *testing.T) {
	now := time.Now().UTC()
	cases := []string{
		`{"schema":"crossusage.limits.v1","providers":{"codex":{"resources":{"session":{"unit":"percent","utilization":0.2}}}},"errors":[{"providerId":"codex_cli","message":"login expired"}]}`,
		`{"schema":"crossusage.limits.v1","providers":{"codex":{"fetchedAt":"2020-01-01T00:00:00Z","resources":{"session":{"unit":"percent","utilization":0.2}}}}}`,
		`{"schema":"crossusage.limits.v1","providers":{"codex":{"fetchedAt":"invalid","resources":{"session":{"unit":"percent","utilization":0.2}}}}}`,
		`[{"providerId":"codex","fetchedAt":"2020-01-01T00:00:00Z","lines":[{"type":"progress","label":"Session","used":20,"limit":100}]}]`,
	}
	for _, body := range cases {
		snapshot, err := Normalize([]byte(body), 5, now)
		if err != nil {
			t.Fatal(err)
		}
		if len(snapshot.Providers) != 1 || len(snapshot.Providers[0].Windows) != 1 {
			t.Fatal("useful cached windows lost")
		}
		if providerAvailability(snapshot.Providers, true)[1].Available {
			t.Fatal("stale/error upstream data unlocked provider")
		}
	}
}

func TestMalformedPollLocksAvailabilityAndPreservesUsage(t *testing.T) {
	poller, store, _, client := newPollerHarness(t)
	if result := poller.PollNow(context.Background()); !result.Success {
		t.Fatal(result.Error)
	}
	client.httpClient = &http.Client{Transport: roundTripFunc(func(_ *http.Request) (*http.Response, error) { return testHTTPResponse(http.StatusOK, `{}`), nil })}
	if result := poller.PollNow(context.Background()); result.Success {
		t.Fatal("invalid poll accepted")
	}
	if got := latestSnap(t, store); !got.Stale || len(got.Providers[0].Windows) != 1 {
		t.Fatal("failed poll discarded last-known usage")
	}
	if catalogRow(t, availabilitySnapshot(t, poller.api, "x"), "codex").Available {
		t.Fatal("failed poll unlocked provider")
	}
}

func TestSupportedCatalogMatchesPinnedCrossUsagePlugins(t *testing.T) {
	fixture, err := os.ReadFile("testdata/crossusage-v1.4.4-plugin-ids.json")
	if err != nil {
		t.Fatal(err)
	}
	var upstream struct {
		Version   string
		PluginIDs []string
	}
	if err := json.Unmarshal(fixture, &upstream); err != nil {
		t.Fatal(err)
	}
	manifest, err := os.ReadFile("../release-manifest.json")
	if err != nil {
		t.Fatal(err)
	}
	var release struct{ CrossUsage struct{ Version string } }
	if err := json.Unmarshal(manifest, &release); err != nil {
		t.Fatal(err)
	}
	if upstream.Version != release.CrossUsage.Version {
		t.Fatal("refresh upstream catalog fixture for the pinned CrossUsage version")
	}
	for _, id := range catalogPluginIDs() {
		if !slices.Contains(upstream.PluginIDs, id) {
			t.Fatalf("unsupported upstream plugin %s", id)
		}
		if !inProviderCatalog(id) {
			t.Fatalf("missing canonical settings row for %s", id)
		}
	}
	if len(catalogPluginIDs()) != len(providerCatalog) {
		t.Fatal("supported catalog/plugin count drift")
	}
}

func TestFreshAliasWinsOverStaleCachedAlias(t *testing.T) {
	body := []byte(`{"schema":"crossusage.limits.v1","providers":{"claude":{"stale":true,"resources":{"session":{"unit":"percent","utilization":0.1},"weekly":{"unit":"percent","utilization":0.2}}},"claude_code":{"resources":{"session":{"unit":"percent","utilization":0.3}}}}}`)
	for range 25 {
		snapshot, err := Normalize(body, 5, time.Now().UTC())
		if err != nil {
			t.Fatal(err)
		}
		if len(snapshot.Providers) != 1 || snapshot.Providers[0].Stale || snapshot.Providers[0].Windows[0].UsedPercent != 30 {
			t.Fatalf("fresh canonical alias lost: %+v", snapshot.Providers)
		}
		if !providerAvailability(snapshot.Providers, true)[2].Available {
			t.Fatal("fresh alias did not unlock")
		}
	}
}
