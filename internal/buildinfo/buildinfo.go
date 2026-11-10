// Package buildinfo holds the version stamp, overridden at build time:
// go build -ldflags "-X disklens/internal/buildinfo.Version=0.1.0"
package buildinfo

// Version is the release version; source builds without ldflags report -dev.
var Version = "0.1.0-dev"
