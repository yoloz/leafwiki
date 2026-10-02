package urlimport

import (
	"net"
	"net/url"
	"testing"
)

// TestIsPrivateIP covers the SSRF guard's address classification. A false
// negative here means the importer would happily connect to an internal
// service, so every private/loopback/link-local range must be rejected.
func TestIsPrivateIP(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		ip      string
		private bool
	}{
		{"loopback v4", "127.0.0.1", true},
		{"loopback v6", "::1", true},
		{"private 10/8", "10.0.0.1", true},
		{"private 172.16/12", "172.16.5.4", true},
		{"private 192.168/16", "192.168.1.1", true},
		{"link local v4", "169.254.10.10", true},
		{"link local v6", "fe80::1", true},
		{"unspecified v4", "0.0.0.0", true},
		{"unspecified v6", "::", true},
		{"unique local v6", "fc00::1", true},
		{"public v4", "8.8.8.8", false},
		{"public v4 alt", "1.1.1.1", false},
		{"public v6", "2606:4700:4700::1111", false},
		{"nil ip", "", true},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var ip net.IP
			if tc.ip != "" {
				ip = net.ParseIP(tc.ip)
				if ip == nil {
					t.Fatalf("test bug: %q is not a valid IP", tc.ip)
				}
			}
			if got := isPrivateIP(ip); got != tc.private {
				t.Errorf("isPrivateIP(%q) = %v, want %v", tc.ip, got, tc.private)
			}
		})
	}
}

// TestAssertSafeScheme verifies that only http/https are accepted, blocking
// file://, gopher://, ftp:// and empty schemes.
func TestAssertSafeScheme(t *testing.T) {
	t.Parallel()

	ok := []string{"http://example.com", "https://example.com/a?b=c"}
	for _, raw := range ok {
		raw := raw
		t.Run("allowed "+raw, func(t *testing.T) {
			t.Parallel()
			u, err := url.Parse(raw)
			if err != nil {
				t.Fatalf("parse %q: %v", raw, err)
			}
			if err := assertSafeScheme(u); err != nil {
				t.Errorf("assertSafeScheme(%q) = %v, want nil", raw, err)
			}
		})
	}

	blocked := []string{"ftp://example.com", "file:///etc/passwd", "gopher://example.com", "//example.com"}
	for _, raw := range blocked {
		raw := raw
		t.Run("blocked "+raw, func(t *testing.T) {
			t.Parallel()
			u, err := url.Parse(raw)
			if err != nil {
				t.Fatalf("parse %q: %v", raw, err)
			}
			if err := assertSafeScheme(u); err == nil {
				t.Errorf("assertSafeScheme(%q) = nil, want error", raw)
			}
		})
	}
}
