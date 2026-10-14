// Package version holds build metadata injected via -ldflags.
package version

var (
	// Version is the release tag, e.g. v0.1.0.
	Version = "dev"
	// Commit is the git commit the binary was built from.
	Commit = "none"
	// Date is the build time in RFC 3339.
	Date = "unknown"
)
