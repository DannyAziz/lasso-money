package config

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type Paths struct {
	DataDir        string
	ConfigFile     string
	EnrollmentFile string
	DBFile         string
}

type Env map[string]string

func (e Env) Get(key string) string {
	if value := strings.TrimSpace(os.Getenv(key)); value != "" {
		return value
	}
	return strings.TrimSpace(e[key])
}

func (e Env) GetDefault(key, fallback string) string {
	if value := e.Get(key); value != "" {
		return value
	}
	return fallback
}

func ResolvePaths(configPath string) (Paths, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return Paths{}, err
	}
	dataDir := filepath.Join(home, ".lasso")
	if configPath == "" {
		configPath = filepath.Join(dataDir, "config.env")
	}
	configPath = ExpandHome(configPath)
	return Paths{
		DataDir:        dataDir,
		ConfigFile:     configPath,
		EnrollmentFile: filepath.Join(dataDir, "enrollment.json"),
		DBFile:         filepath.Join(dataDir, "lasso.db"),
	}, nil
}

func ResolveEnrollmentPath(paths Paths, env Env) string {
	if p := env.Get("TELLER_ENROLLMENT_PATH"); p != "" {
		return ExpandHome(p)
	}
	return paths.EnrollmentFile
}

func ExpandHome(path string) string {
	if path == "~" {
		home, err := os.UserHomeDir()
		if err == nil {
			return home
		}
	}
	if strings.HasPrefix(path, "~/") {
		home, err := os.UserHomeDir()
		if err == nil {
			return filepath.Join(home, path[2:])
		}
	}
	return path
}

func LoadEnvFile(path string) (Env, error) {
	file, err := os.Open(ExpandHome(path))
	if err != nil {
		return nil, err
	}
	defer file.Close()

	env := Env{}
	scanner := bufio.NewScanner(file)
	lineNo := 0
	for scanner.Scan() {
		lineNo++
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		line = strings.TrimPrefix(line, "export ")
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			return nil, fmt.Errorf("%s:%d: expected KEY=VALUE", path, lineNo)
		}
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)
		value = strings.Trim(value, `"'`)
		if key == "" {
			return nil, fmt.Errorf("%s:%d: empty key", path, lineNo)
		}
		env[key] = value
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return env, nil
}

func LoadOptionalEnvFile(path string) (Env, error) {
	env, err := LoadEnvFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return Env{}, err
		}
		return Env{}, err
	}
	return env, nil
}
