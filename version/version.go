// Package version exposes the herdrem release version. The VERSION file is
// the single source of truth: it is embedded into the binary at compile
// time, and `make bump V=x.y.z` keeps web/package.json in sync.
package version

import (
	_ "embed"
	"strings"
)

//go:embed VERSION
var raw string

// Current is the herdrem release version (e.g. "0.2.0").
var Current = strings.TrimSpace(raw)
