// Package install owns offline, per-user Linux installation and removal.
package install

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"runtime"
)

// Run handles installation commands before application configuration, network or
// window initialization. A false result leaves ordinary application flags alone.
func Run(args []string, stdout, stderr io.Writer) (handled bool, status int) {
	if len(args) == 0 || args[0] != "install" && args[0] != "uninstall" {
		return false, 0
	}
	command := args[0]
	flags := flag.NewFlagSet(command, flag.ContinueOnError)
	flags.SetOutput(stderr)
	flags.Usage = func() {
		_, _ = fmt.Fprintf(flags.Output(), "Usage: pepe %s\nOffline per-user Linux %s; existing configuration is preserved.\n", command, command)
	}
	if err := flags.Parse(args[1:]); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return true, 0
		}
		return true, 2
	}
	if flags.NArg() != 0 {
		flags.Usage()
		return true, 2
	}
	if runtime.GOOS != "linux" {
		_, _ = fmt.Fprintln(stderr, "Pépé installation currently supports Linux only.")
		return true, 1
	}
	p, err := environmentPaths()
	if err == nil {
		i := installer{paths: p, out: stdout}
		if command == "uninstall" {
			err = i.uninstall()
		} else {
			var executable string
			executable, err = os.Executable()
			if err == nil {
				err = i.install(executable)
			}
		}
	}
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "Pépé %s failed: %v\n", command, err)
		return true, 1
	}
	return true, 0
}
