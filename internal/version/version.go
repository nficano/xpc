// Package version exposes the build version of the xpc binary.
//
// Version is intended to be overridden at build time via -ldflags
// "-X github.com/nficano/xpc/internal/version.Version=<value>".
package version

// Version is the human-readable version string. Default is suitable for
// development builds; release builds override it via -ldflags.
var Version = "0.0.0-dev"

// Codename is the human-friendly release name (e.g. "loony-lionfish") set by
// the release workflow via -ldflags. Empty for dev builds.
var Codename = ""

// String returns the version string, with the codename appended in parens
// when set by a release build.
func String() string {
	if Codename != "" {
		return Version + " (" + Codename + ")"
	}
	return Version
}
