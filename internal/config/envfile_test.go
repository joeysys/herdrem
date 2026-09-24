package config

import (
	"os"
	"path/filepath"
	"testing"
)

func writeEnvFile(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "herdrem.env")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func unsetForTest(t *testing.T, key string) {
	t.Helper()
	os.Unsetenv(key)
	t.Cleanup(func() { os.Unsetenv(key) })
}

func TestLoadEnvFile_SetsUnsetVars(t *testing.T) {
	unsetForTest(t, "HERDREM_TEST_A")
	unsetForTest(t, "HERDREM_TEST_B")
	path := writeEnvFile(t, "# comment\n\nHERDREM_TEST_A=1\nexport HERDREM_TEST_B=two\n")

	got, err := LoadEnvFile(path, true)
	if err != nil || got != path {
		t.Fatalf("LoadEnvFile = (%q, %v), want (%q, nil)", got, err, path)
	}
	if os.Getenv("HERDREM_TEST_A") != "1" || os.Getenv("HERDREM_TEST_B") != "two" {
		t.Fatalf("env = %q/%q", os.Getenv("HERDREM_TEST_A"), os.Getenv("HERDREM_TEST_B"))
	}
}

func TestLoadEnvFile_DoesNotOverrideProcessEnv(t *testing.T) {
	t.Setenv("HERDREM_TEST_C", "from-env")
	path := writeEnvFile(t, "HERDREM_TEST_C=from-file\n")

	if _, err := LoadEnvFile(path, true); err != nil {
		t.Fatal(err)
	}
	if got := os.Getenv("HERDREM_TEST_C"); got != "from-env" {
		t.Fatalf("process env was overridden: %q", got)
	}
}

func TestLoadEnvFile_QuotedValues(t *testing.T) {
	unsetForTest(t, "HERDREM_TEST_DQ")
	unsetForTest(t, "HERDREM_TEST_SQ")
	unsetForTest(t, "HERDREM_TEST_BS")
	path := writeEnvFile(t,
		`HERDREM_TEST_DQ="with spaces and \"quoted\" part"`+"\n"+
			`HERDREM_TEST_SQ='literal'`+"\n"+
			`HERDREM_TEST_BS="C:\bin\herdrem"`+"\n")

	if _, err := LoadEnvFile(path, true); err != nil {
		t.Fatal(err)
	}
	if got := os.Getenv("HERDREM_TEST_DQ"); got != `with spaces and "quoted" part` {
		t.Errorf("double-quoted = %q", got)
	}
	if got := os.Getenv("HERDREM_TEST_SQ"); got != "literal" {
		t.Errorf("single-quoted = %q", got)
	}
	// Unknown escapes stay literal — only \" and \\ are unescaped.
	if got := os.Getenv("HERDREM_TEST_BS"); got != `C:\bin\herdrem` {
		t.Errorf("backslash = %q", got)
	}
}

func TestLoadEnvFile_Missing(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "nope.env")

	if got, err := LoadEnvFile(missing, false); err != nil || got != "" {
		t.Fatalf("non-explicit missing file = (%q, %v), want (\"\", nil)", got, err)
	}
	if _, err := LoadEnvFile(missing, true); err == nil {
		t.Fatal("explicit missing file should error")
	}
}

func TestLoadEnvFile_EmptyPathUsesDefault(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HERDREM_DATA_DIR", dir)
	unsetForTest(t, "HERDREM_TEST_DEF")

	// no file at the default path → skipped, not an error
	if got, err := LoadEnvFile("", false); err != nil || got != "" {
		t.Fatalf("default-path skip = (%q, %v), want (\"\", nil)", got, err)
	}

	path := filepath.Join(dir, "herdrem.env")
	if err := os.WriteFile(path, []byte("HERDREM_TEST_DEF=yes\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got, err := LoadEnvFile("", false); err != nil || got != path {
		t.Fatalf("default-path load = (%q, %v), want (%q, nil)", got, err, path)
	}
	if os.Getenv("HERDREM_TEST_DEF") != "yes" {
		t.Fatal("default-path value not loaded")
	}
}
