// Package version exposes the build version of the xpc binary.
//
// Version is intended to be overridden at build time via -ldflags
// "-X github.com/nficano/xpc/internal/version.Version=<value>".
package version

// Version is the human-readable version string. Default is suitable for
// development builds; release builds override it via -ldflags.
var Version = "0.0.0-dev"

// String returns the version string.
func String() string {
	return Version
}
