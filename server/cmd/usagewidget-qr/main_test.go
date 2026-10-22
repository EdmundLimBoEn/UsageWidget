package main

import (
	"bytes"
	"flag"
	"os"
	"os/exec"
	"strings"
	"testing"
)

// Run the real entry point in a subprocess to capture stdout/stderr and exits.
func TestQRProcess(t *testing.T) {
	if os.Getenv("USAGEWIDGET_QR_TEST_PROCESS") != "1" {
		return
	}
	for i, arg := range os.Args {
		if arg == "--" {
			os.Args = append(os.Args[:1], os.Args[i+1:]...)
			break
		}
	}
	flag.CommandLine = flag.NewFlagSet(os.Args[0], flag.ExitOnError)
	main()
	os.Exit(0)
}

func TestTokenSources(t *testing.T) {
	const token = "test-only-token-0123456789abcdef0123456789"
	t.Setenv("USAGEWIDGET_QR_TEST_PROCESS", "1")
	run := func(envToken string, args ...string) (string, string, error) {
		t.Helper()
		t.Setenv("USAGEWIDGET_TOKEN", envToken)
		cmd := exec.Command(os.Args[0], append([]string{"-test.run=^TestQRProcess$", "--", "-url", "https://example.test"}, args...)...)
		var stdout, stderr bytes.Buffer
		cmd.Stdout, cmd.Stderr = &stdout, &stderr
		err := cmd.Run()
		return stdout.String(), stderr.String(), err
	}
	wantQR, stderr, err := run(token)
	if err != nil || wantQR == "" || strings.Contains(stderr, "deprecated") {
		t.Fatal("environment token must generate a QR without a deprecation warning")
	}
	for _, envToken := range []string{"", "different-test-token-0123456789abcdef"} {
		stdout, stderr, err := run(envToken, "-token", token)
		if err != nil || stdout != wantQR {
			t.Fatal("argument token must work and take precedence over the environment")
		}
		if !strings.Contains(stderr, "-token is deprecated") || !strings.Contains(stderr, "USAGEWIDGET_TOKEN") {
			t.Fatal("argument token must emit a deprecation warning to stderr")
		}
		if strings.Contains(stderr, token) {
			t.Fatal("warning must not disclose the token")
		}
	}
	_, stderr, err = run(token, "-token=")
	if err == nil || !strings.Contains(stderr, "-token is deprecated") {
		t.Fatal("an explicitly empty token argument must warn and override the environment")
	}
}
