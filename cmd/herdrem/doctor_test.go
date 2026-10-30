package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakeHerdr writes an executable stub that answers ServerStatus's
// `status server --json` call with the given JSON.
func fakeHerdr(t *testing.T, json string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "herdr")
	script := "#!/bin/sh\nprintf '%s' '" + json + "'\n"
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

// doctorEnv isolates the test from the real data dir and env file: a temp
// data dir means DefaultEnvFilePath points at a nonexistent file, and
// explicit env always wins over file values anyway.
func doctorEnv(t *testing.T) {
	t.Helper()
	t.Setenv("HERDREM_DATA_DIR", t.TempDir())
	t.Setenv("HERDREM_BIND", "127.0.0.1")
	t.Setenv("HERDREM_TOKEN", "")
	t.Setenv("HERDR_REMOTES", "")
}

func TestDoctor_MissingHerdrFails(t *testing.T) {
	doctorEnv(t)
	t.Setenv("HERDR_BIN", filepath.Join(t.TempDir(), "no-such-herdr"))
	var out bytes.Buffer
	code := runDoctor(nil, &out)
	if code != 1 {
		t.Fatalf("exit = %d, want 1; out:\n%s", code, out.String())
	}
	if !strings.Contains(out.String(), "FAIL  herdr binary") {
		t.Errorf("no FAIL line for herdr binary; out:\n%s", out.String())
	}
}

func TestDoctor_NonLoopbackWithoutTokenFails(t *testing.T) {
	doctorEnv(t)
	t.Setenv("HERDR_BIN", fakeHerdr(t, `{"running":true,"version":"0.9.3","protocol":22}`))
	t.Setenv("HERDREM_BIND", "0.0.0.0")
	var out bytes.Buffer
	code := runDoctor(nil, &out)
	if code != 1 {
		t.Fatalf("exit = %d, want 1; out:\n%s", code, out.String())
	}
	if !strings.Contains(out.String(), "FAIL  relay auth") {
		t.Errorf("no FAIL line for relay auth; out:\n%s", out.String())
	}
	if strings.Contains(out.String(), "FAIL  herdr") {
		t.Errorf("herdr checks should pass with the stub; out:\n%s", out.String())
	}
}

func TestDoctor_OldHerdrFails(t *testing.T) {
	doctorEnv(t)
	t.Setenv("HERDR_BIN", fakeHerdr(t, `{"running":true,"version":"0.8.0","protocol":21}`))
	var out bytes.Buffer
	code := runDoctor(nil, &out)
	if code != 1 {
		t.Fatalf("exit = %d, want 1; out:\n%s", code, out.String())
	}
	if !strings.Contains(out.String(), "FAIL  herdr server (local)") {
		t.Errorf("no FAIL line for old herdr; out:\n%s", out.String())
	}
}

func TestDoctor_HealthySetupPasses(t *testing.T) {
	doctorEnv(t)
	t.Setenv("HERDR_BIN", fakeHerdr(t, `{"running":true,"version":"0.9.3","protocol":22}`))
	var out bytes.Buffer
	code := runDoctor(nil, &out)
	if code != 0 {
		t.Fatalf("exit = %d, want 0; out:\n%s", code, out.String())
	}
	if strings.Contains(out.String(), "FAIL") {
		t.Errorf("unexpected FAIL; out:\n%s", out.String())
	}
}
