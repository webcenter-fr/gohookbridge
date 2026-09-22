package proxy

import (
	"github.com/webcenter-fr/gohookbridge/internal/app"

	"github.com/urfave/cli/v2"
)

func Command() *cli.Command {
	return &cli.Command{
		Name:      "proxy",
		UsageText: "gohookbridge proxy --pubkey <key> --listen :9090 --target <server-url>/<channel>",
		Usage:     "Start an HTTP server that encrypts incoming webhooks and forwards them to a gohookbridge channel",
		Action:    func(c *cli.Context) error { return startProxy(c) },
		Flags:     append(app.CommonFlags, app.ProxyFlags...),
	}
}

func ProduceCommand() *cli.Command {
	return &cli.Command{
		Name:      "produce",
		UsageText: "gohookbridge produce --pubkey <key> <server-url>/<channel> [payload-file]",
		Usage:     "Encrypt and send a webhook payload to a gohookbridge channel",
		Action:    func(c *cli.Context) error { return produce(c) },
		Flags:     append(app.CommonFlags, app.ProduceFlags...),
	}
}
