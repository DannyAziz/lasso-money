package teller

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNormalizeConnectPayload(t *testing.T) {
	enrollment, err := NormalizeConnectPayload(map[string]any{
		"accessToken": "token_1234567890abcdef",
		"enrollment": map[string]any{
			"id": "enr_123",
			"institution": map[string]any{
				"id":   "american_express",
				"name": "American Express",
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if enrollment.ID != "enr_123" {
		t.Fatalf("ID = %q", enrollment.ID)
	}
	if enrollment.InstitutionName != "American Express" {
		t.Fatalf("InstitutionName = %q", enrollment.InstitutionName)
	}
	if enrollment.AccessToken != "token_1234567890abcdef" {
		t.Fatalf("AccessToken = %q", enrollment.AccessToken)
	}
}

func TestSaveLoadEnrollmentPermissions(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "enrollment.json")
	want := Enrollment{ID: "enr_1", InstitutionName: "Test Bank", AccessToken: "token_secret", Provider: "teller"}
	if err := SaveEnrollment(path, want); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("mode = %04o", got)
	}
	got, err := LoadEnrollment(path)
	if err != nil {
		t.Fatal(err)
	}
	if got.AccessToken != want.AccessToken || got.ID != want.ID {
		t.Fatalf("loaded %#v", got)
	}
}

func TestMaskToken(t *testing.T) {
	cases := map[string]string{
		"token_1234567890": "token_…7890",
		"short":            "sh…",
		"ab":               "…",
		"a":                "…",
		"":                 "",
	}
	for token, want := range cases {
		if got := MaskToken(token); got != want {
			t.Fatalf("MaskToken(%q) = %q, want %q", token, got, want)
		}
	}
}

func TestNormalizeConnectPayloadFallbackIDIsNotTokenDerived(t *testing.T) {
	token := "token_1234567890abcdef"
	enrollment, err := NormalizeConnectPayload(map[string]any{"accessToken": token})
	if err != nil {
		t.Fatal(err)
	}
	if enrollment.ID == "" {
		t.Fatal("expected fallback enrollment ID")
	}
	if strings.Contains(token, strings.TrimPrefix(enrollment.ID, "local_")) {
		t.Fatalf("fallback ID %q leaks the access token", enrollment.ID)
	}
}
