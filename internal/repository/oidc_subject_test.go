package repository_test

import (
	"context"
	"errors"
	"testing"

	"github.com/webcenter-fr/gohookbridge/internal/domain"
	"github.com/webcenter-fr/gohookbridge/internal/repository/storetest"
	"gotest.tools/v3/assert"
)

func TestGetUserByOIDCSubject(t *testing.T) {
	rs := storetest.NewRaftStore(t)
	ctx := context.Background()

	assert.NilError(t, rs.CreateUser(ctx, &domain.User{
		ID:           "u1",
		Username:     "alice",
		OIDCSubjects: []string{"sub-1", "sub-2"},
		Roles:        []string{},
		Channels:     []string{},
	}))

	u, err := rs.GetUserByOIDCSubject(ctx, "sub-2")
	assert.NilError(t, err)
	assert.Equal(t, u.Username, "alice")
	assert.Equal(t, u.ID, "u1")

	_, err = rs.GetUserByOIDCSubject(ctx, "nope")
	assert.Assert(t, errors.Is(err, domain.ErrNotFound))
}
