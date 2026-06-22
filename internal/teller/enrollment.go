package teller

import (
	"crypto/rand"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type Enrollment struct {
	ID              string `json:"id"`
	InstitutionID   string `json:"institution_id,omitempty"`
	InstitutionName string `json:"institution_name,omitempty"`
	AccessToken     string `json:"access_token"`
	Provider        string `json:"provider,omitempty"`
}

type EnrollmentFile struct {
	Enrollments []Enrollment `json:"enrollments"`
}

func LoadEnrollments(path string) ([]Enrollment, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var file EnrollmentFile
	if err := json.Unmarshal(data, &file); err != nil || file.Enrollments == nil {
		var legacy Enrollment
		if legacyErr := json.Unmarshal(data, &legacy); legacyErr != nil {
			return nil, legacyErr
		}
		file.Enrollments = []Enrollment{legacy}
	}
	return validateEnrollments(path, file.Enrollments)
}

func validateEnrollments(path string, enrollments []Enrollment) ([]Enrollment, error) {
	if len(enrollments) == 0 {
		return nil, fmt.Errorf("enrollment %s contains no enrollments", path)
	}
	seen := map[string]bool{}
	for i := range enrollments {
		enrollments[i].AccessToken = strings.TrimSpace(enrollments[i].AccessToken)
		if enrollments[i].AccessToken == "" {
			return nil, fmt.Errorf("enrollment %s is missing access_token", path)
		}
		if enrollments[i].Provider == "" {
			enrollments[i].Provider = "teller"
		}
		if len(enrollments) > 1 && strings.TrimSpace(enrollments[i].ID) == "" {
			return nil, fmt.Errorf("enrollment %s has an item without id", path)
		}
		if enrollments[i].ID != "" && seen[enrollments[i].ID] {
			return nil, fmt.Errorf("enrollment %s contains duplicate id %q", path, enrollments[i].ID)
		}
		seen[enrollments[i].ID] = true
	}
	return enrollments, nil
}

func SaveEnrollments(path string, enrollments []Enrollment) error {
	enrollments, err := validateEnrollments(path, enrollments)
	if err != nil {
		return err
	}
	data, err := json.MarshalIndent(EnrollmentFile{Enrollments: enrollments}, "", "  ")
	if err != nil {
		return err
	}
	return writeSecretFile(path, append(data, '\n'))
}

func LoadEnrollment(path string) (Enrollment, error) {
	enrollments, err := LoadEnrollments(path)
	if err != nil {
		return Enrollment{}, err
	}
	if len(enrollments) != 1 {
		return Enrollment{}, fmt.Errorf("enrollment %s contains %d enrollments", path, len(enrollments))
	}
	return enrollments[0], nil
}

func SaveEnrollment(path string, enrollment Enrollment) error {
	return SaveEnrollments(path, []Enrollment{enrollment})
}

func AddEnrollment(path string, enrollment Enrollment) error {
	enrollments, err := LoadEnrollments(path)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	for i, existing := range enrollments {
		if existing.ID == enrollment.ID {
			enrollments[i] = enrollment
			return SaveEnrollments(path, enrollments)
		}
	}
	return SaveEnrollments(path, append(enrollments, enrollment))
}

func MaskToken(token string) string {
	token = strings.TrimSpace(token)
	if token == "" {
		return ""
	}
	if len(token) <= 10 {
		if len(token) <= 2 {
			return "…"
		}
		return token[:2] + "…"
	}
	return token[:6] + "…" + token[len(token)-4:]
}

func NormalizeConnectPayload(payload map[string]any) (Enrollment, error) {
	accessToken, _ := payload["accessToken"].(string)
	if accessToken == "" {
		accessToken, _ = payload["access_token"].(string)
	}
	if accessToken == "" {
		return Enrollment{}, fmt.Errorf("connect payload missing accessToken")
	}

	enrollment := Enrollment{AccessToken: accessToken, Provider: "teller"}
	if blob, ok := payload["enrollment"].(map[string]any); ok {
		enrollment.ID, _ = blob["id"].(string)
		if institution, ok := blob["institution"].(map[string]any); ok {
			enrollment.InstitutionID, _ = institution["id"].(string)
			enrollment.InstitutionName, _ = institution["name"].(string)
		}
	}
	if enrollment.ID == "" {
		enrollment.ID = "local_" + rand.Text()
	}
	return enrollment, nil
}

func writeSecretFile(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		return err
	}
	return os.Chmod(path, 0o600)
}
