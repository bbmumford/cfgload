package cfgload_test

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/bbmumford/cfgload"
)

// TestConfig is a sample config for testing
type TestConfig struct {
	Name    string `yaml:"name" json:"name"`
	Port    int    `yaml:"port" json:"port"`
	Debug   bool   `yaml:"debug" json:"debug"`
	Timeout string `yaml:"timeout" json:"timeout"`
}

// Validate implements the Validate() error interface
func (c *TestConfig) Validate() error {
	if c.Name == "" {
		return &ValidationError{Field: "name", Message: "is required"}
	}
	if c.Port <= 0 || c.Port > 65535 {
		return &ValidationError{Field: "port", Message: "must be between 1 and 65535"}
	}
	return nil
}

type ValidationError struct {
	Field   string
	Message string
}

func (e *ValidationError) Error() string {
	return e.Field + " " + e.Message
}

func TestLoadFromFile(t *testing.T) {
	// Create temp config file
	content := `name: test-app
port: 8080
debug: true
timeout: 30s
`
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.yaml")
	if err := os.WriteFile(configPath, []byte(content), 0644); err != nil {
		t.Fatalf("failed to write test config: %v", err)
	}

	var cfg TestConfig
	err := cfgload.Load(&cfg, cfgload.LoadOptions{
		URL: "file://" + configPath,
	})
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}

	if cfg.Name != "test-app" {
		t.Errorf("expected name 'test-app', got '%s'", cfg.Name)
	}
	if cfg.Port != 8080 {
		t.Errorf("expected port 8080, got %d", cfg.Port)
	}
	if !cfg.Debug {
		t.Error("expected debug to be true")
	}
	if cfg.Timeout != "30s" {
		t.Errorf("expected timeout '30s', got '%s'", cfg.Timeout)
	}
}

func TestLoadWithValidation(t *testing.T) {
	// Test config that fails validation (missing name)
	content := `port: 8080
debug: true
`
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "invalid.yaml")
	if err := os.WriteFile(configPath, []byte(content), 0644); err != nil {
		t.Fatalf("failed to write test config: %v", err)
	}

	var cfg TestConfig
	err := cfgload.Load(&cfg, cfgload.LoadOptions{
		URL: "file://" + configPath,
	})
	if err == nil {
		t.Error("expected validation error, got nil")
	}
}

func TestLoadWithInvalidPort(t *testing.T) {
	content := `name: test-app
port: 99999
`
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "invalid-port.yaml")
	if err := os.WriteFile(configPath, []byte(content), 0644); err != nil {
		t.Fatalf("failed to write test config: %v", err)
	}

	var cfg TestConfig
	err := cfgload.Load(&cfg, cfgload.LoadOptions{
		URL: "file://" + configPath,
	})
	if err == nil {
		t.Error("expected validation error for invalid port, got nil")
	}
}

func TestLoadNilTarget(t *testing.T) {
	err := cfgload.Load(nil, cfgload.LoadOptions{
		URL: "file:///some/path.yaml",
	})
	if err == nil {
		t.Error("expected error for nil target, got nil")
	}
}

func TestLoadMissingURL(t *testing.T) {
	var cfg TestConfig
	err := cfgload.Load(&cfg, cfgload.LoadOptions{})
	if err == nil {
		t.Error("expected error for missing URL, got nil")
	}
}

func TestLoadEnvFallback(t *testing.T) {
	var cfg TestConfig
	err := cfgload.Load(&cfg, cfgload.LoadOptions{
		URL:              "", // Empty URL
		AllowEnvFallback: true,
		EnvLoader: func(target interface{}) error {
			c := target.(*TestConfig)
			c.Name = "env-app"
			c.Port = 9090
			c.Debug = false
			return nil
		},
	})
	if err != nil {
		t.Fatalf("Load with env fallback failed: %v", err)
	}

	if cfg.Name != "env-app" {
		t.Errorf("expected name 'env-app', got '%s'", cfg.Name)
	}
	if cfg.Port != 9090 {
		t.Errorf("expected port 9090, got %d", cfg.Port)
	}
}

func TestLoadJSONFormat(t *testing.T) {
	content := `{"name": "json-app", "port": 3000, "debug": false, "timeout": "1m"}`
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.json")
	if err := os.WriteFile(configPath, []byte(content), 0644); err != nil {
		t.Fatalf("failed to write test config: %v", err)
	}

	var cfg TestConfig
	err := cfgload.Load(&cfg, cfgload.LoadOptions{
		URL: "file://" + configPath,
	})
	if err != nil {
		t.Fatalf("Load JSON failed: %v", err)
	}

	if cfg.Name != "json-app" {
		t.Errorf("expected name 'json-app', got '%s'", cfg.Name)
	}
	if cfg.Port != 3000 {
		t.Errorf("expected port 3000, got %d", cfg.Port)
	}
}

func TestMustLoad(t *testing.T) {
	content := `name: must-load-app
port: 8080
`
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.yaml")
	if err := os.WriteFile(configPath, []byte(content), 0644); err != nil {
		t.Fatalf("failed to write test config: %v", err)
	}

	var cfg TestConfig
	// Should not panic
	cfgload.MustLoad(&cfg, cfgload.LoadOptions{
		URL: "file://" + configPath,
	})

	if cfg.Name != "must-load-app" {
		t.Errorf("expected name 'must-load-app', got '%s'", cfg.Name)
	}
}

func TestMustLoadPanics(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Error("expected MustLoad to panic on error")
		}
	}()

	var cfg TestConfig
	cfgload.MustLoad(&cfg, cfgload.LoadOptions{
		URL: "file:///nonexistent/path/config.yaml",
	})
}

// --- Registry Tests ---

func TestRegistryUpdateAndGet(t *testing.T) {
	// Reset registry state (if possible) or use unique names
	name := "test-config-" + time.Now().Format("20060102150405")

	cfg := &TestConfig{
		Name:  "registry-app",
		Port:  5000,
		Debug: true,
	}

	err := cfgload.UpdateValue(name, cfg)
	if err != nil {
		t.Fatalf("UpdateValue failed: %v", err)
	}

	retrieved, ok := cfgload.Get(name)
	if !ok {
		t.Fatal("expected config to be found in registry")
	}

	retrievedCfg, ok := retrieved.(*TestConfig)
	if !ok {
		t.Fatalf("expected *TestConfig, got %T", retrieved)
	}

	if retrievedCfg.Name != "registry-app" {
		t.Errorf("expected name 'registry-app', got '%s'", retrievedCfg.Name)
	}
}

func TestRegistryGetAs(t *testing.T) {
	name := "test-config-getas-" + time.Now().Format("20060102150405")

	cfg := &TestConfig{
		Name:  "getas-app",
		Port:  6000,
		Debug: false,
	}

	if err := cfgload.UpdateValue(name, cfg); err != nil {
		t.Fatalf("UpdateValue failed: %v", err)
	}

	retrievedCfg, ok := cfgload.GetAs[*TestConfig](name)
	if !ok {
		t.Fatal("expected config to be found via GetAs")
	}

	if retrievedCfg.Name != "getas-app" {
		t.Errorf("expected name 'getas-app', got '%s'", retrievedCfg.Name)
	}
}

func TestRegistryMustGetAs(t *testing.T) {
	name := "test-config-mustgetas-" + time.Now().Format("20060102150405")

	cfg := &TestConfig{
		Name: "mustgetas-app",
		Port: 7000,
	}

	if err := cfgload.UpdateValue(name, cfg); err != nil {
		t.Fatalf("UpdateValue failed: %v", err)
	}

	// Should not panic
	retrievedCfg := cfgload.MustGetAs[*TestConfig](name)
	if retrievedCfg.Name != "mustgetas-app" {
		t.Errorf("expected name 'mustgetas-app', got '%s'", retrievedCfg.Name)
	}
}

func TestRegistryMustGetAsPanics(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Error("expected MustGetAs to panic for missing key")
		}
	}()

	cfgload.MustGetAs[*TestConfig]("nonexistent-key")
}

func TestRegistryExists(t *testing.T) {
	name := "test-config-exists-" + time.Now().Format("20060102150405")

	if cfgload.Exists(name) {
		t.Error("expected config to not exist before registration")
	}

	cfg := &TestConfig{Name: "exists-app", Port: 8000}
	if err := cfgload.UpdateValue(name, cfg); err != nil {
		t.Fatalf("UpdateValue failed: %v", err)
	}

	if !cfgload.Exists(name) {
		t.Error("expected config to exist after registration")
	}
}

func TestRegistryTypeMismatch(t *testing.T) {
	name := "test-config-typemismatch-" + time.Now().Format("20060102150405")

	cfg := &TestConfig{Name: "first", Port: 1000}
	if err := cfgload.UpdateValue(name, cfg); err != nil {
		t.Fatalf("First UpdateValue failed: %v", err)
	}

	// Try to update with different type
	type OtherConfig struct {
		Value string
	}
	other := &OtherConfig{Value: "test"}
	err := cfgload.UpdateValue(name, other)
	if err == nil {
		t.Error("expected type mismatch error, got nil")
	}
}

func TestRegistrySubscribe(t *testing.T) {
	name := "test-config-subscribe-" + time.Now().Format("20060102150405")

	received := make(chan *TestConfig, 1)
	unsubscribe, err := cfgload.SubscribeAs(name, func(cfg *TestConfig) {
		select {
		case received <- cfg:
		default:
		}
	})
	if err != nil {
		t.Fatalf("Subscribe failed: %v", err)
	}
	defer unsubscribe()

	// Update value
	cfg := &TestConfig{Name: "subscribed-app", Port: 9000}
	if err := cfgload.UpdateValue(name, cfg); err != nil {
		t.Fatalf("UpdateValue failed: %v", err)
	}

	// Wait for subscription notification
	select {
	case receivedCfg := <-received:
		if receivedCfg.Name != "subscribed-app" {
			t.Errorf("expected name 'subscribed-app', got '%s'", receivedCfg.Name)
		}
	case <-time.After(time.Second):
		t.Error("subscription notification timed out")
	}
}

func TestRegistryRegister(t *testing.T) {
	name := "test-primitive-" + time.Now().Format("20060102150405")

	// Register primitive value
	if err := cfgload.Register(name, "my-cookie-name"); err != nil {
		t.Fatalf("Register failed: %v", err)
	}

	val, ok := cfgload.GetAs[string](name)
	if !ok {
		t.Fatal("expected value to be found")
	}
	if val != "my-cookie-name" {
		t.Errorf("expected 'my-cookie-name', got '%s'", val)
	}
}

func TestLoadWithRegistry(t *testing.T) {
	content := `name: registered-app
port: 4000
debug: true
`
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.yaml")
	if err := os.WriteFile(configPath, []byte(content), 0644); err != nil {
		t.Fatalf("failed to write test config: %v", err)
	}

	name := "test-load-registry-" + time.Now().Format("20060102150405")

	var cfg TestConfig
	err := cfgload.Load(&cfg, cfgload.LoadOptions{
		URL:      "file://" + configPath,
		Register: true,
		Name:     name,
	})
	if err != nil {
		t.Fatalf("Load with registry failed: %v", err)
	}

	// Verify it's in the registry
	registeredCfg, ok := cfgload.GetAs[*TestConfig](name)
	if !ok {
		t.Fatal("expected config to be registered")
	}

	if registeredCfg.Name != "registered-app" {
		t.Errorf("expected name 'registered-app', got '%s'", registeredCfg.Name)
	}
}
