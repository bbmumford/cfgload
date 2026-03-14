package main

import (
	"fmt"
	"log"
	"os"
	"time"

	"github.com/bbmumford/cfgload"
)

// AppConfig is a simple example config type.
type AppConfig struct {
	Name    string `yaml:"name" json:"name"`
	Timeout string `yaml:"timeout" json:"timeout"`
}

// Validate enforces required fields and parses duration to ensure it is valid.
func (c *AppConfig) Validate() error {
	if c.Name == "" {
		return fmt.Errorf("name is required")
	}
	if c.Timeout == "" {
		c.Timeout = "30s"
	}
	if _, err := time.ParseDuration(c.Timeout); err != nil {
		return fmt.Errorf("invalid timeout: %w", err)
	}
	return nil
}

func main() {
	// Write a temporary YAML config for demonstration purposes.
	tmp, err := os.CreateTemp("", "example-config-*.yaml")
	if err != nil {
		log.Fatalf("failed to create temp file: %v", err)
	}
	defer os.Remove(tmp.Name())

	if _, err := tmp.WriteString("name: example-app\ntimeout: 45s\n"); err != nil {
		log.Fatalf("failed to write temp config: %v", err)
	}
	tmp.Close()

	// Load the config from the file, validate it, and publish to registry.
	var cfg AppConfig
	if err := cfgload.Load(&cfg, cfgload.LoadOptions{
		URL:      "file://" + tmp.Name(),
		Register: true,
		Name:     "app",
	}); err != nil {
		log.Fatalf("load failed: %v", err)
	}

	// Access the loaded config via the registry.
	snap := cfgload.MustGetAs[*AppConfig]("app")
	fmt.Printf("Loaded app %s with timeout %s\n", snap.Name, snap.Timeout)
}
