// internal/cli/config.go
package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
)

// Config holds all settings the CLI needs to talk to relay-server.
type Config struct {
	ServerURL string `json:"server_url"`
	Token     string `json:"token"`
}

// configFilePathFn is a variable so tests can override it.
var configFilePathFn = defaultConfigFilePath

// LoadConfig reads the config file then applies RELAY_URL / RELAY_TOKEN overrides.
// A missing config file is not an error — an empty Config is returned.
func LoadConfig() (*Config, error) {
	cfg := &Config{}
	path, err := configFilePathFn()
	if err == nil {
		if data, readErr := os.ReadFile(path); readErr == nil {
			_ = json.Unmarshal(data, cfg)
		}
	}
	if v := os.Getenv("RELAY_URL"); v != "" {
		cfg.ServerURL = v
	}
	if v := os.Getenv("RELAY_TOKEN"); v != "" {
		cfg.Token = v
	}
	return cfg, nil
}

// SaveConfig writes cfg to the config file, creating parent directories as needed.
func SaveConfig(cfg *Config) error {
	path, err := configFilePathFn()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return fmt.Errorf("create config dir: %w", err)
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0600)
}

// NewClient constructs an authenticated HTTP client from cfg.
func (cfg *Config) NewClient() *Client {
	return NewClient(cfg.ServerURL, cfg.Token)
}

func defaultConfigFilePath() (string, error) {
	if runtime.GOOS == "windows" {
		appData := os.Getenv("APPDATA")
		if appData == "" {
			return "", fmt.Errorf("APPDATA not set")
		}
		return filepath.Join(appData, "relay", "config.json"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("user home dir: %w", err)
	}
	return filepath.Join(home, ".relay", "config.json"), nil
}
