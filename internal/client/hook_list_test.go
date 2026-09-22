package client

import (
	"context"
	"errors"
	"log/slog"
	"testing"
	"time"

	"github.com/google/go-github/v57/github"
	"gotest.tools/v3/assert"
)

type mockGHOp struct {
	hooks      []*github.Hook
	deliveries []*github.HookDelivery
	err        error
}

func (m *mockGHOp) Starting() {}

func (m *mockGHOp) ListHooks(_ context.Context, _, _ string, _ *github.ListOptions) ([]*github.Hook, *github.Response, error) {
	if m.err != nil {
		return nil, &github.Response{}, m.err
	}
	return m.hooks, &github.Response{}, nil
}

func (m *mockGHOp) ListHookDeliveries(_ context.Context, _, _ string, _ int64, _ *github.ListCursorOptions) ([]*github.HookDelivery, *github.Response, error) {
	if m.err != nil {
		return nil, &github.Response{}, m.err
	}
	return m.deliveries, &github.Response{}, nil
}

func (m *mockGHOp) GetHookDelivery(_ context.Context, _, _ string, _, _ int64) (*github.HookDelivery, *github.Response, error) {
	if m.err != nil {
		return nil, &github.Response{}, m.err
	}
	if len(m.deliveries) > 0 {
		return m.deliveries[0], &github.Response{}, nil
	}
	return nil, &github.Response{}, nil
}

func TestListHooks(t *testing.T) {
	logger := slog.New(slog.DiscardHandler)

	t.Run("Success", func(t *testing.T) {
		hooks := []*github.Hook{
			{
				ID:   github.Int64(123),
				Name: github.String("web"),
				Config: map[string]any{
					"url": "https://example.com/webhook",
				},
			},
			{
				ID:   github.Int64(456),
				Name: github.String("custom"),
				Config: map[string]any{
					"url": "https://test.com/webhook",
				},
			},
		}

		mockGh := &mockGHOp{hooks: hooks}

		opts := &replayOpts{
			logger: logger,
			org:    "testorg",
			repo:   "testrepo",
			ghop:   mockGh,
		}

		err := opts.listHooks(context.Background())

		assert.NilError(t, err)
	})

	t.Run("Error", func(t *testing.T) {
		mockGh := &mockGHOp{err: errors.New("test error")}

		opts := &replayOpts{
			logger: logger,
			org:    "testorg",
			repo:   "testrepo",
			ghop:   mockGh,
		}

		err := opts.listHooks(context.Background())

		assert.Assert(t, err != nil)
	})
}

func TestListDeliveries(t *testing.T) {
	logger := slog.New(slog.DiscardHandler)

	t.Run("Success", func(t *testing.T) {
		deliveryTime := time.Now()
		deliveries := []*github.HookDelivery{
			{
				ID:          github.Int64(789),
				GUID:        github.String("guid-1"),
				DeliveredAt: &github.Timestamp{Time: deliveryTime},
				Event:       github.String("push"),
			},
			{
				ID:          github.Int64(101112),
				GUID:        github.String("guid-2"),
				DeliveredAt: &github.Timestamp{Time: deliveryTime.Add(-1 * time.Hour)},
				Event:       github.String("pull_request"),
			},
		}

		mockGh := &mockGHOp{deliveries: deliveries}

		opts := &replayOpts{
			logger: logger,
			org:    "testorg",
			repo:   "testrepo",
			ghop:   mockGh,
		}

		err := opts.listDeliveries(context.Background(), 123)

		assert.NilError(t, err)
	})

	t.Run("Error", func(t *testing.T) {
		mockGh := &mockGHOp{err: errors.New("test error")}

		opts := &replayOpts{
			logger: logger,
			org:    "testorg",
			repo:   "testrepo",
			ghop:   mockGh,
		}

		err := opts.listDeliveries(context.Background(), 123)

		assert.Assert(t, err != nil)
	})
}
