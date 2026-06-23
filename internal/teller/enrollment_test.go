package teller

import (
	"encoding/json"
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

func TestAddEnrollmentMigratesLegacyAndOnlyReplacesSameID(t *testing.T) {
	path := filepath.Join(t.TempDir(), "enrollment.json")
	legacy := Enrollment{ID: "enr_1", InstitutionID: "bank_1", AccessToken: "token_1"}
	data, _ := json.Marshal(legacy)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := AddEnrollment(path, Enrollment{ID: "enr_2", InstitutionID: "bank_1", AccessToken: "token_2"}); err != nil {
		t.Fatal(err)
	}
	if err := AddEnrollment(path, Enrollment{ID: "enr_1", InstitutionID: "bank_1", AccessToken: "token_1_new"}); err != nil {
		t.Fatal(err)
	}
	enrollments, err := LoadEnrollments(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(enrollments) != 2 || enrollments[0].AccessToken != "token_1_new" || enrollments[1].AccessToken != "token_2" {
		t.Fatalf("enrollments = %#v", enrollments)
	}
	var file EnrollmentFile
	if data, err := os.ReadFile(path); err != nil || json.Unmarshal(data, &file) != nil || len(file.Enrollments) != 2 {
		t.Fatalf("file is not canonical: %s, err=%v", data, err)
	}
}

func TestLoadEnrollmentsRejectsInvalidLegacyShape(t *testing.T) {
	path := filepath.Join(t.TempDir(), "enrollment.json")
	if err := os.WriteFile(path, []byte(`{"access_token":[]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if enrollments, err := LoadEnrollments(path); err == nil || enrollments != nil {
		t.Fatalf("enrollments = %#v, err = %v; want error", enrollments, err)
	}
}

func TestAddEnrollmentExplainsIDLessLegacyFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "enrollment.json")
	data, _ := json.Marshal(Enrollment{AccessToken: "legacy_token"})
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	err := AddEnrollment(path, Enrollment{ID: "enr_2", AccessToken: "token_2"})
	if err == nil || !strings.Contains(err.Error(), "reconnect it before adding another") {
		t.Fatalf("err = %v", err)
	}
}

func TestEnrollmentValidation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "enrollment.json")
	for name, enrollments := range map[string][]Enrollment{
		"empty":         {},
		"missing token": {{ID: "enr_1"}},
		"missing id":    {{ID: "enr_1", AccessToken: "a"}, {AccessToken: "b"}},
		"duplicate id":  {{ID: "enr_1", AccessToken: "a"}, {ID: "enr_1", AccessToken: "b"}},
	} {
		t.Run(name, func(t *testing.T) {
			if err := SaveEnrollments(path, enrollments); err == nil {
				t.Fatal("want validation error")
			}
		})
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
