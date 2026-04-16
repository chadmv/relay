// internal/cli/config_test.go
package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func withTempConfigPath(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	orig := configFilePathFn
	configFilePathFn = func() (string, error) { return path, nil }
	t.Cleanup(func() { configFilePathFn = orig })
	return path
}

func TestLoadConfig_MissingFile(t *testing.T) {
	withTempConfigPath(t) // points at non-existent file
	t.Setenv("RELAY_URL", "")
	t.Setenv("RELAY_TOKEN", "")

	cfg, err := LoadConfig()
	require.NoError(t, err)
	require.Equal(t, "", cfg.ServerURL)
	require.Equal(t, "", cfg.Token)
}

func TestLoadConfig_FileOnly(t *testing.T) {
	path := withTempConfigPath(t)
	t.Setenv("RELAY_URL", "")
	t.Setenv("RELAY_TOKEN", "")

	data, _ := json.Marshal(Config{ServerURL: "http://example.com", Token: "abc"})
	require.NoError(t, os.WriteFile(path, data, 0600))

	cfg, err := LoadConfig()
	require.NoError(t, err)
	require.Equal(t, "http://example.com", cfg.ServerURL)
	require.Equal(t, "abc", cfg.Token)
}

func TestLoadConfig_EnvOnly(t *testing.T) {
	withTempConfigPath(t)
	t.Setenv("RELAY_URL", "http://env.example.com")
	t.Setenv("RELAY_TOKEN", "env-token")

	cfg, err := LoadConfig()
	require.NoError(t, err)
	require.Equal(t, "http://env.example.com", cfg.ServerURL)
	require.Equal(t, "env-token", cfg.Token)
}

func TestLoadConfig_EnvOverridesFile(t *testing.T) {
	path := withTempConfigPath(t)
	t.Setenv("RELAY_URL", "http://override.com")
	t.Setenv("RELAY_TOKEN", "override-token")

	data, _ := json.Marshal(Config{ServerURL: "http://file.com", Token: "file-token"})
	require.NoError(t, os.WriteFile(path, data, 0600))

	cfg, err := LoadConfig()
	require.NoError(t, err)
	require.Equal(t, "http://override.com", cfg.ServerURL)
	require.Equal(t, "override-token", cfg.Token)
}

func TestSaveConfig(t *testing.T) {
	path := withTempConfigPath(t)

	cfg := &Config{ServerURL: "http://save.com", Token: "save-tok"}
	require.NoError(t, SaveConfig(cfg))

	data, err := os.ReadFile(path)
	require.NoError(t, err)

	var loaded Config
	require.NoError(t, json.Unmarshal(data, &loaded))
	require.Equal(t, "http://save.com", loaded.ServerURL)
	require.Equal(t, "save-tok", loaded.Token)
}
