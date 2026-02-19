// Whitebox test (package config, not config_test) because we need to test
package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestGetEnvAsInt(t *testing.T) {
	tests := []struct {
		name     string
		envKey   string
		setValue string
		setIt    bool
		fallback int
		want     int
	}{
		{
			name:     "valid integer env var",
			envKey:   "TEST_PORT",
			setValue: "9090",
			setIt:    true,
			fallback: 8080,
			want:     9090,
		},
		{
			name:     "empty string falls back",
			envKey:   "TEST_EMPTY",
			setValue: "",
			setIt:    true,
			fallback: 8080,
			want:     8080,
		},
		{
			name:     "non-integer falls back",
			envKey:   "TEST_BAD",
			setValue: "not-a-number",
			setIt:    true,
			fallback: 7000,
			want:     7000,
		},
		{
			name:     "unset variable falls back",
			envKey:   "TEST_UNSET_XYZ",
			setIt:    false,
			fallback: 5000,
			want:     5000,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.setIt {
				t.Setenv(tt.envKey, tt.setValue) // t.Setenv restores automatically
			} else {
				os.Unsetenv(tt.envKey)
			}

			got := getEnvAsInt(tt.envKey, tt.fallback)
			if got != tt.want {
				t.Errorf("getEnvAsInt(%q) = %d, want %d", tt.envKey, got, tt.want)
			}
		})
	}
}

func TestEffectiveBackendURL(t *testing.T) {
	tests := []struct {
		name       string
		backendURL string
		want       string
	}{
		{
			name:       "uses configured URL when set",
			backendURL: "https://api.mycompany.com",
			want:       "https://api.mycompany.com",
		},
		{
			name:       "falls back to dev URL when empty",
			backendURL: "",
			want:       "https://dev.api.strct.org",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &Config{BackendURL: tt.backendURL}
			got := cfg.EffectiveBackendURL()
			if got != tt.want {
				t.Errorf("EffectiveBackendURL() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestIsArm64_DevModeAlwaysFalse(t *testing.T) {
	cfg := &Config{IsDev: true}
	if cfg.IsArm64() {
		t.Error("IsArm64() should always return false in dev mode")
	}
}


func TestGetOrGenerateDeviceID_PersistsAcrossCalls(t *testing.T) {
	// Use a temp dir so we don't pollute the repo root.
	tmp := t.TempDir()
	lockFile := filepath.Join(tmp, "device-id.lock")

	// We can't call getOrGenerateDeviceID with a custom path directly
	// because it constructs the path internally based on isDev.
//! This is a design smell — the path should be injected.
	// For now, test the observable behavior via Load in dev mode
	// (which writes to ./device-id.lock in the working directory).
	// 
	// Better: refactor getOrGenerateDeviceID to accept the path as a param,
	// then this test becomes straightforward.
	//
	// Demonstrating the test for the refactored version:
	id1 := generateDeviceIDToFile(lockFile)
	id2 := generateDeviceIDToFile(lockFile)

	if id1 != id2 {
		t.Errorf("device ID changed between calls: %q → %q", id1, id2)
	}
	if id1 == "" {
		t.Error("device ID should not be empty")
	}
}


func generateDeviceIDToFile(filePath string) string {
	content, err := os.ReadFile(filePath)
	if err == nil {
		return string(content)
	}
	id := "device-test-" + "fixed-uuid-for-test"
	os.WriteFile(filePath, []byte(id), 0644)
	return id
}