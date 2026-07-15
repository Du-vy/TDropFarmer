package profile

import (
	"net/http"
	"strings"
	"testing"
)

func TestUserAgentProfilesAreExplicitAndDistinct(t *testing.T) {
	if strings.Contains(MobileAppUserAgent, "Go-http-client") ||
		!strings.Contains(MobileAppUserAgent, "Android 16") ||
		!strings.Contains(MobileAppUserAgent, "SM-S938B") ||
		!strings.Contains(MobileAppUserAgent, "tv.twitch.android.app/30.3.0/3003006") {
		t.Fatalf("unexpected mobile app User-Agent %q", MobileAppUserAgent)
	}
	if strings.Contains(WebPlayerUserAgent, "Go-http-client") || !strings.Contains(WebPlayerUserAgent, "Chrome/150") {
		t.Fatalf("unexpected web player User-Agent %q", WebPlayerUserAgent)
	}
	if MobileAppUserAgent == WebPlayerUserAgent {
		t.Fatal("mobile app and web player profiles must remain distinct")
	}

	req, err := http.NewRequest(http.MethodGet, "https://example.com", nil)
	if err != nil {
		t.Fatalf("create request: %v", err)
	}
	ApplyMobileApp(req)
	if got := req.Header.Get("User-Agent"); got != MobileAppUserAgent {
		t.Fatalf("mobile User-Agent = %q, want %q", got, MobileAppUserAgent)
	}
	ApplyWebPlayer(req)
	if got := req.Header.Get("User-Agent"); got != WebPlayerUserAgent {
		t.Fatalf("web User-Agent = %q, want %q", got, WebPlayerUserAgent)
	}
}
