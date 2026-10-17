// Package version holds build metadata injected with -ldflags.
package version

// Set at build time:
//
//	go build -ldflags "-X gonghao/internal/version.Commit=$(git rev-parse --short HEAD) -X gonghao/internal/version.BuiltAt=..."
var (
	Commit  = "dev"
	BuiltAt = ""
)

// Info is what /api/version returns.
type Info struct {
	Commit  string `json:"commit"`
	BuiltAt string `json:"built_at,omitempty"`
	Schema  int    `json:"schema"`
}
