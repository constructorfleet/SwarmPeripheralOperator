// Package version holds build metadata set through -ldflags.
package version

import "fmt"

// Set by -ldflags "-X .../internal/version.Version=...".
var (
	Version = "dev"
	Commit  = "unknown"
)

// String formats build metadata for --version output.
func String(binary string) string {
	return fmt.Sprintf("%s %s (%s)", binary, Version, Commit)
}
