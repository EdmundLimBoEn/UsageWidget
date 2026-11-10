package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestWindowsSmokeScriptCollectsOverCrossUsageHTTP(t *testing.T) {
	for _, path := range windowsDaemonSmokeWorkflows(t) {
		t.Run(filepath.Base(path), func(t *testing.T) {
			script, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			text := string(script)
			if _, ok := powershellEnvAssignment(text, "CODEXBAR_URL"); ok {
				t.Fatal("assigns CODEXBAR_URL, which LoadConfig ignores")
			}
			if _, ok := powershellEnvAssignment(text, "CROSSUSAGE_URL"); !ok {
				t.Fatal("does not assign CROSSUSAGE_URL")
			}
			body := windowsSmokeMockJSON(t, text)
			wantID := windowsSmokeSnapshotProviderID(text)
			fetchedAt := time.Date(2026, 11, 10, 2, 0, 0, 0, time.UTC)
			snapshot, err := Normalize(body, 5, fetchedAt)
			if err != nil {
				t.Fatalf("smoke mock JSON is not a CrossUsage payload: %v", err)
			}
			if len(snapshot.Providers) == 0 || snapshot.Providers[0].ID != wantID {
				t.Fatalf("smoke snapshot provider = %+v, want id %q", snapshot.Providers, wantID)
			}

			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write(body)
			}))
			t.Cleanup(upstream.Close)

			t.Setenv("USAGEWIDGET_TOKEN", validTestToken)
			t.Setenv("CROSSUSAGE_URL", upstream.URL)
			t.Setenv("CROSSUSAGE_CMD", "")
			t.Setenv("CROSSUSAGE_BIN", "")
			t.Setenv("COLLECTOR_SOCKET", "")
			t.Setenv("DB_PATH", filepath.Join(t.TempDir(), "smoke.db"))
			cfg, err := LoadConfig()
			if err != nil {
				t.Fatal(err)
			}
			if NewUsageSourceFromConfig(cfg).SourceName() != "crossusage-http" {
				t.Fatalf("source = %s, want crossusage-http", NewUsageSourceFromConfig(cfg).SourceName())
			}
			store, err := OpenStore(cfg.DBPath)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = store.Close() })
			src := NewUsageSourceFromConfig(cfg)
			api := NewAPI(cfg, store, src)
			api.SetPoller(NewPoller(store, src, noopNotifier{}, api))
			rec := doRequest(t, api, http.MethodPost, "/v1/poll", cfg.Token, nil)
			if rec.Code != http.StatusOK {
				t.Fatalf("POST /v1/poll status = %d, body %s", rec.Code, rec.Body.String())
			}
			var got pollResponse
			if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
				t.Fatal(err)
			}
			if !got.Success {
				t.Fatalf("POST /v1/poll success=false: %+v", got)
			}
			snapRec := doRequest(t, api, http.MethodGet, "/v1/snapshot", cfg.Token, nil)
			if snapRec.Code != http.StatusOK {
				t.Fatalf("GET /v1/snapshot status = %d, body %s", snapRec.Code, snapRec.Body.String())
			}
			var live Snapshot
			if err := json.Unmarshal(snapRec.Body.Bytes(), &live); err != nil {
				t.Fatal(err)
			}
			if len(live.Providers) == 0 || live.Providers[0].ID != wantID {
				t.Fatalf("live snapshot provider = %+v, want id %q", live.Providers, wantID)
			}
		})
	}
}

func TestPullRequestCIRunsWindowsSmoke(t *testing.T) {
	for _, rel := range []string{
		filepath.Join(".github", "workflows", "ci.yml"),
		filepath.Join(".github", "workflows", "release.yml"),
	} {
		body, err := os.ReadFile(filepath.Join(repoRoot(t), rel))
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(body), "windows-smoke") {
			t.Errorf("%s does not run windows-smoke", rel)
		}
	}
}

func windowsDaemonSmokeWorkflows(t *testing.T) []string {
	t.Helper()
	dir := filepath.Join(repoRoot(t), ".github", "workflows")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var paths []string
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if !strings.HasSuffix(name, ".yml") && !strings.HasSuffix(name, ".yaml") {
			continue
		}
		path := filepath.Join(dir, name)
		body, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(body), "usagewidgetd.exe") && strings.Contains(string(body), "/v1/poll") {
			paths = append(paths, path)
		}
	}
	if len(paths) == 0 {
		t.Fatal("no Windows daemon smoke workflow found")
	}
	return paths
}

func repoRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(file), ".."))
}

func powershellEnvAssignment(script, key string) (string, bool) {
	re := regexp.MustCompile(`(?m)\$env:` + regexp.QuoteMeta(key) + `\s*=\s*"([^"]*)"`)
	match := re.FindStringSubmatch(script)
	if match == nil {
		return "", false
	}
	return match[1], true
}

func windowsSmokeMockJSON(t *testing.T, script string) []byte {
	t.Helper()
	if match := regexp.MustCompile(`GetBytes\('([^']*)'\)`).FindStringSubmatch(script); match != nil {
		return []byte(match[1])
	}
	t.Fatal("windows smoke mock JSON not found")
	return nil
}

func windowsSmokeSnapshotProviderID(script string) string {
	match := regexp.MustCompile(`\$snapshot\.providers\[0\]\.id -ne "([^"]+)"`).FindStringSubmatch(script)
	if match == nil {
		return "codex"
	}
	return match[1]
}
