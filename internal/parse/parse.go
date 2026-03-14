package parse

import (
	"encoding/json"
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"
)

// Format represents the configuration file format
type Format string

const (
	FormatYAML Format = "yaml"
	FormatJSON Format = "json"
	FormatAuto Format = "auto"
)

// Parse parses configuration data into the target struct
// Supports auto-detection based on content or explicit format specification
func Parse(data []byte, target interface{}, format Format) error {
	if len(data) == 0 {
		return fmt.Errorf("config data is empty")
	}

	// Auto-detect format if not specified
	if format == FormatAuto {
		format = detectFormat(data)
	}

	switch format {
	case FormatYAML:
		return parseYAML(data, target)
	case FormatJSON:
		return parseJSON(data, target)
	default:
		return fmt.Errorf("unsupported format: %s", format)
	}
}

// detectFormat attempts to detect the configuration format
func detectFormat(data []byte) Format {
	// Trim leading whitespace
	trimmed := strings.TrimSpace(string(data))

	// JSON starts with { or [
	if strings.HasPrefix(trimmed, "{") || strings.HasPrefix(trimmed, "[") {
		return FormatJSON
	}

	// Assume YAML for everything else
	// YAML is more permissive and can often parse JSON too
	return FormatYAML
}

// parseYAML parses YAML data into the target
func parseYAML(data []byte, target interface{}) error {
	if err := yaml.Unmarshal(data, target); err != nil {
		return fmt.Errorf("failed to parse YAML: %w", err)
	}
	return nil
}

// parseJSON parses JSON data into the target
func parseJSON(data []byte, target interface{}) error {
	if err := json.Unmarshal(data, target); err != nil {
		return fmt.Errorf("failed to parse JSON: %w", err)
	}
	return nil
}
