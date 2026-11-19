// Package ociargs parses the runc command line used by containerd shims.
package ociargs

import "strings"

// Invocation is the subset of a runc command line SPO acts on.
type Invocation struct {
	Command     string
	Bundle      string
	ContainerID string
	LogPath     string
	LogFormat   string
}

// Global and subcommand flags that take a value. Unknown flags are treated as boolean.
var valueFlags = map[string]bool{
	"root": true, "log": true, "log-format": true, "criu": true, "rootless": true,
	"bundle": true, "b": true, "pid-file": true, "console-socket": true, "preserve-fds": true,
}

// Parse extracts the subcommand, bundle, container ID, and log settings.
func Parse(args []string) Invocation {
	var inv Invocation
	var positional []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		if !strings.HasPrefix(a, "-") {
			if inv.Command == "" {
				inv.Command = a
			} else {
				positional = append(positional, a)
			}
			continue
		}
		name, value, hasValue := strings.Cut(strings.TrimLeft(a, "-"), "=")
		if !hasValue && valueFlags[name] && i+1 < len(args) {
			i++
			value = args[i]
		}
		switch name {
		case "bundle", "b":
			inv.Bundle = value
		case "log":
			inv.LogPath = value
		case "log-format":
			inv.LogFormat = value
		}
	}
	if len(positional) > 0 {
		inv.ContainerID = positional[len(positional)-1]
	}
	return inv
}
