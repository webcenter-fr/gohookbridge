package service

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/disaster37/go-secguard/bantracker"
	"github.com/disaster37/go-secguard/ratelimit"
	"github.com/disaster37/go-secguard/testutil"
	"gotest.tools/v3/assert"
)

// newFakeBanTracker returns a BanTracker backed by a manually-advanced clock so
// window/ban expiry is deterministic (no time.Sleep).
func newFakeBanTracker(clock *testutil.FakeClock) *BanTracker {
	return bantracker.New(0, clock.Now)
}

func newFakeRateLimiter(clock *testutil.FakeClock) *RateLimiter {
	return &RateLimiter{inner: ratelimit.NewRateLimiter(0, clock.Now)}
}

func TestRateLimiterAllowsUnderLimit(t *testing.T) {
	rl := NewRateLimiter()

	for i := 0; i < 5; i++ {
		assert.Equal(t, true, rl.Allow("192.0.2.1", 10, 60))
	}
}

func TestRateLimiterRejectsOverLimit(t *testing.T) {
	rl := NewRateLimiter()

	for i := 0; i < 3; i++ {
		assert.Equal(t, true, rl.Allow("192.0.2.1", 3, 60))
	}
	assert.Equal(t, false, rl.Allow("192.0.2.1", 3, 60))
}

func TestRateLimiterWindowExpiry(t *testing.T) {
	clock := testutil.NewFakeClock(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	rl := newFakeRateLimiter(clock)

	assert.Equal(t, true, rl.Allow("192.0.2.1", 1, 1))
	assert.Equal(t, false, rl.Allow("192.0.2.1", 1, 1))

	clock.Advance(1100 * time.Millisecond)

	assert.Equal(t, true, rl.Allow("192.0.2.1", 1, 1))
}

func TestRateLimiterDifferentIPs(t *testing.T) {
	rl := NewRateLimiter()

	assert.Equal(t, true, rl.Allow("192.0.2.1", 1, 60))
	assert.Equal(t, false, rl.Allow("192.0.2.1", 1, 60))
	assert.Equal(t, true, rl.Allow("192.0.2.2", 1, 60))
}

func TestBanTrackerSameCredentialDoesNotTriggerBan(t *testing.T) {
	bt := NewBanTracker()
	window := time.Minute

	for i := 0; i < 100; i++ {
		bt.RecordAndBan("192.0.2.1", "fingerprint-same", 5, time.Hour, window)
	}

	assert.Equal(t, false, bt.IsBanned("192.0.2.1"))
}

func TestBanTrackerDifferentCredentialsTriggerBan(t *testing.T) {
	bt := NewBanTracker()
	window := time.Minute

	var banned bool
	for i := 0; i < 5; i++ {
		banned = bt.RecordAndBan("192.0.2.1", "fingerprint-"+string(rune('a'+i)), 5, time.Hour, window)
	}

	assert.Equal(t, true, banned)
	assert.Equal(t, true, bt.IsBanned("192.0.2.1"))
}

func TestBanTrackerMixSameAndDifferent(t *testing.T) {
	bt := NewBanTracker()
	window := time.Minute

	// Same credential 10 times - should be deduplicated to 1 unique fingerprint
	for i := 0; i < 10; i++ {
		bt.RecordAndBan("192.0.2.1", "fingerprint-config-error", 5, time.Hour, window)
	}

	// 3 different credentials - total unique = 1 + 3 = 4, below threshold 5
	var banned bool
	for i := 0; i < 3; i++ {
		banned = bt.RecordAndBan("192.0.2.1", "fingerprint-attack-"+string(rune('a'+i)), 5, time.Hour, window)
	}
	assert.Equal(t, false, banned)

	// 4th different credential - total unique = 1 + 4 = 5, should trigger ban
	banned = bt.RecordAndBan("192.0.2.1", "fingerprint-attack-d", 5, time.Hour, window)
	assert.Equal(t, true, banned)
}

func TestBanExpiresAfterDuration(t *testing.T) {
	clock := testutil.NewFakeClock(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	bt := newFakeBanTracker(clock)
	window := time.Minute

	var banned bool
	for i := 0; i < 5; i++ {
		banned = bt.RecordAndBan("192.0.2.1", "fingerprint-"+string(rune('a'+i)), 5, time.Second, window)
	}
	assert.Equal(t, true, banned)
	assert.Equal(t, true, bt.IsBanned("192.0.2.1"))

	clock.Advance(1100 * time.Millisecond)
	assert.Equal(t, false, bt.IsBanned("192.0.2.1"))
}

func TestManualUnban(t *testing.T) {
	bt := NewBanTracker()
	window := time.Minute

	for i := 0; i < 5; i++ {
		bt.RecordAndBan("192.0.2.1", "fingerprint-"+string(rune('a'+i)), 5, time.Hour, window)
	}

	assert.Equal(t, true, bt.IsBanned("192.0.2.1"))

	bt.Unban("192.0.2.1")
	assert.Equal(t, false, bt.IsBanned("192.0.2.1"))
}

func TestListBans(t *testing.T) {
	bt := NewBanTracker()
	window := time.Minute

	for i := 0; i < 5; i++ {
		bt.RecordAndBan("192.0.2.1", "fingerprint-"+string(rune('a'+i)), 5, time.Hour, window)
	}

	bans := bt.ListBans()
	assert.Equal(t, 1, len(bans))
	assert.Equal(t, "192.0.2.1", bans[0].IP)
	assert.Equal(t, 5, bans[0].UniqueFailures)
}

func TestFingerprintGeneration(t *testing.T) {
	fp1 := FingerprintLogin("admin")
	fp2 := FingerprintLogin("admin")
	fp3 := FingerprintLogin("other")

	assert.Equal(t, fp1, fp2)
	assert.Assert(t, fp1 != fp3)

	// go-secguard normalizes username case/whitespace before hashing.
	assert.Equal(t, fp1, FingerprintLogin("  ADMIN "))
}

func TestBanTrackerRespectsWindowSeconds(t *testing.T) {
	clock := testutil.NewFakeClock(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	bt := newFakeBanTracker(clock)

	// Two distinct failures recorded now.
	bt.RecordAndBan("192.0.2.1", "fp-1", 2, time.Hour, time.Minute)
	bt.RecordAndBan("192.0.2.1", "fp-2", 2, time.Hour, time.Minute)
	assert.Equal(t, true, bt.IsBanned("192.0.2.1"))

	// A fresh tracker with failures older than the window must not ban.
	old := newFakeBanTracker(clock)
	clock.Advance(-3 * time.Minute)
	old.RecordAndBan("192.0.2.2", "fp-old-1", 2, time.Hour, time.Minute)
	old.RecordAndBan("192.0.2.2", "fp-old-2", 2, time.Hour, time.Minute)
	clock.Advance(3 * time.Minute)
	// The window (60s) has elapsed, so the failures are expired: no ban.
	assert.Equal(t, false, old.RecordAndBan("192.0.2.2", "fp-new", 2, time.Hour, time.Minute))
}

func TestRateLimiterSweepRemovesExpiredKeys(t *testing.T) {
	clock := testutil.NewFakeClock(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	rl := newFakeRateLimiter(clock)

	rl.Allow("192.0.2.2", 10, 60)
	assert.Equal(t, 1, rl.Len())

	// Age the entry past the sweep window.
	clock.Advance(10 * time.Minute)
	rl.Sweep(60)

	assert.Equal(t, 0, rl.Len())
}

func TestBanTrackerSweepRemovesExpired(t *testing.T) {
	clock := testutil.NewFakeClock(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	bt := newFakeBanTracker(clock)

	bt.RecordAndBan("192.0.2.1", "fp-old", 5, time.Hour, time.Minute)
	bt.RecordAndBan("192.0.2.2", "fp-new", 5, time.Hour, time.Minute)
	bt.RecordAndBan("192.0.2.3", "fp-ban", 1, time.Second, time.Minute)

	clock.Advance(10 * time.Minute)
	bt.Sweep(time.Minute)

	// The expired ban is gone, and the failure history is pruned.
	assert.Equal(t, false, bt.IsBanned("192.0.2.3"))
	assert.Equal(t, 0, len(bt.ListBans()))
}

func TestClampWindow(t *testing.T) {
	assert.Equal(t, 300, clampWindow(0))
	assert.Equal(t, 300, clampWindow(-5))
	assert.Equal(t, 60, clampWindow(60))
}

func TestRateLimiterAndBanTrackerConcurrent(_ *testing.T) {
	rl := NewRateLimiter()
	bt := NewBanTracker()

	done := make(chan struct{})
	for i := 0; i < 8; i++ {
		go func() {
			defer func() { done <- struct{}{} }()
			for j := 0; j < 50; j++ {
				rl.Allow("192.0.2.1", 10, 60)
				rl.Sweep(60)
				bt.RecordAndBan("192.0.2.1", "fp", 5, time.Hour, time.Minute)
				bt.Sweep(time.Minute)
				bt.IsBanned("192.0.2.1")
			}
		}()
	}
	for i := 0; i < 8; i++ {
		<-done
	}
}

func TestExtractSignatureValue(t *testing.T) {
	t.Run("github", func(t *testing.T) {
		req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/ch", nil)
		req.Header.Set("X-Hub-Signature-256", "sha256=abc123")
		assert.Equal(t, "sha256=abc123", ExtractSignatureValue(req))
	})

	t.Run("gitlab", func(t *testing.T) {
		req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/ch", nil)
		req.Header.Set("X-Gitlab-Token", "my-secret")
		assert.Equal(t, "my-secret", ExtractSignatureValue(req))
	})

	t.Run("bitbucket", func(t *testing.T) {
		req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/ch", nil)
		req.Header.Set("X-Hub-Signature", "sha256=def456")
		assert.Equal(t, "sha256=def456", ExtractSignatureValue(req))
	})

	t.Run("gitea", func(t *testing.T) {
		req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/ch", nil)
		req.Header.Set("X-Gitea-Signature", "sha256=ghi789")
		assert.Equal(t, "sha256=ghi789", ExtractSignatureValue(req))
	})

	t.Run("unknown", func(t *testing.T) {
		req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/ch", nil)
		assert.Equal(t, "unknown", ExtractSignatureValue(req))
	})
}

// F12: concurrent sweep/record/ban must be race-free (run with -race).
func TestBanTrackerConcurrentSweep(_ *testing.T) {
	bt := NewBanTracker()

	done := make(chan struct{})
	for i := 0; i < 8; i++ {
		go func(n int) {
			defer func() { done <- struct{}{} }()
			for j := 0; j < 100; j++ {
				bt.RecordAndBan("192.0.2.1", fmt.Sprintf("fp-%d", n), 5, time.Hour, time.Minute)
				bt.Sweep(time.Minute)
				bt.IsBanned("192.0.2.1")
				bt.ListBans()
			}
		}(i)
	}
	for i := 0; i < 8; i++ {
		<-done
	}
}

// F18: forwarded headers are trusted only from configured proxy CIDRs, walked
// right-to-left.
func TestGetRealIPTrustedProxyCIDR(t *testing.T) {
	t.Run("untrusted peer ignores XFF", func(t *testing.T) {
		req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/", nil)
		req.RemoteAddr = "203.0.113.9:1234"
		req.Header.Set("X-Forwarded-For", "10.0.0.1")
		ip, err := GetRealIP(req, true, ParseTrustedProxyCIDRs([]string{"192.168.0.0/16"}))
		assert.NilError(t, err)
		assert.Equal(t, ip.String(), "203.0.113.9")
	})

	t.Run("trusted peer walks XFF right-to-left", func(t *testing.T) {
		req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/", nil)
		req.RemoteAddr = "192.168.1.1:1"
		req.Header.Set("X-Forwarded-For", "198.51.100.7, 10.0.0.1")
		trusted := ParseTrustedProxyCIDRs([]string{"192.168.0.0/16", "10.0.0.0/8"})
		ip, err := GetRealIP(req, true, trusted)
		assert.NilError(t, err)
		assert.Equal(t, ip.String(), "198.51.100.7")
	})

	t.Run("behind proxy disabled uses peer", func(t *testing.T) {
		req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/", nil)
		req.RemoteAddr = "192.168.1.1:1"
		req.Header.Set("X-Forwarded-For", "198.51.100.7")
		ip, err := GetRealIP(req, false, nil)
		assert.NilError(t, err)
		assert.Equal(t, ip.String(), "192.168.1.1")
	})
}

func TestValidateTrustedProxyCIDRs(t *testing.T) {
	assert.NilError(t, ValidateTrustedProxyCIDRs([]string{"192.168.0.0/16", "10.0.0.1"}))
	assert.Assert(t, ValidateTrustedProxyCIDRs([]string{"not-a-cidr"}) != nil)
}
