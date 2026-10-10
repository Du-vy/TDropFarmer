package profile

import (
	"net/http"
	"strings"
	"testing"
)

func TestUserAgentProfileIsExplicitWebBrowser(t *testing.T) {
	if strings.Contains(UserAgent, "Go-http-client") || !strings.Contains(UserAgent, "Chrome/150") {
		t.Fatalf("unexpected User-Agent %q", UserAgent)
	}
	if UserAgent != WebPlayerUserAgent {
		t.Fatal("UserAgent and WebPlayerUserAgent must match")
	}

	req, err := http.NewRequest(http.MethodGet, "https://example.com", nil)
	if err != nil {
		t.Fatalf("create request: %v", err)
	}
	Apply(req)
	if got := req.Header.Get("User-Agent"); got != UserAgent {
		t.Fatalf("User-Agent = %q, want %q", got, UserAgent)
	}

	req2, err := http.NewRequest(http.MethodGet, "https://example.com", nil)
	if err != nil {
		t.Fatalf("create request: %v", err)
	}
	ApplyWebPlayer(req2)
	if got := req2.Header.Get("User-Agent"); got != UserAgent {
		t.Fatalf("web User-Agent = %q, want %q", got, UserAgent)
	}
}
