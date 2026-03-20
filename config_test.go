package routex

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestEnv_BasicResolution verifies the env() helper handles all cases.
func TestEnv_BasicResolution(t *testing.T) {
	t.Setenv("TEST_ROUTEX_MODEL", "claude-opus-4-6")

	tests := []struct {
		input string
		want  string
	}{
		{"plain-value", "plain-value"},               // no prefix — returned as-is
		{"env:TEST_ROUTEX_MODEL", "claude-opus-4-6"}, // env: prefix — reads the var
		{"env:ROUTEX_NO_EXIST", ""},                  // env: prefix — var not set → ""
		{"", ""},                                     // empty — returned as-is
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := env(tt.input)
			if got != tt.want {
				t.Errorf("env(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

// TestEnvOr_Fallback verifies envOr() returns the fallback when the value is empty.
func TestEnvOr_Fallback(t *testing.T) {
	tests := []struct {
		value    string
		fallback string
		want     string
	}{
		{"set-value", "fallback", "set-value"},          // value present — use it
		{"", "fallback", "fallback"},                    // empty — use fallback
		{"env:ROUTEX_NO_EXIST", "fallback", "fallback"}, // unset env var — use fallback
	}

	for _, tt := range tests {
		t.Run(tt.value+"/"+tt.fallback, func(t *testing.T) {
			got := envOr(tt.value, tt.fallback)
			if got != tt.want {
				t.Errorf("envOr(%q, %q) = %q, want %q", tt.value, tt.fallback, tt.want, tt.want)
			}
		})
	}
}

// TestBuildConfig_EnvInAnyField verifies that env: works in fields
// beyond just api_key — model, name, task input, etc.
func TestBuildConfig_EnvInAnyField(t *testing.T) {
	t.Setenv("TEST_ROUTEX_NAME", "my-crew-from-env")
	t.Setenv("TEST_ROUTEX_MODEL", "claude-haiku-4-5-20251001")
	t.Setenv("PLANNER_GOAL", "Testing env")
	t.Setenv("TEST_ROUTEX_TASK", "") // ensure ROUTEX_TASK override is not active

	raw := yamlFile{}
	raw.Runtime.Name = "env:TEST_ROUTEX_NAME"
	raw.Runtime.LLMProvider = "anthropic"
	raw.Runtime.Model = "env:TEST_ROUTEX_MODEL"
	raw.Runtime.APIKey = "sk-test-key"
	raw.Memory.Backend = "inmem"
	raw.Agents = []agent{
		{ID: "planner", Role: "planner", Restart: "one_for_one", Goal: "env:PLANNER_GOAL"},
	}

	cfg, err := buildConfig(raw)
	if err != nil {
		t.Fatalf("buildConfig() error: %v", err)
	}

	if cfg.Name != "my-crew-from-env" {
		t.Errorf("Name = %q, want %q", cfg.Name, "my-crew-from-env")
	}
	if cfg.LLM.Model != "claude-haiku-4-5-20251001" {
		t.Errorf("Model = %q, want %q", cfg.LLM.Model, "claude-haiku-4-5-20251001")
	}
	if cfg.Agents[0].Goal != "Testing env" {
		t.Errorf("Goal = %q, want %q", cfg.Agents[0].Goal, "Testing env")
	}
}

// TestBuildConfig_EnvInToolExtra verifies env: works in tool extra fields.
func TestBuildConfig_EnvInToolExtra(t *testing.T) {
	t.Setenv("TEST_ROUTEX_FROM_EMAIL", "agent@example.com")

	raw := yamlFile{}
	raw.Runtime.LLMProvider = "anthropic"
	raw.Runtime.Model = "claude-sonnet-4-6"
	raw.Runtime.APIKey = "sk-test"
	raw.Memory.Backend = "inmem"
	raw.Tools = []struct {
		Name       string            `yaml:"name"`
		APIKey     string            `yaml:"api_key"`
		BaseDir    string            `yaml:"base_dir"`
		MaxResults int               `yaml:"max_results"`
		Extra      map[string]string `yaml:"extra"`
	}{
		{
			Name:   "send_email",
			APIKey: "sg-test-key",
			Extra: map[string]string{
				"from_email": "env:TEST_ROUTEX_FROM_EMAIL",
				"from_name":  "Test Agent",
			},
		},
	}
	raw.Agents = []agent{
		{ID: "planner", Role: "planner", Restart: "one_for_one", Goal: "PLANNER_GOAL"},
	}

	cfg, err := buildConfig(raw)
	if err != nil {
		t.Fatalf("buildConfig() error: %v", err)
	}

	if len(cfg.ToolConfigs) != 1 {
		t.Fatalf("expected 1 tool config, got %d", len(cfg.ToolConfigs))
	}

	fromEmail := cfg.ToolConfigs[0].Extra["from_email"]
	if fromEmail != "agent@example.com" {
		t.Errorf("tool extra from_email = %q, want %q", fromEmail, "agent@example.com")
	}

	// Plain values in extra must also pass through unchanged
	fromName := cfg.ToolConfigs[0].Extra["from_name"]
	if fromName != "Test Agent" {
		t.Errorf("tool extra from_name = %q, want %q", fromName, "Test Agent")
	}
}

// ".." cannot escape the config file's directory.
func TestLoadEnvFile_PathTraversal(t *testing.T) {
	// Create a temp directory to act as the config directory
	configDir := t.TempDir()
	configPath := filepath.Join(configDir, "agents.yaml")

	// Create a .env file one level UP from the config dir —
	// this is what the attacker wants to read
	parentDir := filepath.Dir(configDir)
	parentEnv := filepath.Join(parentDir, ".env")
	if err := os.WriteFile(parentEnv, []byte("SECRET=leaked"), 0600); err != nil {
		t.Fatalf("setup: write parent .env: %v", err)
	}
	defer os.Remove(parentEnv)

	traversalCases := []string{
		"../.env",
		"../.",
		"../../.env",
		".././.env",
		"subdir/../../.env",
	}

	for _, envFile := range traversalCases {
		t.Run(envFile, func(t *testing.T) {
			err := loadEnvFile(envFile, configPath)
			if err == nil {
				t.Errorf("loadEnvFile(%q) should have rejected path traversal, got nil error", envFile)
				return
			}
			// Error message should explain what happened clearly
			if !strings.Contains(err.Error(), "outside the config directory") {
				t.Errorf("error should mention 'outside the config directory', got: %v", err)
			}
			// Verify the secret was NOT loaded into the environment
			if os.Getenv("SECRET") == "leaked" {
				t.Errorf("path traversal succeeded — SECRET was loaded from parent .env")
				os.Unsetenv("SECRET")
			}
		})
	}
}

func TestLoadEnvFile_Empty(t *testing.T) {
	// Empty string means no loading — must not error
	if err := loadEnvFile("", "agents.yaml"); err != nil {
		t.Errorf("loadEnvFile(\"\") should not error, got: %v", err)
	}
}

func TestLoadEnvFile_DotLoadsEnv(t *testing.T) {
	// "." loads .env from the config directory
	dir := t.TempDir()
	configPath := filepath.Join(dir, "agents.yaml")
	envPath := filepath.Join(dir, ".env")

	if err := os.WriteFile(envPath, []byte("TEST_ROUTEX_KEY=hello123"), 0600); err != nil {
		t.Fatalf("write .env: %v", err)
	}
	defer os.Unsetenv("TEST_ROUTEX_KEY")

	if err := loadEnvFile(".", configPath); err != nil {
		t.Fatalf("loadEnvFile(\".\") error: %v", err)
	}

	if got := os.Getenv("TEST_ROUTEX_KEY"); got != "hello123" {
		t.Errorf("TEST_ROUTEX_KEY = %q, want %q", got, "hello123")
	}
}

func TestLoadEnvFile_DotMissingIsOk(t *testing.T) {
	// "." with no .env present is not an error — developer may not have
	// created the file yet
	dir := t.TempDir()
	configPath := filepath.Join(dir, "agents.yaml")
	// deliberately do not create .env

	if err := loadEnvFile(".", configPath); err != nil {
		t.Errorf("loadEnvFile(\".\") with missing .env should not error, got: %v", err)
	}
}

func TestLoadEnvFile_NamedFileMissingIsError(t *testing.T) {
	// An explicitly named file that doesn't exist is a real mistake
	dir := t.TempDir()
	configPath := filepath.Join(dir, "agents.yaml")

	err := loadEnvFile(".env.prod", configPath)
	if err == nil {
		t.Error("loadEnvFile(\".env.prod\") on missing file should error, got nil")
	}
}

func TestLoadEnvFile_ExistingEnvNotOverwritten(t *testing.T) {
	// Variables already in the environment must not be overwritten
	// by the .env file — shell exports and platform secrets always win
	dir := t.TempDir()
	configPath := filepath.Join(dir, "agents.yaml")
	envPath := filepath.Join(dir, ".env")

	// Set the variable in the environment BEFORE loading
	t.Setenv("TEST_ROUTEX_OVERWRITE", "original")

	// .env tries to overwrite it
	if err := os.WriteFile(envPath, []byte("TEST_ROUTEX_OVERWRITE=overwritten"), 0600); err != nil {
		t.Fatalf("write .env: %v", err)
	}

	if err := loadEnvFile(".", configPath); err != nil {
		t.Fatalf("loadEnvFile error: %v", err)
	}

	// Original value must still be there
	if got := os.Getenv("TEST_ROUTEX_OVERWRITE"); got != "original" {
		t.Errorf("env var was overwritten: got %q, want %q", got, "original")
	}
}

func TestLoadEnvFile_SubdirectoryAllowed(t *testing.T) {
	// A file inside a subdirectory of the config dir is allowed
	dir := t.TempDir()
	configPath := filepath.Join(dir, "agents.yaml")
	subDir := filepath.Join(dir, "secrets")
	if err := os.MkdirAll(subDir, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	envPath := filepath.Join(subDir, ".env")
	if err := os.WriteFile(envPath, []byte("TEST_ROUTEX_SUB=subvalue"), 0600); err != nil {
		t.Fatalf("write .env: %v", err)
	}
	defer os.Unsetenv("TEST_ROUTEX_SUB")

	if err := loadEnvFile("secrets/.env", configPath); err != nil {
		t.Errorf("loadEnvFile(\"secrets/.env\") should be allowed, got: %v", err)
	}
}
