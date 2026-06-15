package gohookbridge

import (
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/webcenter-fr/gohookbridge/gohookbridge/store"
	"github.com/urfave/cli/v2"
	"gopkg.in/yaml.v3"
)

func migrateConfig(c *cli.Context) error {
	global := store.GlobalConfig{}
	hasConfig := false

	if v := os.Getenv("GOSMEE_MAX_BODY_SIZE"); v != "" {
		size, err := strconv.Atoi(v)
		if err != nil {
			return fmt.Errorf("GOSMEE_MAX_BODY_SIZE: invalid integer %q", v)
		}
		global.Server.MaxBodySize = size
		hasConfig = true
	}

	if v := os.Getenv("GOSMEE_CORS_ORIGIN"); v != "" {
		global.Server.CORSOrigin = v
		hasConfig = true
	}

	if v := os.Getenv("GOSMEE_TRUST_PROXY"); v != "" {
		global.Server.BehindReverseProxy = v == "true" || v == "1"
		hasConfig = true
	}

	if v := os.Getenv("GOSMEE_FOOTER"); v != "" {
		global.Server.Footer = v
		hasConfig = true
	}

	if v := os.Getenv("GOSMEE_AUTH_SESSION_SECRET"); v != "" {
		global.Server.SessionSecret = v
		hasConfig = true
	}

	if v := os.Getenv("GOSMEE_WEBHOOK_SIGNATURE"); v != "" {
		global.Defaults.WebhookSecret = v
		hasConfig = true
	}

	if v := os.Getenv("GOSMEE_ALLOWED_IPS"); v != "" {
		global.Defaults.AllowedIPs = strings.Split(v, ",")
		hasConfig = true
	}

	if !hasConfig {
		fmt.Fprintln(os.Stderr, "No deprecated environment variables found. Nothing to migrate.")
		return nil
	}

	bootstrap := store.BootstrapConfig{
		Global: &global,
	}

	out, err := yaml.Marshal(&bootstrap)
	if err != nil {
		return fmt.Errorf("marshal bootstrap config: %w", err)
	}

	fmt.Fprintln(os.Stdout, string(out))
	fmt.Fprintln(os.Stderr, "# Above is your migrated bootstrap.yaml configuration.")
	fmt.Fprintln(os.Stderr, "# Save it to a file and pass --bootstrap-config-file=path/to/bootstrap.yaml to gohookbridge server.")
	return nil
}