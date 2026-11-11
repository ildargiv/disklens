// Package buildinfo holds the product identity and version stamp. Version is
// overridden at build time:
// go build -ldflags "-X disklens/internal/buildinfo.Version=0.2.0"
package buildinfo

// Version is the release version; source builds without ldflags report -dev.
var Version = "0.2.0-dev"

const (
	// Product is the display name of the application.
	Product = "DiskLens"
	// Vendor is the publisher shown next to the product name.
	Vendor = "KopiTehnik"
	// FullName is used in banners, the TUI header and the installer.
	FullName = Product + " by " + Vendor
)
