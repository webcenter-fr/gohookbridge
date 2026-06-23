package server

import (
	"os"

	"github.com/mattn/go-isatty"
	"github.com/mgutz/ansi"
	"github.com/urfave/cli/v2"
	gohookbridge "github.com/webcenter-fr/gohookbridge/gohookbridge"
)

func Command() *cli.Command {
	return &cli.Command{
		Name:  "server",
		Usage: "Make gohookbridge a relay server from your external webhook",
		Action: func(c *cli.Context) error {
			if !isatty.IsTerminal(os.Stdout.Fd()) {
				ansi.DisableColors(true)
			}
			return serve(c)
		},
		Flags: gohookbridge.ServerFlags,
		Subcommands: []*cli.Command{
			{
				Name:        "migrate-config",
				Usage:       "Migrate deprecated environment variables to a bootstrap.yaml config",
				Description: `Reads deprecated environment variables (GOSMEE_WEBHOOK_SIGNATURE, GOSMEE_ALLOWED_IPS, etc.) and outputs a bootstrap.yaml configuration to stdout.`,
				Action: func(_ *cli.Context) error {
					return migrateConfig(nil)
				},
			},
		},
	}
}
