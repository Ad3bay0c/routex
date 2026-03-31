package routex

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
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

func writeAgentsYAML(t *testing.T, dir, baseURL string, extraRuntime, extraSections string) string {
	t.Helper()
	path := filepath.Join(dir, "agents.yaml")
	body := fmt.Sprintf(`runtime:
  name: loadcfg
  llm_provider: openai
  model: gpt-4o
  api_key: test-key
  base_url: %q
%s
memory:
  backend: inmem
%s
agents:
  - id: a1
    role: planner
    restart: one_for_one
    goal: accomplish the task
`, baseURL, extraRuntime, extraSections)
	if err := os.WriteFile(path, []byte(body), 0600); err != nil {
		t.Fatalf("write agents.yaml: %v", err)
	}
	return path
}

func TestLoadConfig_Success(t *testing.T) {
	srv := mockOpenAIServer(t, "ok")
	t.Cleanup(srv.Close)

	dir := t.TempDir()
	path := writeAgentsYAML(t, dir, srv.URL, "", "")

	rt, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	t.Cleanup(rt.Stop)
	if rt.cfg.Name != "loadcfg" {
		t.Errorf("Name = %q, want loadcfg", rt.cfg.Name)
	}
}

func TestLoadConfig_WithTaskInput(t *testing.T) {
	srv := mockOpenAIServer(t, "ok")
	t.Cleanup(srv.Close)

	dir := t.TempDir()
	path := writeAgentsYAML(t, dir, srv.URL, "", `task:
  input: "from yaml"
`)

	rt, err := LoadConfig(path, WithTaskInput("from option"))
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	t.Cleanup(rt.Stop)
	if rt.cfg.Task.Input != "from option" {
		t.Errorf("Task.Input = %q, want from option", rt.cfg.Task.Input)
	}
}

func TestLoadConfig_WithEnvFileOverridesYAML(t *testing.T) {
	srv := mockOpenAIServer(t, "ok")
	t.Cleanup(srv.Close)

	dir := t.TempDir()
	// YAML asks for a different env file; WithEnvFile must win. Use a fresh
	// var name — godotenv does not overwrite vars already in the environment
	// (including empty values set via t.Setenv).
	key := "TEST_ROUTEX_WITH_ENVFILE_OVERRIDE"
	path := writeAgentsYAML(t, dir, srv.URL, `  env_file: ".env.official"
`, "")
	if err := os.WriteFile(filepath.Join(dir, ".env.override"), []byte(key+"=from-override\n"), 0600); err != nil {
		t.Fatal(err)
	}
	_ = os.Unsetenv(key)

	rt, err := LoadConfig(path, WithEnvFile(".env.override"))
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	t.Cleanup(rt.Stop)
	if os.Getenv(key) != "from-override" {
		t.Errorf("%s = %q, want from-override", key, os.Getenv(key))
	}
	_ = os.Unsetenv(key)
}

func TestLoadConfig_ReadFileError(t *testing.T) {
	_, err := LoadConfig(filepath.Join(t.TempDir(), "nope.yaml"))
	if err == nil {
		t.Fatal("expected error for missing file")
	}
	if !strings.Contains(err.Error(), "cannot read config file") {
		t.Errorf("error = %v", err)
	}
}

func TestLoadConfig_InvalidYAML(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "agents.yaml")
	if err := os.WriteFile(path, []byte("runtime: [unclosed"), 0600); err != nil {
		t.Fatal(err)
	}
	_, err := LoadConfig(path)
	if err == nil || !strings.Contains(err.Error(), "invalid YAML") {
		t.Fatalf("want invalid YAML error, got %v", err)
	}
}

func TestLoadConfig_EnvFileLoadError(t *testing.T) {
	srv := mockOpenAIServer(t, "ok")
	t.Cleanup(srv.Close)

	dir := t.TempDir()
	path := writeAgentsYAML(t, dir, srv.URL, `  env_file: ".env"
`, "")
	// Peek loads "." — no .env is ok. Use WithEnvFile to require missing named file.
	_, err := LoadConfig(path, WithEnvFile("missing-named.env"))
	if err == nil || !strings.Contains(err.Error(), "load env file") {
		t.Fatalf("want load env file error, got %v", err)
	}
}

func TestLoadConfig_BuildConfigError(t *testing.T) {
	srv := mockOpenAIServer(t, "ok")
	t.Cleanup(srv.Close)

	dir := t.TempDir()
	p := filepath.Join(dir, "agents.yaml")
	yaml := fmt.Sprintf(`runtime:
  name: x
  llm_provider: openai
  model: gpt-4o
  api_key: key
  base_url: %q
memory:
  backend: inmem
agents: []
`, srv.URL)
	if err := os.WriteFile(p, []byte(yaml), 0600); err != nil {
		t.Fatal(err)
	}
	_, err := LoadConfig(p)
	if err == nil || !strings.Contains(err.Error(), "at least one agent") {
		t.Fatalf("want config error, got %v", err)
	}
}

func TestWithEnvFile_WithTaskInput_Functions(t *testing.T) {
	var o loadOptions
	WithEnvFile("/tmp/e")(&o)
	if o.envFile != "/tmp/e" {
		t.Errorf("envFile = %q", o.envFile)
	}
	WithTaskInput("task")(&o)
	if o.taskInput != "task" {
		t.Errorf("taskInput = %q", o.taskInput)
	}
}

func minimalValidRaw() yamlFile {
	raw := yamlFile{}
	raw.Runtime.LLMProvider = "anthropic"
	raw.Runtime.Model = "claude-sonnet-4-6"
	raw.Runtime.APIKey = "sk-ant-test"
	raw.Memory.Backend = "inmem"
	raw.Agents = []agent{
		{ID: "p", Role: "planner", Restart: "one_for_one", Goal: "g"},
	}
	return raw
}

func TestBuildConfig_RuntimeLLMValidation(t *testing.T) {
	_, err := buildConfig(yamlFile{})
	if err == nil || !strings.Contains(err.Error(), "llm_provider") {
		t.Fatalf("want llm_provider error: %v", err)
	}

	raw := yamlFile{}
	raw.Runtime.LLMProvider = "openai"
	_, err = buildConfig(raw)
	if err == nil || !strings.Contains(err.Error(), "model is required") {
		t.Fatalf("want model error: %v", err)
	}

	raw.Runtime.Model = "gpt-4o"
	_, err = buildConfig(raw)
	if err == nil || !strings.Contains(err.Error(), "api_key is required") {
		t.Fatalf("want api_key error: %v", err)
	}
}

func TestBuildConfig_OllamaNoAPIKey(t *testing.T) {
	raw := minimalValidRaw()
	raw.Runtime.LLMProvider = "ollama"
	raw.Runtime.Model = "llama3"
	raw.Runtime.APIKey = ""
	_, err := buildConfig(raw)
	if err != nil {
		t.Fatalf("ollama without api_key should be valid at config level: %v", err)
	}
}

func TestBuildConfig_TaskAndObservability(t *testing.T) {
	t.Setenv("ROUTEX_TASK", "env task wins")
	defer t.Setenv("ROUTEX_TASK", "")

	raw := minimalValidRaw()
	raw.Task.Input = "yaml task"
	raw.Task.OutputFile = "/tmp/out.md"
	raw.Task.MaxDuration = "5m"
	raw.Observability.Tracing = true
	raw.Observability.Metrics = true
	raw.Observability.JaegerEndpoint = "http://jaeger:4318"
	raw.Observability.MetricsAddr = ":19999"

	cfg, err := buildConfig(raw)
	if err != nil {
		t.Fatalf("buildConfig: %v", err)
	}
	if cfg.Task.Input != "env task wins" {
		t.Errorf("Task.Input = %q", cfg.Task.Input)
	}
	if cfg.Task.OutputFile != "/tmp/out.md" {
		t.Errorf("OutputFile = %q", cfg.Task.OutputFile)
	}
	if cfg.Task.MaxDuration != 5*time.Minute {
		t.Errorf("MaxDuration = %v", cfg.Task.MaxDuration)
	}
	if !cfg.Observability.Tracing || !cfg.Observability.Metrics {
		t.Errorf("observability flags")
	}
	if cfg.Observability.JaegerEndpoint != "http://jaeger:4318" {
		t.Errorf("JaegerEndpoint = %q", cfg.Observability.JaegerEndpoint)
	}
	if cfg.Observability.MetricsAddr != ":19999" {
		t.Errorf("MetricsAddr = %q", cfg.Observability.MetricsAddr)
	}

	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "http://otel:4318")
	t.Setenv("ROUTEX_METRICS_ADDR", ":20000")
	defer t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "")
	defer t.Setenv("ROUTEX_METRICS_ADDR", "")

	raw.Observability.JaegerEndpoint = ""
	raw.Observability.MetricsAddr = ""
	cfg, err = buildConfig(raw)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Observability.JaegerEndpoint != "http://otel:4318" {
		t.Errorf("fallback JaegerEndpoint = %q", cfg.Observability.JaegerEndpoint)
	}
	if cfg.Observability.MetricsAddr != ":20000" {
		t.Errorf("fallback MetricsAddr = %q", cfg.Observability.MetricsAddr)
	}
}

func TestBuildConfig_TaskMaxDurationInvalid(t *testing.T) {
	raw := minimalValidRaw()
	raw.Task.MaxDuration = "not-a-duration"
	_, err := buildConfig(raw)
	if err == nil || !strings.Contains(err.Error(), "max_duration") {
		t.Fatalf("want max_duration error: %v", err)
	}
}

func TestBuildConfig_ToolsValidation(t *testing.T) {
	raw := minimalValidRaw()
	raw.Tools = []struct {
		Name       string            `yaml:"name"`
		APIKey     string            `yaml:"api_key"`
		BaseDir    string            `yaml:"base_dir"`
		MaxResults int               `yaml:"max_results"`
		Extra      map[string]string `yaml:"extra"`
	}{{Name: ""}}
	_, err := buildConfig(raw)
	if err == nil || !strings.Contains(err.Error(), "must have a name") {
		t.Fatalf("want tool name error: %v", err)
	}

	raw = minimalValidRaw()
	raw.Tools = []struct {
		Name       string            `yaml:"name"`
		APIKey     string            `yaml:"api_key"`
		BaseDir    string            `yaml:"base_dir"`
		MaxResults int               `yaml:"max_results"`
		Extra      map[string]string `yaml:"extra"`
	}{{Name: "mcp", Extra: map[string]string{}}}
	_, err = buildConfig(raw)
	if err == nil || !strings.Contains(err.Error(), "server_url") {
		t.Fatalf("want mcp server_url error: %v", err)
	}

	raw = minimalValidRaw()
	raw.Tools = []struct {
		Name       string            `yaml:"name"`
		APIKey     string            `yaml:"api_key"`
		BaseDir    string            `yaml:"base_dir"`
		MaxResults int               `yaml:"max_results"`
		Extra      map[string]string `yaml:"extra"`
	}{{Name: "mcp", Extra: map[string]string{"server_url": "http://localhost:9"}}}
	cfg, err := buildConfig(raw)
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.ToolConfigs) != 1 || cfg.ToolConfigs[0].Name != "mcp" {
		t.Fatalf("tool config: %+v", cfg.ToolConfigs)
	}
}

func TestBuildConfig_AgentsValidation(t *testing.T) {
	raw := minimalValidRaw()
	raw.Agents = []agent{}
	_, err := buildConfig(raw)
	if err == nil || !strings.Contains(err.Error(), "at least one agent") {
		t.Fatalf("want agents required: %v", err)
	}

	raw = minimalValidRaw()
	raw.Agents = []agent{{ID: "", Role: "planner", Restart: "one_for_one", Goal: "g"}}
	_, err = buildConfig(raw)
	if err == nil || !strings.Contains(err.Error(), "id is required") {
		t.Fatalf("want id error: %v", err)
	}

	raw = minimalValidRaw()
	raw.Agents = []agent{
		{ID: "x", Role: "planner", Restart: "one_for_one", Goal: "g"},
		{ID: "x", Role: "writer", Restart: "one_for_one", Goal: "g"},
	}
	_, err = buildConfig(raw)
	if err == nil || !strings.Contains(err.Error(), "duplicate agent id") {
		t.Fatalf("want duplicate id: %v", err)
	}

	raw = minimalValidRaw()
	raw.Agents[0].Role = "not-a-role"
	_, err = buildConfig(raw)
	if err == nil || !strings.Contains(err.Error(), "unknown role") {
		t.Fatalf("want role error: %v", err)
	}

	raw = minimalValidRaw()
	raw.Agents[0].Goal = ""
	_, err = buildConfig(raw)
	if err == nil || !strings.Contains(err.Error(), "goal is required") {
		t.Fatalf("want goal error: %v", err)
	}

	raw = minimalValidRaw()
	raw.Agents[0].Timeout = "bad"
	_, err = buildConfig(raw)
	if err == nil || !strings.Contains(err.Error(), "timeout") {
		t.Fatalf("want timeout error: %v", err)
	}

	raw = minimalValidRaw()
	raw.Agents[0].Restart = "invalid_policy"
	_, err = buildConfig(raw)
	if err == nil || !strings.Contains(err.Error(), "restart") {
		t.Fatalf("want restart error: %v", err)
	}

	raw = minimalValidRaw()
	raw.Agents[0].DependsOn = []string{"missing"}
	_, err = buildConfig(raw)
	if err == nil || !strings.Contains(err.Error(), "depends_on") {
		t.Fatalf("want depends_on error: %v", err)
	}
}

func TestBuildConfig_AgentLLMBlock(t *testing.T) {
	raw := minimalValidRaw()
	raw.Agents[0].LLM = &struct {
		Provider string `yaml:"provider"`
		Model    string `yaml:"model"`
		APIKey   string `yaml:"api_key"`
		BaseURL  string `yaml:"base_url"`
	}{Provider: "", Model: "m"}
	_, err := buildConfig(raw)
	if err == nil || !strings.Contains(err.Error(), "llm.provider") {
		t.Fatalf("want llm.provider error: %v", err)
	}

	raw = minimalValidRaw()
	raw.Agents[0].LLM = &struct {
		Provider string `yaml:"provider"`
		Model    string `yaml:"model"`
		APIKey   string `yaml:"api_key"`
		BaseURL  string `yaml:"base_url"`
	}{Provider: "openai", Model: ""}
	_, err = buildConfig(raw)
	if err == nil || !strings.Contains(err.Error(), "llm.model") {
		t.Fatalf("want llm.model error: %v", err)
	}

	raw = minimalValidRaw()
	raw.Agents[0].LLM = &struct {
		Provider string `yaml:"provider"`
		Model    string `yaml:"model"`
		APIKey   string `yaml:"api_key"`
		BaseURL  string `yaml:"base_url"`
	}{Provider: "openai", Model: "gpt-4o", APIKey: ""}
	_, err = buildConfig(raw)
	if err == nil || !strings.Contains(err.Error(), "llm.api_key") {
		t.Fatalf("want llm.api_key error: %v", err)
	}

	raw = minimalValidRaw()
	raw.Agents[0].LLM = &struct {
		Provider string `yaml:"provider"`
		Model    string `yaml:"model"`
		APIKey   string `yaml:"api_key"`
		BaseURL  string `yaml:"base_url"`
	}{Provider: "ollama", Model: "llama3", APIKey: "", BaseURL: "http://localhost:11434/v1"}
	cfg, err := buildConfig(raw)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Agents[0].LLM == nil || cfg.Agents[0].LLM.Provider != "ollama" {
		t.Fatalf("agent LLM: %+v", cfg.Agents[0].LLM)
	}
}

func TestBuildConfig_AgentTimeoutSuccess(t *testing.T) {
	raw := minimalValidRaw()
	raw.Agents[0].Timeout = "90s"
	cfg, err := buildConfig(raw)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Agents[0].Timeout != 90*time.Second {
		t.Errorf("Timeout = %v", cfg.Agents[0].Timeout)
	}
}

func TestBuildConfig_MemoryTTLOK(t *testing.T) {
	raw := minimalValidRaw()
	raw.Memory.TTL = "2h"
	cfg, err := buildConfig(raw)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Memory.TTL != 2*time.Hour {
		t.Errorf("Memory.TTL = %v", cfg.Memory.TTL)
	}
}

func TestBuildConfig_MemoryValidation(t *testing.T) {
	raw := minimalValidRaw()
	raw.Memory.Backend = "cassandra"
	_, err := buildConfig(raw)
	if err == nil || !strings.Contains(err.Error(), "memory.backend") {
		t.Fatalf("want backend error: %v", err)
	}

	raw = minimalValidRaw()
	raw.Memory.TTL = "not-duration"
	_, err = buildConfig(raw)
	if err == nil || !strings.Contains(err.Error(), "memory.ttl") {
		t.Fatalf("want ttl error: %v", err)
	}

	raw = minimalValidRaw()
	raw.Memory.Backend = "redis"
	raw.Memory.RedisURL = ""
	t.Setenv("REDIS_URL", "")
	defer t.Setenv("REDIS_URL", "")
	_, err = buildConfig(raw)
	if err == nil || !strings.Contains(err.Error(), "redis_url") {
		t.Fatalf("want redis_url error: %v", err)
	}

	mr := miniredis.RunT(t)
	raw.Memory.RedisURL = "redis://" + mr.Addr()
	cfg, err := buildConfig(raw)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Memory.RedisURL == "" {
		t.Fatal("expected redis URL")
	}

	raw = minimalValidRaw()
	raw.Memory.Backend = "redis"
	raw.Memory.RedisURL = ""
	t.Setenv("REDIS_URL", "redis://"+mr.Addr())
	cfg, err = buildConfig(raw)
	if err != nil {
		t.Fatalf("REDIS_URL fallback: %v", err)
	}
	if !strings.Contains(cfg.Memory.RedisURL, mr.Addr()) {
		t.Errorf("RedisURL = %q", cfg.Memory.RedisURL)
	}
}

func TestBuildMemoryStore(t *testing.T) {
	s, err := buildMemoryStore(MemoryConfig{Backend: "inmem"})
	if err != nil || s == nil {
		t.Fatalf("inmem: %v", err)
	}
	_ = s.Close()

	s, err = buildMemoryStore(MemoryConfig{Backend: ""})
	if err != nil || s == nil {
		t.Fatalf("empty backend as inmem: %v", err)
	}
	_ = s.Close()

	mr := miniredis.RunT(t)
	s, err = buildMemoryStore(MemoryConfig{Backend: "redis", RedisURL: "redis://" + mr.Addr(), TTL: time.Hour})
	if err != nil {
		t.Fatalf("redis: %v", err)
	}
	_ = s.Close()

	_, err = buildMemoryStore(MemoryConfig{Backend: "wat"})
	if err == nil || !strings.Contains(err.Error(), "unknown memory backend") {
		t.Fatalf("want unknown backend: %v", err)
	}
}

func TestEnvOr_NonEmptyFromEnvPrefix(t *testing.T) {
	t.Setenv("CFG_TEST_X", "from-env")
	if got := envOr("env:CFG_TEST_X", "fallback"); got != "from-env" {
		t.Errorf("got %q", got)
	}
}

func TestLoadEnvFile_GodotenvNonNotExistError(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("root can read mode-0 files")
	}
	dir := t.TempDir()
	configPath := filepath.Join(dir, "agents.yaml")
	envPath := filepath.Join(dir, "locked.env")
	if err := os.WriteFile(envPath, []byte("K=v"), 0000); err != nil {
		t.Fatalf("write: %v", err)
	}
	err := loadEnvFile("locked.env", configPath)
	if err == nil {
		t.Fatal("expected error loading unreadable env file")
	}
	_ = os.Chmod(envPath, 0600)
}

func TestLoadConfig_PeekSkipsEnvOnBadYAMLPrefix(t *testing.T) {
	// First unmarshal to peek fails; LoadConfig still errors on full parse.
	dir := t.TempDir()
	path := filepath.Join(dir, "agents.yaml")
	// Invalid YAML document
	if err := os.WriteFile(path, []byte("{"), 0600); err != nil {
		t.Fatal(err)
	}
	_, err := LoadConfig(path)
	if err == nil || !strings.Contains(err.Error(), "invalid YAML") {
		t.Fatalf("got %v", err)
	}
}
