package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadEnvFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.env")
	if err := os.WriteFile(path, []byte("# comment\nTELLER_APPLICATION_ID=app_test\nexport TELLER_ENV=development\nTELLER_CERT_PATH=\"~/cert.pem\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	env, err := LoadEnvFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := env.Get("TELLER_APPLICATION_ID"); got != "app_test" {
		t.Fatalf("TELLER_APPLICATION_ID = %q", got)
	}
	if got := env.Get("TELLER_ENV"); got != "development" {
		t.Fatalf("TELLER_ENV = %q", got)
	}
	if got := env.Get("TELLER_CERT_PATH"); got != "~/cert.pem" {
		t.Fatalf("TELLER_CERT_PATH = %q", got)
	}
}

func TestResolvePathsDefault(t *testing.T) {
	paths, err := ResolvePaths("")
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(paths.DataDir) != ".lasso" {
		t.Fatalf("DataDir = %q", paths.DataDir)
	}
	if filepath.Base(paths.ConfigFile) != "config.env" {
		t.Fatalf("ConfigFile = %q", paths.ConfigFile)
	}
	if filepath.Base(paths.EnrollmentFile) != "enrollment.json" {
		t.Fatalf("EnrollmentFile = %q", paths.EnrollmentFile)
	}
}
