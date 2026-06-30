// Package version exposes build version metadata, injected via -ldflags at build time.
package version

// Set with: go build -ldflags "-X .../internal/version.Version=... -X .../internal/version.GitCommit=..."
var (
	Version   = "0.0.0-dev"
	GitCommit = "unknown"
)

// String returns a human-readable version string.
func String() string {
	return Version + " (" + GitCommit + ")"
}
