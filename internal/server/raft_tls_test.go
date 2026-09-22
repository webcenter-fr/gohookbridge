package server

import (
	"flag"
	"testing"

	"github.com/urfave/cli/v2"
	"github.com/webcenter-fr/gohookbridge/internal/app"
	"github.com/webcenter-fr/gohookbridge/internal/repository"
	"gotest.tools/v3/assert"
)

// newTestCLIContext builds a cli.Context with the server flags and their
// defaults, optionally overridden by args.
func newTestCLIContext(t *testing.T, args ...string) *cli.Context {
	t.Helper()
	cliApp := cli.NewApp()
	cliApp.Flags = app.ServerFlags
	set := flag.NewFlagSet("test", flag.ContinueOnError)
	for _, f := range cliApp.Flags {
		assert.NilError(t, f.Apply(set))
	}
	assert.NilError(t, set.Parse(args))
	return cli.NewContext(cliApp, set, nil)
}

func TestBuildRaftTLSConfig_Disabled(t *testing.T) {
	ctx := newTestCLIContext(t)
	cfg, err := buildRaftTLSConfig(ctx, repository.RaftDiscoveryConfig{}, "host")
	assert.NilError(t, err)
	assert.Assert(t, cfg == nil)
}

func TestEffectiveNamespace(t *testing.T) {
	t.Setenv("POD_NAMESPACE", "from-env")
	assert.Equal(t, effectiveNamespace(newTestCLIContext(t)), "from-env")
	assert.Equal(t, effectiveNamespace(newTestCLIContext(t, "--raft-namespace", "explicit")), "explicit")
}
