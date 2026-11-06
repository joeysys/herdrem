package version

import (
	"encoding/json"
	"os"
	"testing"
)

// web/package.json must match version/VERSION. Bump both sides via
// `make bump V=x.y.z`; editing only one side fails this test.
func TestMatchesWebPackage(t *testing.T) {
	data, err := os.ReadFile("../web/package.json")
	if err != nil {
		t.Fatalf("read web/package.json: %v", err)
	}
	var pkg struct {
		Version string `json:"version"`
	}
	if err := json.Unmarshal(data, &pkg); err != nil {
		t.Fatalf("parse web/package.json: %v", err)
	}
	if pkg.Version != Current {
		t.Fatalf("version drift: version/VERSION=%q web/package.json=%q (run `make bump V=%s` or fix by hand)", Current, pkg.Version, Current)
	}
}
