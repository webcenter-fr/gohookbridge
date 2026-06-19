package main

import (
	"log"
	"os"

	gohookbridge "github.com/webcenter-fr/gohookbridge/gohookbridge"
	"github.com/webcenter-fr/gohookbridge/gohookbridge/proxy"
)

func main() {
	app := gohookbridge.MakeApp(
		proxy.Command(),
		proxy.ProduceCommand(),
		gohookbridge.KeygenCommand(),
	)
	app.Commands = append(app.Commands, gohookbridge.CompletionCommands()...)
	if err := app.Run(os.Args); err != nil {
		log.Fatal(err)
	}
}
