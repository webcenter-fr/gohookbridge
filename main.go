package main

import (
	"log"
	"os"

	gohookbridge "github.com/webcenter-fr/gohookbridge/gohookbridge"
)

func main() {
	if err := gohookbridge.Run(os.Args); err != nil {
		log.Fatal(err)
	}
}
