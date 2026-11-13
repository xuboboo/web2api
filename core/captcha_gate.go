package core

import (
	"sync"
	"time"
)

// CaptchaGate remembers that an engine's browser endpoint recently answered
// with a captcha/challenge, so the engine can prefer its plain-HTTP path for a
// while instead of paying for another doomed browser attempt.
//
// It is intentionally coarse: scraping a single exit IP oscillates between
// "browser works" and "browser challenged", and the plain-HTTP path is often
// still accepted during a browser challenge.
type CaptchaGate struct {
	mu    sync.Mutex
	until time.Time
}

// DefaultCaptchaGateTTL is how long an engine stays browser-gated after a
// challenge.
const DefaultCaptchaGateTTL = 10 * time.Minute

// Blocked reports whether the browser path is currently considered gated.
func (g *CaptchaGate) Blocked() bool {
	if g == nil {
		return false
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	return time.Now().Before(g.until)
}

// Trip marks the browser path gated for ttl (DefaultCaptchaGateTTL when <= 0).
func (g *CaptchaGate) Trip(ttl time.Duration) {
	if g == nil {
		return
	}
	if ttl <= 0 {
		ttl = DefaultCaptchaGateTTL
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	until := time.Now().Add(ttl)
	if until.After(g.until) {
		g.until = until
	}
}

// Clear drops the gate (a browser search succeeded).
func (g *CaptchaGate) Clear() {
	if g == nil {
		return
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	g.until = time.Time{}
}
