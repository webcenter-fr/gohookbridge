package service

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/webcenter-fr/gohookbridge/internal/domain"
)

// fakeRepository is an in-memory domain.Repository used by service tests to
// prove the service layer never depends on the repository package.
type fakeRepository struct {
	mu        sync.Mutex
	channels  map[string]*domain.Channel
	users     map[string]*domain.User
	username  map[string]string // username -> user ID
	roles     map[string]*domain.Role
	rmappings map[string]*domain.RoleMapping
	acls      map[string][]*domain.ChannelRoleMapping // channelID -> entries
	config    *domain.GlobalConfig
	providers []domain.OIDCProvider
	cursors   map[string]*domain.ClientCursor
	setupEnd  time.Time
}

func newFakeRepository() *fakeRepository {
	return &fakeRepository{
		channels:  make(map[string]*domain.Channel),
		users:     make(map[string]*domain.User),
		username:  make(map[string]string),
		roles:     make(map[string]*domain.Role),
		rmappings: make(map[string]*domain.RoleMapping),
		acls:      make(map[string][]*domain.ChannelRoleMapping),
		config:    domain.DefaultGlobalConfig(),
		cursors:   make(map[string]*domain.ClientCursor),
	}
}

var _ domain.Repository = (*fakeRepository)(nil)

func (f *fakeRepository) GetChannel(_ context.Context, id string) (*domain.Channel, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	ch, ok := f.channels[id]
	if !ok {
		return nil, fmt.Errorf("%w: channel %q", domain.ErrNotFound, id)
	}
	cp := *ch
	return &cp, nil
}

func (f *fakeRepository) ListChannels(_ context.Context) ([]*domain.Channel, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]*domain.Channel, 0, len(f.channels))
	for _, ch := range f.channels {
		cp := *ch
		out = append(out, &cp)
	}
	return out, nil
}

func (f *fakeRepository) CreateChannel(_ context.Context, p *domain.Channel) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.channels[p.ID]; ok {
		return fmt.Errorf("%w: channel %q", domain.ErrAlreadyExists, p.ID)
	}
	cp := *p
	f.channels[p.ID] = &cp
	return nil
}

func (f *fakeRepository) UpdateChannel(_ context.Context, p *domain.Channel) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.channels[p.ID]; !ok {
		return fmt.Errorf("%w: channel %q", domain.ErrNotFound, p.ID)
	}
	cp := *p
	f.channels[p.ID] = &cp
	return nil
}

func (f *fakeRepository) DeleteChannel(_ context.Context, id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.channels, id)
	delete(f.acls, id)
	return nil
}

func (f *fakeRepository) CreateChannelRoleMapping(_ context.Context, m *domain.ChannelRoleMapping) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, e := range f.acls[m.ChannelID] {
		if e.Type == m.Type && e.Subject == m.Subject && e.Role == m.Role {
			return nil
		}
	}
	cp := *m
	if cp.ID == "" {
		cp.ID = fmt.Sprintf("acl-%d", len(f.acls[m.ChannelID])+1)
	}
	f.acls[m.ChannelID] = append(f.acls[m.ChannelID], &cp)
	return nil
}

func (f *fakeRepository) ListChannelRoleMappings(_ context.Context, channelID string) ([]domain.ChannelRoleMapping, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]domain.ChannelRoleMapping, 0, len(f.acls[channelID]))
	for _, e := range f.acls[channelID] {
		out = append(out, *e)
	}
	return out, nil
}

func (f *fakeRepository) DeleteChannelRoleMapping(_ context.Context, channelID, entryID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	kept := f.acls[channelID][:0]
	for _, e := range f.acls[channelID] {
		if e.ID != entryID {
			kept = append(kept, e)
		}
	}
	f.acls[channelID] = kept
	return nil
}

func (f *fakeRepository) GetUser(_ context.Context, id string) (*domain.User, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	u, ok := f.users[id]
	if !ok {
		return nil, fmt.Errorf("%w: user %q", domain.ErrNotFound, id)
	}
	cp := *u
	return &cp, nil
}

func (f *fakeRepository) GetUserByUsername(_ context.Context, username string) (*domain.User, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	id, ok := f.username[username]
	if !ok {
		return nil, fmt.Errorf("%w: user %q", domain.ErrNotFound, username)
	}
	u, ok := f.users[id]
	if !ok {
		return nil, fmt.Errorf("%w: user %q", domain.ErrNotFound, username)
	}
	cp := *u
	return &cp, nil
}

func (f *fakeRepository) ListUsers(_ context.Context) ([]*domain.User, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]*domain.User, 0, len(f.users))
	for _, u := range f.users {
		cp := *u
		out = append(out, &cp)
	}
	return out, nil
}

func (f *fakeRepository) CreateUser(_ context.Context, u *domain.User) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.users[u.ID]; ok {
		return fmt.Errorf("%w: user %q", domain.ErrAlreadyExists, u.ID)
	}
	cp := *u
	f.users[u.ID] = &cp
	f.username[u.Username] = u.ID
	return nil
}

func (f *fakeRepository) UpdateUser(_ context.Context, u *domain.User) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.users[u.ID]; !ok {
		return fmt.Errorf("%w: user %q", domain.ErrNotFound, u.ID)
	}
	cp := *u
	f.users[u.ID] = &cp
	f.username[u.Username] = u.ID
	return nil
}

func (f *fakeRepository) DeleteUser(_ context.Context, id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if u, ok := f.users[id]; ok {
		delete(f.username, u.Username)
	}
	delete(f.users, id)
	return nil
}

func (f *fakeRepository) GetRole(_ context.Context, name string) (*domain.Role, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if r, ok := f.roles[name]; ok {
		cp := *r
		return &cp, nil
	}
	for _, r := range domain.DefaultRoles {
		if r.Name == name {
			cp := r
			return &cp, nil
		}
	}
	return nil, fmt.Errorf("%w: role %q", domain.ErrNotFound, name)
}

func (f *fakeRepository) ListRoles(_ context.Context) ([]domain.Role, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]domain.Role, 0, len(domain.DefaultRoles)+len(f.roles))
	out = append(out, domain.DefaultRoles...)
	for _, r := range f.roles {
		out = append(out, *r)
	}
	return out, nil
}

func (f *fakeRepository) CreateRole(_ context.Context, r domain.Role) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, def := range domain.DefaultRoles {
		if def.Name == r.Name {
			return fmt.Errorf("%w: role %q", domain.ErrAlreadyExists, r.Name)
		}
	}
	cp := r
	f.roles[r.Name] = &cp
	return nil
}

func (f *fakeRepository) CreateRoleMapping(_ context.Context, m *domain.RoleMapping) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, e := range f.rmappings {
		if e.Type == m.Type && e.Subject == m.Subject && e.Role == m.Role && e.ChannelScope == m.ChannelScope {
			return nil
		}
	}
	cp := *m
	if cp.ID == "" {
		cp.ID = fmt.Sprintf("rm-%d", len(f.rmappings)+1)
	}
	f.rmappings[cp.ID] = &cp
	return nil
}

func (f *fakeRepository) ListRoleMappings(_ context.Context) ([]domain.RoleMapping, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]domain.RoleMapping, 0, len(f.rmappings))
	for _, m := range f.rmappings {
		out = append(out, *m)
	}
	return out, nil
}

func (f *fakeRepository) DeleteRoleMapping(_ context.Context, id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.rmappings, id)
	return nil
}

func (f *fakeRepository) GetUserRoleMappings(_ context.Context, userID string) ([]domain.RoleMapping, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []domain.RoleMapping
	for _, m := range f.rmappings {
		if m.Type == "user" && m.Subject == userID {
			out = append(out, *m)
		}
	}
	return out, nil
}

func (f *fakeRepository) GetGroupRoleMappings(_ context.Context, groupName string) ([]domain.RoleMapping, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []domain.RoleMapping
	for _, m := range f.rmappings {
		if m.Type == "group" && m.Subject == groupName {
			out = append(out, *m)
		}
	}
	return out, nil
}

func (f *fakeRepository) GetUserChannelRoleMappings(_ context.Context, userID string) ([]domain.ChannelRoleMapping, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []domain.ChannelRoleMapping
	for _, entries := range f.acls {
		for _, e := range entries {
			if e.Type == "user" && e.Subject == userID {
				out = append(out, *e)
			}
		}
	}
	return out, nil
}

func (f *fakeRepository) GetGroupChannelRoleMappings(_ context.Context, groupName string) ([]domain.ChannelRoleMapping, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []domain.ChannelRoleMapping
	for _, entries := range f.acls {
		for _, e := range entries {
			if e.Type == "group" && e.Subject == groupName {
				out = append(out, *e)
			}
		}
	}
	return out, nil
}

func (f *fakeRepository) GetGlobalConfig(_ context.Context) (*domain.GlobalConfig, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	cp := *f.config
	return &cp, nil
}

func (f *fakeRepository) UpdateGlobalConfig(_ context.Context, cfg *domain.GlobalConfig) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	cp := *cfg
	f.config = &cp
	return nil
}

func (f *fakeRepository) OIDCProviders(_ context.Context) ([]domain.OIDCProvider, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]domain.OIDCProvider, len(f.providers))
	copy(out, f.providers)
	return out, nil
}

func (f *fakeRepository) SetOIDCProviders(_ context.Context, providers []domain.OIDCProvider) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.providers = append([]domain.OIDCProvider(nil), providers...)
	return nil
}

func (f *fakeRepository) GetClientCursor(_ context.Context, channel, clientID string) (*domain.ClientCursor, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	c, ok := f.cursors[channel+"/"+clientID]
	if !ok {
		return nil, fmt.Errorf("%w: client cursor %s/%s", domain.ErrNotFound, channel, clientID)
	}
	cp := *c
	return &cp, nil
}

func (f *fakeRepository) SetClientCursor(_ context.Context, cursor *domain.ClientCursor) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	cp := *cursor
	f.cursors[cursor.Channel+"/"+cursor.ClientID] = &cp
	return nil
}

func (f *fakeRepository) GetSetupModeEndTime(_ context.Context) time.Time {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.setupEnd
}

func (f *fakeRepository) SetSetupModeEndTime(_ context.Context, t time.Time) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.setupEnd = t
	return nil
}
