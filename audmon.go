package main

import (
	"io"
	"log"

	"github.com/MichaelDarr/audmon/cmd"
)

func main() {
	// Set up logging (currently just thrown away)
	log.SetOutput(io.Discard)

	cmd.Execute()
}
