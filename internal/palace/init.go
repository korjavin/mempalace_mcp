package palace

import (
	"encoding/json"
	"errors"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
)

// Config represents the mempalace config.json structure.
type Config struct {
	PalacePath     string `json:"palace_path"`
	CollectionName string `json:"collection_name"`
}

// EnsureInitialized checks if the mempalace config and palace directory exist,
// creating them if needed. This allows the MCP server to start without manual
// "mempalace init" which requires interactive prompts.
func EnsureInitialized(palacePath string) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	return EnsureInitializedWithHome(palacePath, home)
}

// EnsureInitializedWithHome is like EnsureInitialized but accepts an explicit
// home directory, making it testable without affecting the real home dir.
func EnsureInitializedWithHome(palacePath, home string) error {
	configDir := filepath.Join(home, ".mempalace")
	configFile := filepath.Join(configDir, "config.json")

	// Create palace data directory if missing.
	if err := os.MkdirAll(palacePath, 0o755); err != nil {
		return err
	}

	// Check if config already exists.
	if _, err := os.Stat(configFile); err == nil {
		slog.Info("mempalace config already exists", "path", configFile)
		return nil
	} else if !errors.Is(err, fs.ErrNotExist) {
		return err
	}

	// Create config directory.
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		return err
	}

	cfg := Config{
		PalacePath:     palacePath,
		CollectionName: "mempalace_drawers",
	}

	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}

	if err := os.WriteFile(configFile, data, 0o644); err != nil {
		return err
	}

	slog.Info("created mempalace config", "path", configFile, "palace_path", palacePath)
	return nil
}
