// Package netutil builds the optional proxied network layer. When a proxy is
// configured, every Twitch-bound connection (HTTP and raw IRC) must leave
// through it so the account presents a single egress IP; the Discord webhook
// notifier intentionally stays direct.
package netutil

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"time"

	xproxy "golang.org/x/net/proxy"
)

// DialContextFunc dials a raw TCP connection, matching net.Dialer.DialContext.
type DialContextFunc func(ctx context.Context, network, addr string) (net.Conn, error)

// ParseProxyURL validates a proxy URL for use by both the HTTP transport and
// the raw IRC dialer. Only SOCKS5 is accepted: an HTTP proxy could not tunnel
// the IRC connection, which would split the account across two egress IPs.
// socks5h is normalized to socks5 — Go's SOCKS5 client already resolves
// hostnames through the proxy, so the two schemes behave identically.
func ParseProxyURL(raw string) (*url.URL, error) {
	parsed, err := url.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("parse proxy url: %w", err)
	}
	switch parsed.Scheme {
	case "socks5", "socks5h":
		parsed.Scheme = "socks5"
	default:
		return nil, fmt.Errorf("proxy url scheme %q is not supported (use socks5:// or socks5h://)", parsed.Scheme)
	}
	if parsed.Host == "" {
		return nil, fmt.Errorf("proxy url %q must include a host", raw)
	}
	return parsed, nil
}

// NewHTTPClient returns an HTTP client that routes requests through proxyURL,
// or a plain direct client when proxyURL is empty.
func NewHTTPClient(proxyURL string, timeout time.Duration) (*http.Client, error) {
	if proxyURL == "" {
		return &http.Client{Timeout: timeout}, nil
	}
	parsed, err := ParseProxyURL(proxyURL)
	if err != nil {
		return nil, err
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = http.ProxyURL(parsed)
	return &http.Client{Timeout: timeout, Transport: transport}, nil
}

// NewDialContext returns a dial function that tunnels raw TCP connections
// (the IRC chat socket) through the SOCKS5 proxy. It returns nil when
// proxyURL is empty, meaning callers should dial directly.
func NewDialContext(proxyURL string) (DialContextFunc, error) {
	if proxyURL == "" {
		return nil, nil
	}
	parsed, err := ParseProxyURL(proxyURL)
	if err != nil {
		return nil, err
	}
	dialer, err := xproxy.FromURL(parsed, &net.Dialer{Timeout: 10 * time.Second})
	if err != nil {
		return nil, fmt.Errorf("build proxy dialer: %w", err)
	}
	if ctxDialer, ok := dialer.(xproxy.ContextDialer); ok {
		return ctxDialer.DialContext, nil
	}
	return func(_ context.Context, network, addr string) (net.Conn, error) {
		return dialer.Dial(network, addr)
	}, nil
}
