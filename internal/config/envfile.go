package config

import (
	"bufio"
	"errors"
	"os"
	"path/filepath"
	"strings"
)

// DefaultEnvFilePath is the env file the binary loads when --env-file is not
// given: $HERDREM_DATA_DIR/herdrem.env, or the default data dir
// (~/.config/herdrem/herdrem.env) — the same file the systemd unit injects
// via EnvironmentFile=.
func DefaultEnvFilePath() string {
	dir := os.Getenv("HERDREM_DATA_DIR")
	if dir == "" {
		dir = defaultDataDir()
	}
	return filepath.Join(dir, "herdrem.env")
}

// LoadEnvFile reads KEY=VALUE lines from path into the process environment,
// skipping variables that are already set — the same precedence as systemd's
// Environment= over EnvironmentFile=, so a unit-run relay is unaffected while
// a foreground `./herdrem serve --web` picks the file up automatically.
// Comments (#), blank lines, an optional `export ` prefix, and single- or
// double-quoted values are supported. explicit marks a user-supplied path:
// a missing file is then an error; for the default path it is skipped
// silently. Returns the resolved path actually loaded ("" when skipped).
func LoadEnvFile(path string, explicit bool) (string, error) {
	if path == "" {
		path = DefaultEnvFilePath()
	}
	f, err := os.Open(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) && !explicit {
			return "", nil
		}
		return "", err
	}
	defer f.Close()

	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		line = strings.TrimPrefix(line, "export ")
		key, val, ok := strings.Cut(line, "=")
		key = strings.TrimSpace(key)
		if !ok || key == "" {
			continue
		}
		if _, exists := os.LookupEnv(key); exists {
			continue
		}
		os.Setenv(key, unquoteEnvValue(strings.TrimSpace(val)))
	}
	return path, sc.Err()
}

// unquoteEnvValue strips surrounding quotes. Double quotes additionally
// unescape \" and \\ — the two escapes install.sh writes via systemd_quote;
// other backslashes stay literal. Single quotes are literal.
func unquoteEnvValue(v string) string {
	if len(v) < 2 {
		return v
	}
	if v[0] == '\'' && v[len(v)-1] == '\'' {
		return v[1 : len(v)-1]
	}
	if v[0] != '"' || v[len(v)-1] != '"' {
		return v
	}
	s := v[1 : len(v)-1]
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); i++ {
		if s[i] == '\\' && i+1 < len(s) && (s[i+1] == '\\' || s[i+1] == '"') {
			i++
		}
		b.WriteByte(s[i])
	}
	return b.String()
}
