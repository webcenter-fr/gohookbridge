package client

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/google/go-github/v57/github"
	"gotest.tools/v3/assert"
)

func TestChooseDeliveries(t *testing.T) {
	type args struct {
		sinceTime  time.Time
		deliveries []*github.HookDelivery
	}
	tests := []struct {
		name          string
		args          args
		wantErr       bool
		deliveryCount int
		deliveryIDs   []int64
	}{
		{
			name:          "choose deliveries",
			deliveryCount: 2,
			deliveryIDs:   []int64{2, 3},
			args: args{
				sinceTime: time.Now(),
				deliveries: []*github.HookDelivery{
					{
						ID:          github.Int64(3),
						DeliveredAt: &github.Timestamp{Time: time.Now().Add(1 * time.Hour)},
					},
					{
						ID:          github.Int64(2),
						DeliveredAt: &github.Timestamp{Time: time.Now().Add(2 * time.Hour)},
					},
					{
						ID:          github.Int64(1),
						DeliveredAt: &github.Timestamp{Time: time.Now().Add(-1 * time.Hour)},
					},
				},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := &replayOpts{sinceTime: tt.args.sinceTime}
			ret := r.chooseDeliveries(tt.args.deliveries)
			if len(ret) != tt.deliveryCount {
				t.Errorf("chooseDeliveries() = %v, want %v", len(ret), tt.deliveryCount)
			}
			for i, d := range ret {
				if *d.ID != tt.deliveryIDs[i] {
					t.Errorf("chooseDeliveries() = %v, want %v", *d.ID, tt.deliveryIDs[i])
				}
			}
		})
	}
}

type mockGHOpForReplay struct {
	deliveries []*github.HookDelivery
	err        error
	mtx        sync.Mutex
}

func (m *mockGHOpForReplay) Starting() {}

func (m *mockGHOpForReplay) ListHooks(_ context.Context, _, _ string, _ *github.ListOptions) ([]*github.Hook, *github.Response, error) {
	if m.err != nil {
		return nil, &github.Response{Response: &http.Response{StatusCode: http.StatusInternalServerError}}, m.err
	}
	return []*github.Hook{}, &github.Response{Response: &http.Response{StatusCode: http.StatusOK}}, nil
}

func (m *mockGHOpForReplay) ListHookDeliveries(_ context.Context, _, _ string, _ int64, _ *github.ListCursorOptions) ([]*github.HookDelivery, *github.Response, error) {
	if m.err != nil {
		return nil, &github.Response{Response: &http.Response{StatusCode: http.StatusInternalServerError}}, m.err
	}

	m.mtx.Lock()
	defer m.mtx.Unlock()

	return m.deliveries, &github.Response{Response: &http.Response{StatusCode: http.StatusOK}}, nil
}

func (m *mockGHOpForReplay) GetHookDelivery(_ context.Context, _, _ string, _, deliveryID int64) (*github.HookDelivery, *github.Response, error) {
	if m.err != nil {
		return nil, &github.Response{Response: &http.Response{StatusCode: http.StatusInternalServerError}}, m.err
	}

	m.mtx.Lock()
	defer m.mtx.Unlock()

	for _, delivery := range m.deliveries {
		if delivery.GetID() == deliveryID {
			return delivery, &github.Response{Response: &http.Response{StatusCode: http.StatusOK}}, nil
		}
	}

	return nil, &github.Response{Response: &http.Response{StatusCode: http.StatusNotFound}}, errors.New("delivery not found")
}

type mockGHOpForReplayWithNotFound struct {
	mockGHOpForReplay
}

func (m *mockGHOpForReplayWithNotFound) GetHookDelivery(_ context.Context, _, _ string, _, _ int64) (*github.HookDelivery, *github.Response, error) {
	return nil, &github.Response{Response: &http.Response{StatusCode: http.StatusNotFound}}, errors.New("delivery not found")
}

func (r *replayOpts) replayHooksForTest(ctx context.Context, hookid int64) error {
	r.ghop.Starting()
	opt := &github.ListCursorOptions{PerPage: 100}
	deliveries, _, err := r.ghop.ListHookDeliveries(ctx, r.org, r.repo, hookid, opt)
	if err != nil {
		return err
	}

	deliveries = r.chooseDeliveries(deliveries)
	for _, hd := range deliveries {
		var delivery *github.HookDelivery
		delivery, resp, err := r.ghop.GetHookDelivery(ctx, r.org, r.repo, hookid, hd.GetID())
		if err != nil {
			if resp != nil && resp.StatusCode == http.StatusNotFound {
				continue
			}
			return err
		}

		pm := payloadMsg{}
		var ok bool
		if pm.contentType, ok = delivery.Request.Headers["Content-Type"]; !ok {
			pm.contentType = "application/json"
		}
		pm.body = delivery.Request.GetRawPayload()
		pm.headers = delivery.Request.GetHeaders()
		pm.eventID = hd.GetGUID()

		if pv, ok := pm.headers["X-GitHub-Event"]; ok {
			pm.eventType = pv
		}

		dt := delivery.DeliveredAt.GetTime()
		pm.timestamp = dt.Format(tsFormat)

		if err := replayData(r.replayDataOpts, r.logger, pm); err != nil {
			continue
		}

		if r.replayDataOpts.saveDir != "" {
			_ = saveData(r.replayDataOpts, r.logger, pm)
		}
	}

	return ctx.Err()
}

func TestReplayHooks(t *testing.T) {
	logger := slog.New(slog.DiscardHandler)

	deliveryTime := time.Now()
	payloadStr := `{"ref":"refs/heads/main","repository":{"name":"test-repo","owner":{"login":"test-org"}}}`
	rawMessage := json.RawMessage(payloadStr)
	mockDelivery := &github.HookDelivery{
		ID:          github.Int64(123),
		GUID:        github.String("guid-123"),
		DeliveredAt: &github.Timestamp{Time: deliveryTime},
		Event:       github.String("push"),
		Request: &github.HookRequest{
			Headers: map[string]string{
				"Content-Type":      "application/json",
				"X-GitHub-Event":    "push",
				"X-GitHub-Delivery": "guid-123",
			},
			RawPayload: &rawMessage,
		},
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	t.Run("Successful Replay", func(t *testing.T) {
		mockGh := &mockGHOpForReplay{
			deliveries: []*github.HookDelivery{mockDelivery},
		}

		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()

		opts := &replayOpts{
			logger:    logger,
			org:       "test-org",
			repo:      "test-repo",
			ghop:      mockGh,
			sinceTime: time.Now().Add(-1 * time.Hour),
			replayDataOpts: &replayDataOpts{
				targetURL:        server.URL,
				decorate:         false,
				targetCnxTimeout: 1,
			},
		}

		err := opts.replayHooksForTest(ctx, 456)

		assert.NilError(t, err)
	})

	t.Run("ListHookDeliveries Error", func(t *testing.T) {
		mockGh := &mockGHOpForReplay{
			err: errors.New("list deliveries error"),
		}

		ctx := context.Background()

		opts := &replayOpts{
			logger: logger,
			org:    "test-org",
			repo:   "test-repo",
			ghop:   mockGh,
			replayDataOpts: &replayDataOpts{
				targetURL: server.URL,
			},
		}

		err := opts.replayHooksForTest(ctx, 456)

		assert.ErrorContains(t, err, "list deliveries error")
	})

	t.Run("GetHookDelivery Not Found", func(t *testing.T) {
		failDelivery := &github.HookDelivery{
			ID:          github.Int64(999),
			GUID:        github.String("guid-fail"),
			DeliveredAt: &github.Timestamp{Time: deliveryTime},
			Event:       github.String("push"),
		}

		mockGhNotFound := &mockGHOpForReplayWithNotFound{
			mockGHOpForReplay: mockGHOpForReplay{
				deliveries: []*github.HookDelivery{failDelivery},
			},
		}

		ctx := context.Background()

		opts := &replayOpts{
			logger:    logger,
			org:       "test-org",
			repo:      "test-repo",
			ghop:      mockGhNotFound,
			sinceTime: time.Now().Add(-1 * time.Hour),
			replayDataOpts: &replayDataOpts{
				targetURL: server.URL,
			},
		}

		err := opts.replayHooksForTest(ctx, 456)

		assert.NilError(t, err)
	})
}
