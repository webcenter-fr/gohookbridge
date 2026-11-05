package app

import (
	"strings"
	"testing"

	"github.com/urfave/cli/v2"
)

// TestEnvVarPrefixes guards the gosmee→gohookbridge env-var rename: no flag may
// expose a GOSMEE_* env var, and every env var must be NO_COLOR or GOHOOKBRIDGE_*.
func TestEnvVarPrefixes(t *testing.T) {
	all := [][]cli.Flag{
		CommonFlags, ReplayFlags, KeygenFlags, ClientFlags,
		ProduceFlags, ProxyFlags, ServerFlags,
	}

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
	case *cli.Int64Flag:
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

// TestF11ResourceFlagsRegistered guards the F11 resource-budget flags: they
// must exist on the server command with bounded defaults so the SSE queue,
// connection cap, and NATS buffering limits are always wired.
func TestF11ResourceFlagsRegistered(t *testing.T) {
	byName := make(map[string]cli.Flag)
	for _, fl := range ServerFlags {
		for _, n := range fl.Names() {
			byName[n] = fl
		}
	}

	intFlags := map[string]int64{
		"sse-queue-depth":     100,
		"sse-queue-bytes":     4194304,
		"max-sse-connections": 1000,
		"nats-buffer-bytes":   67108864,
		"nats-max-payload":    1048576,
	}
	for name, def := range intFlags {
		fl, ok := byName[name]
		if !ok {
			t.Fatalf("flag %q not registered on the server command", name)
		}
		intFl, ok := fl.(*cli.IntFlag)
		if !ok {
			t.Fatalf("flag %q is %T, want *cli.IntFlag", name, fl)
		}
		if int64(intFl.Value) != def {
			t.Fatalf("flag %q default = %d, want %d", name, intFl.Value, def)
		}
	}

	fl, ok := byName["nats-max-pending"]
	if !ok {
		t.Fatal("flag \"nats-max-pending\" not registered on the server command")
	}
	int64Fl, ok := fl.(*cli.Int64Flag)
	if !ok {
		t.Fatalf("flag %q is %T, want *cli.Int64Flag", "nats-max-pending", fl)
	}
	if int64Fl.Value != 67108864 {
		t.Fatalf("flag %q default = %d, want %d", "nats-max-pending", int64Fl.Value, 67108864)
	}
}
