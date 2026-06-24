package auth

import "time"

func TokenExpiresSoon(expiresAt time.Time, now time.Time) bool {
	return expiresAt.IsZero() || expiresAt.Sub(now) < time.Minute
}
