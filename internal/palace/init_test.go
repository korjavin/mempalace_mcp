package palace

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestEnsureInitializedWithHome_CreatesAll(t *testing.T) {
	home := t.TempDir()
	palacePath := filepath.Join(t.TempDir(), "palace")

	if err := EnsureInitializedWithHome(palacePath, home); err != nil {
		t.Fatalf("EnsureInitializedWithHome: %v", err)
	}

	// Palace directory must exist.
	if _, err := os.Stat(palacePath); err != nil {
		t.Fatalf("palace dir not created: %v", err)
	}

	// Config file must exist and be valid JSON.
	configFile := filepath.Join(home, ".mempalace", "config.json")
	data, err := os.ReadFile(configFile)
	if err != nil {
		t.Fatalf("config.json not created: %v", err)
	}

	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		t.Fatalf("config.json not valid JSON: %v", err)
	}

	if cfg.PalacePath != palacePath {
		t.Errorf("palace_path = %q, want %q", cfg.PalacePath, palacePath)
	}
	if cfg.CollectionName != "mempalace_drawers" {
		t.Errorf("collection_name = %q, want %q", cfg.CollectionName, "mempalace_drawers")
	}
}

func TestEnsureInitializedWithHome_Idempotent(t *testing.T) {
	home := t.TempDir()
	palacePath := filepath.Join(t.TempDir(), "palace")

	// First call: creates everything.
	if err := EnsureInitializedWithHome(palacePath, home); err != nil {
		t.Fatalf("first call: %v", err)
	}

	configFile := filepath.Join(home, ".mempalace", "config.json")
	origData, err := os.ReadFile(configFile)
	if err != nil {
		t.Fatalf("read config after first call: %v", err)
	}

	// Second call: should not corrupt anything.
	if err := EnsureInitializedWithHome(palacePath, home); err != nil {
		t.Fatalf("second call: %v", err)
	}

	afterData, err := os.ReadFile(configFile)
	if err != nil {
		t.Fatalf("read config after second call: %v", err)
	}

	if string(origData) != string(afterData) {
		t.Errorf("config.json changed on second call:\nbefore: %s\nafter:  %s", origData, afterData)
	}
}

func TestEnsureInitializedWithHome_ExistingConfigPreserved(t *testing.T) {
	home := t.TempDir()
	palacePath := filepath.Join(t.TempDir(), "palace")

	// Pre-create config with custom content.
	configDir := filepath.Join(home, ".mempalace")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatal(err)
	}
	customConfig := `{"palace_path": "/custom", "collection_name": "custom_col"}`
	configFile := filepath.Join(configDir, "config.json")
	if err := os.WriteFile(configFile, []byte(customConfig), 0o644); err != nil {
		t.Fatal(err)
	}

	// EnsureInitialized should not overwrite existing config.
	if err := EnsureInitializedWithHome(palacePath, home); err != nil {
		t.Fatalf("EnsureInitializedWithHome: %v", err)
	}

	data, err := os.ReadFile(configFile)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != customConfig {
		t.Errorf("existing config was overwritten: got %s", data)
	}
}
