package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"gotest.tools/v3/assert"
)

func TestRateLimiterAllowsUnderLimit(t *testing.T) {
	rl := newRateLimiter()

	for i := 0; i < 5; i++ {
		assert.Equal(t, true, rl.allow("192.0.2.1", 10, 60))
	}
}

func TestRateLimiterRejectsOverLimit(t *testing.T) {
	rl := newRateLimiter()

	for i := 0; i < 3; i++ {
		assert.Equal(t, true, rl.allow("192.0.2.1", 3, 60))
	}
	assert.Equal(t, false, rl.allow("192.0.2.1", 3, 60))
}

func TestRateLimiterWindowExpiry(t *testing.T) {
	rl := newRateLimiter()

	assert.Equal(t, true, rl.allow("192.0.2.1", 1, 1))
	assert.Equal(t, false, rl.allow("192.0.2.1", 1, 1))

	time.Sleep(1100 * time.Millisecond)

	assert.Equal(t, true, rl.allow("192.0.2.1", 1, 1))
}

func TestRateLimiterDifferentIPs(t *testing.T) {
	rl := newRateLimiter()

	assert.Equal(t, true, rl.allow("192.0.2.1", 1, 60))
	assert.Equal(t, false, rl.allow("192.0.2.1", 1, 60))
	assert.Equal(t, true, rl.allow("192.0.2.2", 1, 60))
}

func TestBanTrackerSameCredentialDoesNotTriggerBan(t *testing.T) {
	bt := newBanTracker()
	window := 60

	for i := 0; i < 100; i++ {
		bt.recordFailure("192.0.2.1", "fingerprint-same", window)
	}

	banned := bt.banIfSuspicious("192.0.2.1", 5, 3600)
	assert.Equal(t, false, banned)
}

func TestBanTrackerDifferentCredentialsTriggerBan(t *testing.T) {
	bt := newBanTracker()
	window := 60

	for i := 0; i < 5; i++ {
		bt.recordFailure("192.0.2.1", "fingerprint-"+string(rune('a'+i)), window)
	}

	banned := bt.banIfSuspicious("192.0.2.1", 5, 3600)
	assert.Equal(t, true, banned)
	assert.Equal(t, true, bt.isBanned("192.0.2.1"))
}

func TestBanTrackerMixSameAndDifferent(t *testing.T) {
	bt := newBanTracker()
	window := 60

	// Same credential 10 times - should be deduplicated to 1 unique fingerprint
	for i := 0; i < 10; i++ {
		bt.recordFailure("192.0.2.1", "fingerprint-config-error", window)
	}

	// 3 different credentials - total unique = 1 + 3 = 4, below threshold 5
	for i := 0; i < 3; i++ {
		bt.recordFailure("192.0.2.1", "fingerprint-attack-"+string(rune('a'+i)), window)
	}

	banned := bt.banIfSuspicious("192.0.2.1", 5, 3600)
	assert.Equal(t, false, banned)

	// 4th different credential - total unique = 1 + 4 = 5, should trigger ban
	bt.recordFailure("192.0.2.1", "fingerprint-attack-d", window)
	banned = bt.banIfSuspicious("192.0.2.1", 5, 3600)
	assert.Equal(t, true, banned)
}

func TestBanExpiresAfterDuration(t *testing.T) {
	bt := newBanTracker()
	window := 60

	for i := 0; i < 5; i++ {
		bt.recordFailure("192.0.2.1", "fingerprint-"+string(rune('a'+i)), window)
	}

	banned := bt.banIfSuspicious("192.0.2.1", 5, 1)
	assert.Equal(t, true, banned)
	assert.Equal(t, true, bt.isBanned("192.0.2.1"))

	time.Sleep(1100 * time.Millisecond)
	assert.Equal(t, false, bt.isBanned("192.0.2.1"))
}

func TestManualUnban(t *testing.T) {
	bt := newBanTracker()
	window := 60

	for i := 0; i < 5; i++ {
		bt.recordFailure("192.0.2.1", "fingerprint-"+string(rune('a'+i)), window)
	}

	banned := bt.banIfSuspicious("192.0.2.1", 5, 3600)
	assert.Equal(t, true, banned)
	assert.Equal(t, true, bt.isBanned("192.0.2.1"))

	bt.unban("192.0.2.1")
	assert.Equal(t, false, bt.isBanned("192.0.2.1"))
}

func TestListBans(t *testing.T) {
	bt := newBanTracker()
	window := 60

	for i := 0; i < 5; i++ {
		bt.recordFailure("192.0.2.1", "fingerprint-"+string(rune('a'+i)), window)
	}
	bt.banIfSuspicious("192.0.2.1", 5, 3600)

	bans := bt.listBans()
	assert.Equal(t, 1, len(bans))
	assert.Equal(t, "192.0.2.1", bans[0].IP)
	assert.Equal(t, 5, bans[0].UniqueFailures)
}

func TestFingerprintGeneration(t *testing.T) {
	fp1 := fingerprintLogin("admin")
	fp2 := fingerprintLogin("admin")
	fp3 := fingerprintLogin("other")

	assert.Equal(t, fp1, fp2)
	assert.Assert(t, fp1 != fp3)
}

func TestExtractSignatureValue(t *testing.T) {
	t.Run("github", func(t *testing.T) {
		req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/ch", nil)
		req.Header.Set("X-Hub-Signature-256", "sha256=abc123")
		assert.Equal(t, "sha256=abc123", extractSignatureValue(req))
	})

	t.Run("gitlab", func(t *testing.T) {
		req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/ch", nil)
		req.Header.Set("X-Gitlab-Token", "my-secret")
		assert.Equal(t, "my-secret", extractSignatureValue(req))
	})

	t.Run("bitbucket", func(t *testing.T) {
		req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/ch", nil)
		req.Header.Set("X-Hub-Signature", "sha256=def456")
		assert.Equal(t, "sha256=def456", extractSignatureValue(req))
	})

	t.Run("gitea", func(t *testing.T) {
		req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/ch", nil)
		req.Header.Set("X-Gitea-Signature", "sha256=ghi789")
		assert.Equal(t, "sha256=ghi789", extractSignatureValue(req))
	})

	t.Run("unknown", func(t *testing.T) {
		req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/ch", nil)
		assert.Equal(t, "unknown", extractSignatureValue(req))
	})
}

func TestBanMiddlewareDisabled(t *testing.T) {
	bt := newBanTracker()

	bt.recordFailure("127.0.0.1", "fp1", 60)
	bt.banIfSuspicious("127.0.0.1", 1, 3600)
	assert.Equal(t, true, bt.isBanned("127.0.0.1"))

	bt.unban("127.0.0.1")
	assert.Equal(t, false, bt.isBanned("127.0.0.1"))
}

func TestRateLimitMiddlewareDisabled(t *testing.T) {
	rl := newRateLimiter()

	for i := 0; i < 10; i++ {
		rl.allow("127.0.0.1", 1, 60)
	}

	assert.Equal(t, false, rl.allow("127.0.0.1", 1, 60))
}

func TestAPIUnbanHandlerInvalidIP(t *testing.T) {
	bt := newBanTracker()

	t.Run("rejects empty ip", func(t *testing.T) {
		req := httptest.NewRequestWithContext(context.Background(), http.MethodDelete, "/api/bans/", nil)
		rctx := chi.NewRouteContext()
		rctx.URLParams.Add("ip", "")
		req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))

		w := httptest.NewRecorder()
		apiUnbanHandler(bt)(w, req)
		assert.Equal(t, http.StatusBadRequest, w.Result().StatusCode)
	})

	t.Run("rejects invalid ip", func(t *testing.T) {
		req := httptest.NewRequestWithContext(context.Background(), http.MethodDelete, "/api/bans/not-an-ip", nil)
		rctx := chi.NewRouteContext()
		rctx.URLParams.Add("ip", "not-an-ip")
		req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))

		w := httptest.NewRecorder()
		apiUnbanHandler(bt)(w, req)
		assert.Equal(t, http.StatusBadRequest, w.Result().StatusCode)
	})
}
