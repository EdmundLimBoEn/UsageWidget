package server

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// The scripts launch this test executable as their daemon, so collection uses
// the actual persisted configuration and production source selection.
func TestDesktopSetupDaemon(t *testing.T) {
	if os.Getenv("USAGEWIDGET_SETUP_TEST") != "1" {
		return
	}
	cfg, err := LoadConfig()
	if err != nil {
		t.Fatal(err)
	}
	wantURL := os.Getenv("SETUP_TEST_URL")
	if cfg.CrossUsageURL != wantURL {
		t.Fatal("persisted configuration did not select the requested source")
	}
	if wantURL != "" {
		if cfg.CrossUsageBin != "" || os.Getenv("CROSSUSAGE_RESOURCES") != "" {
			t.Fatal("HTTP configuration contains discovered CLI settings")
		}
		client := NewUsageSourceFromConfig(cfg).(*CrossUsageClient)
		requests := 0
		// Keep the production HTTP fetch path, with an in-memory endpoint so
		// this regression also runs in sandboxes that prohibit local sockets.
		client.httpClient.Transport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
			if req.URL.String() != wantURL {
				t.Fatal("collection requested the wrong URL")
			}
			requests++
			return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`{"providers":[]}`))}, nil
		})
		body, err := client.Fetch(context.Background())
		if err != nil || string(body) != `{"providers":[]}` || requests != 1 {
			t.Fatal("collection did not return the HTTP fixture")
		}
	} else if cfg.CrossUsageBin != os.Getenv("SETUP_TEST_CLI") {
		t.Fatal("CLI discovery no longer works without a URL")
	}
	if err := os.WriteFile(os.Getenv("SETUP_TEST_DONE"), []byte("ok"), 0600); err != nil {
		t.Fatal(err)
	}
}

func TestDesktopFreshSetupSource(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("test fixtures require a POSIX shell")
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	for _, script := range []string{"install-macos.sh", "start-macos.sh", "install-windows.ps1", "start-windows.ps1"} {
		for _, explicitURL := range []bool{true, false} {
			t.Run(fmt.Sprintf("%s/url=%t", script, explicitURL), func(t *testing.T) {
				windows := strings.HasSuffix(script, ".ps1")
				shell := "bash"
				if windows {
					shell = "pwsh"
				}
				shellPath, err := exec.LookPath(shell)
				if err != nil {
					t.Skipf("%s is not installed", shell)
				}
				dir := t.TempDir()
				write := func(name, content string) string {
					t.Helper()
					path := filepath.Join(dir, name)
					if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
						t.Fatal(err)
					}
					if err := os.WriteFile(path, []byte(content), 0700); err != nil {
						t.Fatal(err)
					}
					return path
				}
				for _, source := range []string{script, "start-macos.sh", "start-windows.ps1"} {
					body, err := os.ReadFile(filepath.Join("deploy", source))
					if err != nil {
						t.Fatal(err)
					}
					name := source
					if source == "start-macos.sh" {
						name = "start-server.sh"
					} else if source == "start-windows.ps1" {
						name = "start-server.ps1"
					}
					write(name, string(body))
				}
				cli := write("bin/crossusage-cli", "#!/bin/sh\ntouch \"$SETUP_TEST_CLI_CALLED\"\nexit 1\n")
				write("bin/crossusage-cli.exe", "#!/bin/sh\ntouch \"$SETUP_TEST_CLI_CALLED\"\nexit 1\n")
				write("bin/resources/bundled_plugins/fixture", "")
				for _, name := range []string{"usagewidgetd", "usagewidgetd.exe"} {
					write("bin/"+name, "#!/bin/sh\nexec \"$SETUP_TEST_EXECUTABLE\" -test.run='^TestDesktopSetupDaemon$'\n")
				}
				for _, name := range []string{"curl", "usagewidget-qr", "usagewidget-qr.exe"} {
					write("bin/"+name, "#!/bin/sh\nexit 0\n")
				}
				for _, name := range []string{"tailscale", "tailscale.exe"} {
					write("bin/"+name, "#!/bin/sh\nprintf '%s\\n' '{\"Self\":{\"DNSName\":\"setup.test.\"}}'\n")
				}
				write("bin/launchctl", "#!/bin/sh\nif [ \"$1\" = kickstart ]; then exec bash \"$SETUP_TEST_DIR/start-server.sh\"; fi\n")
				url := ""
				if explicitURL {
					url = "http://127.0.0.1:6736/v1/limits?provider=cursor&provider=claude"
				}
				entry := filepath.Join(dir, script)
				if strings.HasPrefix(script, "start-") {
					entry = filepath.Join(dir, "start-server"+filepath.Ext(script))
				}
				args := []string{entry}
				if windows {
					// Replace only OS integration; execute both real setup and launcher scripts.
					wrapper := `function New-ScheduledTaskAction {}
function New-ScheduledTaskTrigger {}
function Register-ScheduledTask {}
function Start-ScheduledTask {
    & (Get-Process -Id $PID).Path -NoProfile -File (Join-Path $env:SETUP_TEST_DIR 'start-server.ps1')
    if ($LASTEXITCODE -ne 0) { throw 'daemon failed' }
}
function Invoke-RestMethod {}
& $env:SETUP_TEST_ENTRY
`
					args = []string{"-NoProfile", "-File", write("test.ps1", wrapper)}
					if !explicitURL && script == "install-windows.ps1" {
						cli += ".exe"
					}
				}
				cmd := exec.Command(shellPath, args...)
				// Use a clean environment, including a temporary home: never read the
				// developer's configuration or run host service/network commands.
				cmd.Env = []string{
					"PATH=" + filepath.Join(dir, "bin") + string(os.PathListSeparator) + os.Getenv("PATH"),
					"HOME=" + dir, "LOCALAPPDATA=" + dir, "USERNAME=setup-test",
					"USAGEWIDGET_DATA_DIR=" + filepath.Join(dir, "UsageWidget"),
					"CROSSUSAGE_URL=" + url, "SETUP_TEST_URL=" + url,
					"USAGEWIDGET_SETUP_TEST=1", "SETUP_TEST_EXECUTABLE=" + executable,
					"SETUP_TEST_DIR=" + dir, "SETUP_TEST_ENTRY=" + entry,
					"SETUP_TEST_CLI=" + cli, "SETUP_TEST_CLI_CALLED=" + filepath.Join(dir, "cli-called"),
					"SETUP_TEST_DONE=" + filepath.Join(dir, "done"),
				}
				if output, err := cmd.CombinedOutput(); err != nil {
					t.Fatalf("setup failed: %v\n%s", err, output)
				}
				if _, err := os.Stat(filepath.Join(dir, "done")); err != nil {
					t.Fatal("daemon did not verify persisted configuration")
				}
				if _, err := os.Stat(filepath.Join(dir, "cli-called")); !os.IsNotExist(err) {
					t.Fatal("setup invoked the discovered CLI")
				}
			})
		}
	}
}
