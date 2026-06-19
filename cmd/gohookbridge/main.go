package main

import (
	"log"
	"os"

	gohookbridge "github.com/webcenter-fr/gohookbridge/gohookbridge"
	"github.com/webcenter-fr/gohookbridge/gohookbridge/client"
	"github.com/webcenter-fr/gohookbridge/gohookbridge/proxy"
	"github.com/webcenter-fr/gohookbridge/gohookbridge/server"
)

func main() {
	app := gohookbridge.MakeApp(
		server.Command(),
		client.Command(),
		client.ReplayCommand(),
		proxy.Command(),
		proxy.ProduceCommand(),
		gohookbridge.KeygenCommand(),
	)
	app.Commands = append(app.Commands, gohookbridge.CompletionCommands()...)
	if err := app.Run(os.Args); err != nil {
		log.Fatal(err)
	}
}
