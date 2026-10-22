package main

import (
	"net"
	"strings"
	"testing"
)

func TestCollectorHTTPAddress(t *testing.T) {
	for _, address := range []string{"127.0.0.1:0", "127.42.1.2:8080", "[::1]:8080", "localhost:8080"} {
		t.Run(address, func(t *testing.T) {
			resolved, err := collectorHTTPAddress(address, "")
			if err != nil {
				t.Fatal(err)
			}
			host, _, err := net.SplitHostPort(resolved)
			if err != nil || !net.ParseIP(host).IsLoopback() {
				t.Fatalf("not pinned to loopback: %q", resolved)
			}
		})
	}
	for _, address := range []string{":8080", "0.0.0.0:8080", "[::]:8080", "192.168.1.2:8080", "203.0.113.1:8080", "[2001:db8::1]:8080"} {
		t.Run(address, func(t *testing.T) {
			for _, optIn := range []string{"", "0", "true", " 1"} {
				if _, err := collectorHTTPAddress(address, optIn); err == nil || !strings.Contains(err.Error(), "COLLECTOR_ALLOW_REMOTE=1") {
					t.Fatal("remote bind must require explicit opt-in and explain how")
				}
			}
			if _, err := collectorHTTPAddress(address, "1"); err != nil {
				t.Fatal(err)
			}
		})
	}
	for _, optIn := range []string{"", "1"} {
		if _, err := collectorHTTPAddress("127.0.0.1:invalid-port", optIn); err == nil {
			t.Fatal("invalid address accepted")
		}
	}
}
