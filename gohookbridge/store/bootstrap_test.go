package store

import (
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/crypto/bcrypt"
	"gotest.tools/v3/assert"
)

func TestLoadBootstrap_YAML(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bootstrap.yaml")
	content := `global:
  server:
    maxbodysize: 100
    trustproxy: true
users:
  - username: alice
    password: secret123
    roles: [admin]
    projects: ["*"]
projects:
  - id: proj1
    name: Project One
`
	err := os.WriteFile(path, []byte(content), 0644)
	assert.NilError(t, err)

	cfg, err := LoadBootstrap(path)
	assert.NilError(t, err)

	assert.Assert(t, cfg.Global != nil)
	assert.Equal(t, cfg.Global.Server.MaxBodySize, 100)
	assert.Assert(t, cfg.Global.Server.TrustProxy)

	assert.Equal(t, len(cfg.Users), 1)
	assert.Equal(t, cfg.Users[0].Username, "alice")
	assert.Equal(t, cfg.Users[0].Password, "secret123")
	assert.DeepEqual(t, cfg.Users[0].Roles, []string{"admin"})
	assert.DeepEqual(t, cfg.Users[0].Projects, []string{"*"})

	assert.Equal(t, len(cfg.Projects), 1)
	assert.Equal(t, cfg.Projects[0].ID, "proj1")
	assert.Equal(t, cfg.Projects[0].Name, "Project One")
}

func TestLoadBootstrap_JSON(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bootstrap.json")
	content := `{
		"global": {
			"server": {
				"maxbodysize": 200,
				"corsorigin": "https://example.com"
			}
		},
		"users": [
			{"username": "bob", "password": "pass", "roles": ["project_viewer"], "projects": ["proj1"]}
		],
		"projects": [
			{"id": "proj1", "name": "Project One"}
		]
	}`
	err := os.WriteFile(path, []byte(content), 0644)
	assert.NilError(t, err)

	cfg, err := LoadBootstrap(path)
	assert.NilError(t, err)

	assert.Assert(t, cfg.Global != nil)
	assert.Equal(t, cfg.Global.Server.MaxBodySize, 200)
	assert.Equal(t, cfg.Global.Server.CORSOrigin, "https://example.com")

	assert.Equal(t, len(cfg.Users), 1)
	assert.Equal(t, cfg.Users[0].Username, "bob")

	assert.Equal(t, len(cfg.Projects), 1)
	assert.Equal(t, cfg.Projects[0].ID, "proj1")
}

func TestLoadBootstrap_InvalidYAML(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bad.yaml")
	content := `{{{{{invalid yaml`
	err := os.WriteFile(path, []byte(content), 0644)
	assert.NilError(t, err)

	_, err = LoadBootstrap(path)
	assert.ErrorContains(t, err, "parse bootstrap")
}

func TestLoadBootstrap_FileNotFound(t *testing.T) {
	_, err := LoadBootstrap("/nonexistent/path/bootstrap.yaml")
	assert.ErrorContains(t, err, "read bootstrap file")
}

func TestApplyBootstrap_GlobalConfig(t *testing.T) {
	rs := newTestRaftStore(t)

	cfg := &BootstrapConfig{
		Global: &GlobalConfig{
			Server: ServerConfig{
				MaxBodySize: 500,
				CORSOrigin:  "https://app.example.com",
				TrustProxy:  true,
			},
		},
	}

	err := rs.ApplyBootstrap(cfg)
	assert.NilError(t, err)

	got, err := rs.GetGlobalConfig()
	assert.NilError(t, err)
	assert.Equal(t, got.Server.MaxBodySize, 500)
	assert.Equal(t, got.Server.CORSOrigin, "https://app.example.com")
	assert.Assert(t, got.Server.TrustProxy)
}

func TestApplyBootstrap_Users_PasswordHashed(t *testing.T) {
	rs := newTestRaftStore(t)

	cfg := &BootstrapConfig{
		Users: []BootstrapUser{
			{
				Username: "alice",
				Password: "my-secret-password",
				Roles:    []string{"admin"},
				Projects: []string{"*"},
			},
		},
	}

	err := rs.ApplyBootstrap(cfg)
	assert.NilError(t, err)

	user, err := rs.GetUserByUsername("alice")
	assert.NilError(t, err)
	assert.Equal(t, user.Username, "alice")
	assert.Assert(t, user.PasswordHash != "")
	assert.Assert(t, user.PasswordHash != "my-secret-password")

	err = bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte("my-secret-password"))
	assert.NilError(t, err)

	assert.DeepEqual(t, user.Roles, []string{"admin"})
	assert.DeepEqual(t, user.Projects, []string{"*"})
}

func TestApplyBootstrap_Projects(t *testing.T) {
	rs := newTestRaftStore(t)

	cfg := &BootstrapConfig{
		Projects: []BootstrapProject{
			{ID: "proj-a", Name: "Project A"},
			{ID: "proj-b", Name: "Project B"},
		},
	}

	err := rs.ApplyBootstrap(cfg)
	assert.NilError(t, err)

	projects, err := rs.ListProjects()
	assert.NilError(t, err)
	assert.Equal(t, len(projects), 2)

	gotA, err := rs.GetProject("proj-a")
	assert.NilError(t, err)
	assert.Equal(t, gotA.Name, "Project A")

	gotB, err := rs.GetProject("proj-b")
	assert.NilError(t, err)
	assert.Equal(t, gotB.Name, "Project B")
}

func TestApplyBootstrap_DoubleApplication(t *testing.T) {
	rs := newTestRaftStore(t)

	cfg := &BootstrapConfig{
		Projects: []BootstrapProject{
			{ID: "proj1", Name: "One"},
		},
	}

	err := rs.ApplyBootstrap(cfg)
	assert.NilError(t, err)

	err = rs.ApplyBootstrap(&BootstrapConfig{})
	assert.ErrorContains(t, err, "FSM already has data")
}