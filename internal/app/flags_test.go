package app

import (
	"strings"
	"testing"

	"github.com/urfave/cli/v2"
)

// TestEnvVarPrefixes guards the gosmee→gohookbridge env-var rename: no flag may
// expose a GOSMEE_* env var, and every env var must be NO_COLOR or GOHOOKBRIDGE_*.
func TestEnvVarPrefixes(t *testing.T) {
	all := [][]cli.Flag{CommonFlags, ReplayFlags, KeygenFlags, ClientFlags,
		ProduceFlags, ProxyFlags, ServerFlags}

	seen := 0
	for _, group := range all {
		for _, fl := range group {
			envs := flagEnvVars(fl)
			for _, e := range envs {
				seen++
				if strings.Contains(strings.ToUpper(e), "GOSMEE") {
					t.Fatalf("flag %q exposes legacy GOSMEE env var %q", flagName(fl), e)
				}
				if e != "NO_COLOR" && !strings.HasPrefix(e, "GOHOOKBRIDGE_") {
					t.Fatalf("flag %q has non-conforming env var %q", flagName(fl), e)
				}
			}
		}
	}
	if seen == 0 {
		t.Fatal("expected at least one env var across all flag groups")
	}
}

func flagEnvVars(fl cli.Flag) []string {
	switch f := fl.(type) {
	case *cli.StringFlag:
		return f.EnvVars
	case *cli.IntFlag:
		return f.EnvVars
	case *cli.BoolFlag:
		return f.EnvVars
	case *cli.StringSliceFlag:
		return f.EnvVars
	case *cli.DurationFlag:
		return f.EnvVars
	case *cli.Float64Flag:
		return f.EnvVars
	default:
		return nil
	}
}

func flagName(fl cli.Flag) string {
	for _, n := range fl.Names() {
		if len(n) > 1 {
			return n
		}
	}
	return ""
}
