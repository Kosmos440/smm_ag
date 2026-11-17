package ingest

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"time"
)

const maxPageSize = 4 << 20

type Page struct {
	Body        []byte
	URL         *url.URL
	ContentType string
}

// NewPublicClient resolves hosts at dial time and connects only to a validated
// public address. The transport does not use an environment proxy.
func NewPublicClient() *http.Client {
	dialer := &net.Dialer{Timeout: 5 * time.Second, KeepAlive: 30 * time.Second}
	transport := &http.Transport{
		DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
			host, port, err := net.SplitHostPort(address)
			if err != nil {
				return nil, err
			}
			addresses, err := net.DefaultResolver.LookupNetIP(ctx, "ip", host)
			if err != nil {
				return nil, err
			}
			dialErr := errors.New("source host has no permitted public address")
			for _, address := range addresses {
				if !publicIP(address) {
					continue
				}
				conn, err := dialer.DialContext(ctx, network, net.JoinHostPort(address.String(), port))
				if err == nil {
					return conn, nil
				}
				dialErr = err
			}
			return nil, dialErr
		},
		ForceAttemptHTTP2:      true,
		TLSHandshakeTimeout:    5 * time.Second,
		ResponseHeaderTimeout:  10 * time.Second,
		MaxResponseHeaderBytes: 128 << 10,
		IdleConnTimeout:        60 * time.Second,
		MaxIdleConns:           16,
	}
	return &http.Client{
		Transport: transport,
		Timeout:   20 * time.Second,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 5 {
				return errors.New("source redirects too many times")
			}
			if via[len(via)-1].URL.Scheme == "https" && req.URL.Scheme != "https" {
				return errors.New("source redirected from HTTPS to HTTP")
			}
			return validateURL(req.URL)
		},
	}
}

var specialNetworks = []netip.Prefix{
	netip.MustParsePrefix("0.0.0.0/8"),
	netip.MustParsePrefix("100.64.0.0/10"),
	netip.MustParsePrefix("192.0.0.0/24"),
	netip.MustParsePrefix("192.0.2.0/24"),
	netip.MustParsePrefix("192.88.99.0/24"),
	netip.MustParsePrefix("198.18.0.0/15"),
	netip.MustParsePrefix("198.51.100.0/24"),
	netip.MustParsePrefix("203.0.113.0/24"),
	netip.MustParsePrefix("240.0.0.0/4"),
	netip.MustParsePrefix("64:ff9b::/96"),
	netip.MustParsePrefix("64:ff9b:1::/48"),
	netip.MustParsePrefix("100::/64"),
	netip.MustParsePrefix("2001::/23"),
	netip.MustParsePrefix("2001:db8::/32"),
	netip.MustParsePrefix("2002::/16"),
	netip.MustParsePrefix("3fff::/20"),
}

var publicIPv6Network = netip.MustParsePrefix("2000::/3")

func publicIP(ip netip.Addr) bool {
	ip = ip.Unmap()
	if ip.Zone() != "" || !ip.IsGlobalUnicast() || ip.IsPrivate() {
		return false
	}
	if ip.Is6() && !publicIPv6Network.Contains(ip) {
		return false
	}
	for _, network := range specialNetworks {
		if network.Contains(ip) {
			return false
		}
	}
	return true
}

// ParseURL validates the source URL syntax. Resolved addresses are checked by
// NewPublicClient when the network connection is opened.
func ParseURL(raw string) (*url.URL, error) {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return nil, errors.New("invalid source URL")
	}
	if err := validateURL(parsed); err != nil {
		return nil, err
	}
	parsed.Fragment = ""
	return parsed, nil
}

func validateURL(parsed *url.URL) error {
	if parsed.Scheme != "https" && parsed.Scheme != "http" {
		return errors.New("source URL must use HTTP or HTTPS")
	}
	host := strings.ToLower(strings.TrimSuffix(parsed.Hostname(), "."))
	if parsed.User != nil || host == "" || host == "localhost" || strings.HasSuffix(host, ".localhost") {
		return errors.New("source URL must use a public host without credentials")
	}
	if literal, err := netip.ParseAddr(host); err == nil && !publicIP(literal) {
		return errors.New("source URL must use a public IP address")
	}
	port := parsed.Port()
	if port != "" && (parsed.Scheme == "http" && port != "80" || parsed.Scheme == "https" && port != "443") {
		return errors.New("source URL must use the default HTTP or HTTPS port")
	}
	return nil
}

func Fetch(ctx context.Context, client *http.Client, raw string) (Page, error) {
	source, err := ParseURL(raw)
	if err != nil {
		return Page{}, err
	}
	return fetch(ctx, client, source)
}

func fetch(ctx context.Context, client *http.Client, source *url.URL) (Page, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, source.String(), nil)
	if err != nil {
		return Page{}, errors.New("cannot make source request")
	}
	req.Header.Set("User-Agent", "SMM-Agent/1.0")
	resp, err := client.Do(req)
	if err != nil {
		var urlErr *url.Error
		if errors.As(err, &urlErr) {
			err = urlErr.Err
		}
		return Page{}, fmt.Errorf("request source: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return Page{}, fmt.Errorf("source returned HTTP %d", resp.StatusCode)
	}
	if resp.ContentLength > maxPageSize {
		return Page{}, errors.New("source response exceeds size limit")
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxPageSize+1))
	if err != nil {
		return Page{}, fmt.Errorf("read source response: %w", err)
	}
	if len(body) > maxPageSize {
		return Page{}, errors.New("source response exceeds size limit")
	}
	finalURL := source
	if resp.Request != nil {
		finalURL = resp.Request.URL
	}
	return Page{Body: body, URL: finalURL, ContentType: resp.Header.Get("Content-Type")}, nil
}

func ResolveLink(base *url.URL, raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" || strings.HasPrefix(raw, "#") {
		return ""
	}
	parsed, err := base.Parse(raw)
	if err != nil || validateURL(parsed) != nil {
		return ""
	}
	parsed.Fragment = ""
	return parsed.String()
}
