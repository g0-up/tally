// Command tally is the single binary for the Tally API, worker and operator CLI.
package main

import (
	"os"

	"github.com/g0-up/tally/apps/api/internal/cli"
)

// version is stamped at build time with -ldflags "-X main.version=<value>".
var version = "dev"

func main() {
	os.Exit(cli.Execute(version))
}
