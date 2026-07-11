package profile

import "net/http"

const (
	// MobileAppUserAgent matches the Android mobile Client-ID used for OAuth,
	// GQL, and Helix.
	MobileAppUserAgent = "Dalvik/2.1.0 (Linux; U; Android 16; SM-S938B Build/BP2A.250605.031) tv.twitch.android.app/30.2.2/3002026"
	// WebPlayerUserAgent is reserved for the web/site playback flow and the
	// unauthenticated Twitch settings bootstrap.
	WebPlayerUserAgent = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/150.0.0.0 Safari/537.36"
)

func ApplyMobileApp(req *http.Request) {
	req.Header.Set("User-Agent", MobileAppUserAgent)
}

func ApplyWebPlayer(req *http.Request) {
	req.Header.Set("User-Agent", WebPlayerUserAgent)
}
