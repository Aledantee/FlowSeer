// Command device runs the central device service: the operator API, the
// edge-facing enrollment and dispatch surface, the durable lane journal, and
// the drift poll. It reads one prototext file describing the deployment and
// runs until it is interrupted.
//
// A configuration that cannot be read or does not satisfy its schema exits 2,
// before anything is opened or bound. A runtime failure exits 1. An interrupt
// or a termination signal is a clean stop and exits 0: the runtime drains its
// modules, and a service that reported failure every time it was asked to
// stop would make a restart loop look like a crash loop.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"

	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/services/device/internal/host"
)

// version is the build this binary reports as service.version on every signal
// it exports. The release build injects it with -ldflags '-X main.version=…'.
var version = "dev"

// defaultConfigPath is where a packaged deployment puts the file. A flag
// overrides it; there is no environment fallback, because a service reading
// its whole identity from an unnamed source is one nobody can reproduce.
const defaultConfigPath = "/etc/flowseer/device.textproto"

// exitConfig is the status a configuration failure exits with, so a wrapper
// that restarts the service can tell a file it will never accept from a
// runtime failure a retry might survive. Every other failure takes the status
// the error itself names, which [errs.ExitCode] defaults to 1.
const exitConfig = 2

func main() {
	configPath := flag.String("config", defaultConfigPath, "path to the prototext service configuration")
	flag.Parse()

	cfg, err := host.LoadConfig(*configPath)
	if err != nil {
		// Printed rather than logged: this happens before the service has a
		// logger, and an operator who mistyped a path is reading a terminal.
		// The whole chain is printed, because this surface is the operator's
		// own and the failure is about their file.
		fmt.Fprintf(os.Stderr, "device: %v\n", err)
		os.Exit(exitConfig)
	}

	if err := host.Run(context.Background(), cfg, version); err != nil {
		fmt.Fprintf(os.Stderr, "device: %v\n", err)
		os.Exit(errs.ExitCode(err))
	}
}
