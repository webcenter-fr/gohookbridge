package pipeline

import (
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
	"gotest.tools/v3/assert"
)

func TestTrimVersionTag(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"v-prefixed", "v1.2.3", "1.2.3"},
		{"plain", "1.2.3", "1.2.3"},
		{"whitespace", "  dev ", "dev"},
		{"empty", "", ""},
		{"only v", "v", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, TrimVersionTag(tt.input))
		})
	}
}

func TestResolveVersion(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"v-prefixed", "v1.2.3", "1.2.3"},
		{"empty falls back to dev", "", "dev"},
		{"only v falls back to dev", "v", "dev"},
		{"dev stays dev", "dev", "dev"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, ResolveVersion(tt.input))
		})
	}
}

func TestRenderHelmValues(t *testing.T) {
	rendered, err := RenderHelmValues("registry:5000/gohookbridge", "1.2.3", "dagger-smoke")
	assert.NilError(t, err)

	var values map[string]any
	assert.NilError(t, yaml.Unmarshal([]byte(rendered), &values))

	server := nestedMap(t, values, "server")
	assert.Equal(t, 1, nested(t, server, "replicas"))
	assert.Equal(t, "registry:5000/gohookbridge", nested(t, nestedMap(t, server, "image"), "repository"))
	assert.Equal(t, "1.2.3", nested(t, nestedMap(t, server, "image"), "tag"))

	raftTLS := nestedMap(t, nestedMap(t, server, "raft"), "tls")
	assert.Equal(t, false, nested(t, raftTLS, "enabled"))

	bootstrap := nestedMap(t, server, "bootstrap")
	assert.Equal(t, true, nested(t, bootstrap, "enabled"))
	config := nestedMap(t, bootstrap, "config")

	users, ok := nested(t, config, "users").([]any)
	assert.Assert(t, ok, "users must be a list, got %T", nested(t, config, "users"))
	assert.Equal(t, 1, len(users))
	admin, ok := users[0].(map[string]any)
	assert.Assert(t, ok, "user must be a map, got %T", users[0])
	assert.Equal(t, "admin", nested(t, admin, "username"))
	assert.Assert(t, nested(t, admin, "password") != "")

	channels, ok := nested(t, config, "channels").([]any)
	assert.Assert(t, ok, "channels must be a list, got %T", nested(t, config, "channels"))
	assert.Equal(t, 1, len(channels))
	channel, ok := channels[0].(map[string]any)
	assert.Assert(t, ok, "channel must be a map, got %T", channels[0])
	assert.Equal(t, "dagger-smoke", nested(t, channel, "id"))

	assert.Assert(t, strings.Contains(rendered, "fullnameOverride: gohookbridge"))
}

func TestBuildReport(t *testing.T) {
	tests := []struct {
		name     string
		sections []ReportSection
		want     string
	}{
		{
			name:     "no sections yields only the title",
			sections: nil,
			want:     "# Gohookbridge Dagger validation report\n",
		},
		{
			name: "sections render in order with titles and bodies",
			sections: []ReportSection{
				{Title: "Inputs", Body: "- Version: 1.2.3"},
				{Title: "Result", Body: "- Result: **PASSED**\n\nvalidation passed"},
			},
			want: "# Gohookbridge Dagger validation report\n" +
				"\n## Inputs\n\n- Version: 1.2.3\n" +
				"\n## Result\n\n- Result: **PASSED**\n\nvalidation passed\n",
		},
		{
			name:     "body whitespace is trimmed",
			sections: []ReportSection{{Title: "T", Body: "  body  "}},
			want:     "# Gohookbridge Dagger validation report\n\n## T\n\nbody\n",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := BuildReport(tt.sections...)
			assert.Equal(t, tt.want, got)
			// The report renders only titles and bodies: no credential
			// material can ever appear unless a caller puts it in a section.
			assert.Assert(t, !strings.Contains(got, "registry-password"))
			assert.Assert(t, !strings.Contains(got, "DaggerSmoke"))
		})
	}
}

func TestSmokeScriptConstant(t *testing.T) {
	tests := []struct {
		name string
		want string
	}{
		{"version json check", `"version\":\"$EXPECTED_VERSION\"`},
		{"version header check", "X-Gohookbridge-Version"},
		{"health check", "$BASE_URL/health"},
		{"ui index check", "index.html not served"},
		{"webhook post returns 202", "POST /$CHANNEL_ID"},
		{"sse connected message", `{"message":"connected"}`},
		{"sse bodyB round-trip", "bodyB"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Assert(t, strings.Contains(SmokeScript, tt.want),
				"smoke script must contain %q", tt.want)
		})
	}
}

// nested returns the value at key in m, failing the test when it is absent.
func nested(t *testing.T, m map[string]any, key string) any {
	t.Helper()
	value, ok := m[key]
	assert.Assert(t, ok, "missing key %q in %#v", key, m)
	return value
}

// nestedMap returns the nested map at key in m, failing the test when the
// value is missing or not a map.
func nestedMap(t *testing.T, m map[string]any, key string) map[string]any {
	t.Helper()
	value, ok := nested(t, m, key).(map[string]any)
	assert.Assert(t, ok, "key %q must be a map, got %T", key, m[key])
	return value
}
