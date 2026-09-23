package service

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"gotest.tools/v3/assert"
)

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
	rl := NewRateLimiter()

	assert.Equal(t, true, rl.Allow("192.0.2.1", 1, 1))
	assert.Equal(t, false, rl.Allow("192.0.2.1", 1, 1))

	time.Sleep(1100 * time.Millisecond)

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
	window := 60

	for i := 0; i < 100; i++ {
		bt.recordFailure("192.0.2.1", "fingerprint-same", window)
	}

	banned := bt.banIfSuspicious("192.0.2.1", 5, 3600, window)
	assert.Equal(t, false, banned)
}

func TestBanTrackerDifferentCredentialsTriggerBan(t *testing.T) {
	bt := NewBanTracker()
	window := 60

	for i := 0; i < 5; i++ {
		bt.recordFailure("192.0.2.1", "fingerprint-"+string(rune('a'+i)), window)
	}

	banned := bt.banIfSuspicious("192.0.2.1", 5, 3600, window)
	assert.Equal(t, true, banned)
	assert.Equal(t, true, bt.IsBanned("192.0.2.1"))
}

func TestBanTrackerMixSameAndDifferent(t *testing.T) {
	bt := NewBanTracker()
	window := 60

	// Same credential 10 times - should be deduplicated to 1 unique fingerprint
	for i := 0; i < 10; i++ {
		bt.recordFailure("192.0.2.1", "fingerprint-config-error", window)
	}

	// 3 different credentials - total unique = 1 + 3 = 4, below threshold 5
	for i := 0; i < 3; i++ {
		bt.recordFailure("192.0.2.1", "fingerprint-attack-"+string(rune('a'+i)), window)
	}

	banned := bt.banIfSuspicious("192.0.2.1", 5, 3600, window)
	assert.Equal(t, false, banned)

	// 4th different credential - total unique = 1 + 4 = 5, should trigger ban
	bt.recordFailure("192.0.2.1", "fingerprint-attack-d", window)
	banned = bt.banIfSuspicious("192.0.2.1", 5, 3600, window)
	assert.Equal(t, true, banned)
}

func TestBanExpiresAfterDuration(t *testing.T) {
	bt := NewBanTracker()
	window := 60

	for i := 0; i < 5; i++ {
		bt.recordFailure("192.0.2.1", "fingerprint-"+string(rune('a'+i)), window)
	}

	banned := bt.banIfSuspicious("192.0.2.1", 5, 1, window)
	assert.Equal(t, true, banned)
	assert.Equal(t, true, bt.IsBanned("192.0.2.1"))

	time.Sleep(1100 * time.Millisecond)
	assert.Equal(t, false, bt.IsBanned("192.0.2.1"))
}

func TestManualUnban(t *testing.T) {
	bt := NewBanTracker()
	window := 60

	for i := 0; i < 5; i++ {
		bt.recordFailure("192.0.2.1", "fingerprint-"+string(rune('a'+i)), window)
	}

	banned := bt.banIfSuspicious("192.0.2.1", 5, 3600, window)
	assert.Equal(t, true, banned)
	assert.Equal(t, true, bt.IsBanned("192.0.2.1"))

	bt.Unban("192.0.2.1")
	assert.Equal(t, false, bt.IsBanned("192.0.2.1"))
}

func TestListBans(t *testing.T) {
	bt := NewBanTracker()
	window := 60

	for i := 0; i < 5; i++ {
		bt.recordFailure("192.0.2.1", "fingerprint-"+string(rune('a'+i)), window)
	}
	bt.banIfSuspicious("192.0.2.1", 5, 3600, window)

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
}

func TestBanIfSuspiciousRespectsWindowSeconds(t *testing.T) {
	mk := func() *BanTracker {
		bt := NewBanTracker()
		bt.failures["192.0.2.1"] = []banEntry{
			{fingerprint: "fp-old-1", timestamp: time.Now().Add(-2 * time.Minute)},
			{fingerprint: "fp-old-2", timestamp: time.Now().Add(-2 * time.Minute)},
		}
		return bt
	}

	// A short window (e.g. 10s) expires both fingerprints → no ban.
	assert.Equal(t, false, mk().banIfSuspicious("192.0.2.1", 2, 3600, 10))

	// A long window (e.g. 600s) keeps them → ban.
	assert.Equal(t, true, mk().banIfSuspicious("192.0.2.1", 2, 3600, 600))
}

func TestRateLimiterSweepRemovesExpiredKeys(t *testing.T) {
	rl := NewRateLimiter()
	rl.entries["192.0.2.1"] = []time.Time{time.Now().Add(-10 * time.Minute)}
	rl.entries["192.0.2.2"] = []time.Time{time.Now()}

	rl.Sweep(60)

	assert.Equal(t, 1, len(rl.entries))
	_, ok := rl.entries["192.0.2.2"]
	assert.Equal(t, true, ok)
}

func TestBanTrackerSweepRemovesExpired(t *testing.T) {
	bt := NewBanTracker()
	bt.failures["192.0.2.1"] = []banEntry{{fingerprint: "fp-old", timestamp: time.Now().Add(-10 * time.Minute)}}
	bt.failures["192.0.2.2"] = []banEntry{{fingerprint: "fp-new", timestamp: time.Now()}}
	bt.banned["192.0.2.3"] = time.Now().Add(-10 * time.Minute)
	bt.banned["192.0.2.4"] = time.Now().Add(10 * time.Minute)

	bt.Sweep(60)

	_, okOld := bt.failures["192.0.2.1"]
	assert.Equal(t, false, okOld)
	_, okNew := bt.failures["192.0.2.2"]
	assert.Equal(t, true, okNew)
	_, okBanOld := bt.banned["192.0.2.3"]
	assert.Equal(t, false, okBanOld)
	_, okBanNew := bt.banned["192.0.2.4"]
	assert.Equal(t, true, okBanNew)
}

func TestClampWindow(t *testing.T) {
	assert.Equal(t, 300, clampWindow(0))
	assert.Equal(t, 300, clampWindow(-5))
	assert.Equal(t, 60, clampWindow(60))
}

func TestRateLimiterAndBanTrackerConcurrent(t *testing.T) {
	rl := NewRateLimiter()
	bt := NewBanTracker()

	done := make(chan struct{})
	for i := 0; i < 8; i++ {
		go func() {
			defer func() { done <- struct{}{} }()
			for j := 0; j < 50; j++ {
				rl.Allow("192.0.2.1", 10, 60)
				rl.Sweep(60)
				bt.recordFailure("192.0.2.1", "fp", 60)
				bt.banIfSuspicious("192.0.2.1", 5, 3600, 60)
				bt.Sweep(60)
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
