package version

import "strings"

// Build is injected at build time via:
//
//	go build -ldflags "-X 'github.com/trymimicode/mimicode-go/internal/version.Build=$(git describe --tags --long --always)'"
//
// git describe --long always produces "v0.5-87-gd30ddf9" (tag + commits since tag + hash).
// String() formats that as "v0.5.87".
var Build = "dev"

// String formats Build as "tag.N" (latest tag + commits since that tag).
// "v0.5-87-gd30ddf9" → "v0.5.87", "v0.5-0-gabcdef" → "v0.5.0".
// Falls back to the raw Build value if the format is not recognised.
func String() string {
	// git describe --long: "v0.5-87-gd30ddf9"  →  ["v0.5", "87", "gd30ddf9"]
	parts := strings.SplitN(Build, "-", 3)
	if len(parts) >= 2 {
		return parts[0] + "." + parts[1]
	}
	return Build
}
