package ingest

import (
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"testing"
)

func TestPublicNetworkValidation(t *testing.T) {
	for _, address := range []string{"127.0.0.1", "169.254.169.254", "100.64.0.1", "192.168.0.3", "198.18.0.1", "::1", "::ffff:127.0.0.1", "fd00::1", "64:ff9b::7f00:1", "2002:7f00:1::1"} {
		if publicIP(netip.MustParseAddr(address)) {
			t.Errorf("private or special address permitted: %s", address)
		}
	}
	for _, address := range []string{"8.8.8.8", "2606:4700:4700::1111"} {
		if !publicIP(netip.MustParseAddr(address)) {
			t.Errorf("public address denied: %s", address)
		}
	}
	for _, raw := range []string{"https://localhost/feed", "https://127.0.0.1/", "https://[::1]/", "http://example.org:8080/", "https://user:pass@example.org/", "file:///tmp/news"} {
		if _, err := ParseURL(raw); err == nil {
			t.Errorf("unsafe URL permitted: %s", raw)
		}
	}
}

func TestUnsafeRedirect(t *testing.T) {
	client := NewPublicClient()
	prev := []*http.Request{{URL: &url.URL{Scheme: "https", Host: "example.com"}}}
	for _, next := range []*url.URL{
		{Scheme: "https", Host: net.JoinHostPort("::1", "443")},
		{Scheme: "http", Host: "example.com"},
		{Scheme: "https", Host: "localhost"},
	} {
		if err := client.CheckRedirect(&http.Request{URL: next}, prev); err == nil {
			t.Errorf("unsafe redirect permitted: %s", next)
		}
	}
}
