package repository

import (
	"context"
	"errors"
	"testing"

	"github.com/webcenter-fr/gohookbridge/internal/domain"
	"gotest.tools/v3/assert"
)

// F35: the FSM's rejection error must propagate back through Apply so callers
// can map it to a domain error (e.g. 409) instead of silently succeeding.
func TestFSMApplicationErrorPropagated(t *testing.T) {
	rs := newTestRaftStore(t)
	ctx := context.Background()

	user := &domain.User{ID: "u1", Username: "u1"}
	assert.NilError(t, rs.CreateUser(ctx, user))
	err := rs.CreateUser(ctx, user)
	assert.Assert(t, errors.Is(err, domain.ErrAlreadyExists), "got %v", err)

	channel := &domain.Channel{ID: "c1"}
	assert.NilError(t, rs.CreateChannel(ctx, channel))
	err = rs.CreateChannel(ctx, channel)
	assert.Assert(t, errors.Is(err, domain.ErrAlreadyExists), "got %v", err)
}

// F35: multi-record writes (user + index, server + defaults) are applied in a
// single transaction: they either all land or none do.
func TestTransactionalMultiRecordWrites(t *testing.T) {
	rs := newTestRaftStore(t)
	ctx := context.Background()

	assert.NilError(t, rs.CreateUser(ctx, &domain.User{ID: "u1", Username: "old"}))

	got, err := rs.GetUser(ctx, "u1")
	assert.NilError(t, err)
	got.Username = "new"
	assert.NilError(t, rs.UpdateUser(ctx, got))

	_, err = rs.GetUserByUsername(ctx, "new")
	assert.NilError(t, err)
	_, err = rs.GetUserByUsername(ctx, "old")
	assert.Assert(t, errors.Is(err, domain.ErrNotFound), "old username index must be removed")

	cfg := domain.DefaultGlobalConfig()
	cfg.Server.MaxBodySize = 12345
	cfg.Defaults.WebhookSecret = "s3cr3t"
	assert.NilError(t, rs.UpdateGlobalConfig(ctx, cfg))
	stored, err := rs.GetGlobalConfig(ctx)
	assert.NilError(t, err)
	assert.Equal(t, stored.Server.MaxBodySize, 12345)
	assert.Equal(t, stored.Defaults.WebhookSecret, "s3cr3t")

	// A batch containing an invalid key must not partially apply.
	before, err := rs.GetUser(ctx, "u1")
	assert.NilError(t, err)
	err = rs.applyMulti([]fsmKV{
		{Key: "/users/u1/", Value: []byte(`{"id":"u1","username":"evil"}`)},
		{Key: "bad-key", Value: []byte("x")},
	})
	assert.Assert(t, err != nil)
	after, err := rs.GetUser(ctx, "u1")
	assert.NilError(t, err)
	assert.Equal(t, after.Username, before.Username, "failed batch must roll back entirely")
}
