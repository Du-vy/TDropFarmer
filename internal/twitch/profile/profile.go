package profile

import "net/http"

const (
	// UserAgent is the standard modern web browser User-Agent used across all
	// Twitch HTTP interactions (OAuth device flow, GQL, Helix, and playback/telemetry).
	UserAgent = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/150.0.0.0 Safari/537.36"

	// WebPlayerUserAgent is kept as an alias for backwards compatibility.
	WebPlayerUserAgent = UserAgent
)

// Apply sets the standard User-Agent header on an outgoing HTTP request.
func Apply(req *http.Request) {
	req.Header.Set("User-Agent", UserAgent)
}

// ApplyWebPlayer is an alias for Apply.
func ApplyWebPlayer(req *http.Request) {
	Apply(req)
}

