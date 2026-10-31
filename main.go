package main

import (
	"os"

	"github.com/anjay-goel/gofer/internal/cmd"
)

var version = "dev"

func main() {
	os.Exit(cmd.Main(version, os.Args[1:]))
}
