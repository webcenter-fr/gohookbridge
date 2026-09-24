package main

import (
	"log"
	"os"

	"github.com/webcenter-fr/gohookbridge/internal/app"
	"github.com/webcenter-fr/gohookbridge/internal/client"
	"github.com/webcenter-fr/gohookbridge/internal/proxy"
	"github.com/webcenter-fr/gohookbridge/internal/server"
)

func main() {
	cliApp := app.MakeApp(
		server.Command(),
		client.Command(),
		client.ReplayCommand(),
		proxy.Command(),
		proxy.ProduceCommand(),
		app.KeygenCommand(),
	)
	cliApp.Commands = append(cliApp.Commands, app.CompletionCommands()...)
	if err := cliApp.Run(os.Args); err != nil {
		log.Fatal(err)
	}
}
