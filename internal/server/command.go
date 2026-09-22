package server

import (
	"context"
	"os"

	"github.com/mattn/go-isatty"
	"github.com/mgutz/ansi"
	"github.com/urfave/cli/v2"
	"github.com/webcenter-fr/gohookbridge/internal/app"
)

func Command() *cli.Command {
	return &cli.Command{
		Name:  "server",
		Usage: "Make gohookbridge a relay server from your external webhook",
		Action: func(c *cli.Context) error {
			if !isatty.IsTerminal(os.Stdout.Fd()) {
				ansi.DisableColors(true)
			}
			s, err := NewServer(c)
			if err != nil {
				return err
			}
			return s.Run(context.Background())
		},
		Flags: app.ServerFlags,
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
