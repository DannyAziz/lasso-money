package teller

import (
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

func LoadEnrollment(path string) (Enrollment, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Enrollment{}, err
	}
	var enrollment Enrollment
	if err := json.Unmarshal(data, &enrollment); err != nil {
		return Enrollment{}, err
	}
	if enrollment.AccessToken == "" {
		return Enrollment{}, fmt.Errorf("enrollment %s is missing access_token", path)
	}
	if enrollment.Provider == "" {
		enrollment.Provider = "teller"
	}
	return enrollment, nil
}

func SaveEnrollment(path string, enrollment Enrollment) error {
	if enrollment.AccessToken == "" {
		return fmt.Errorf("refusing to save enrollment without access token")
	}
	if enrollment.Provider == "" {
		enrollment.Provider = "teller"
	}
	data, err := json.MarshalIndent(enrollment, "", "  ")
	if err != nil {
		return err
	}
	return writeSecretFile(path, append(data, '\n'))
}

func MaskToken(token string) string {
	token = strings.TrimSpace(token)
	if token == "" {
		return ""
	}
	if len(token) <= 10 {
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
		if len(accessToken) >= 16 {
			enrollment.ID = accessToken[:16]
		} else {
			enrollment.ID = accessToken
		}
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
