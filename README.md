# cfgload

A Go package for universal configuration loading with support for multiple sources, formats, signature verification, and an in-process registry for global config access.

## Overview

`cfgload` provides a unified interface for loading configuration from files or URLs, parsing YAML/JSON, optionally verifying Ed25519 signatures, and registering configs for global access across your application.

## Features

- **Multiple Sources**: Load from `file://` paths or `https://` URLs
- **Format Auto-Detection**: Automatically detect YAML or JSON format
- **Signature Verification**: Ed25519 signature verification for secure configs
- **Environment Fallback**: Fall back to environment variables if URL fetch fails
- **Auto-Reload**: Periodically reload configuration in the background
- **Global Registry**: Store and retrieve configs by name or type
- **Type-Safe Access**: Generic functions for type-safe config retrieval
- **Validation**: Automatic validation via `Validate()` method

## Installation

```bash
go get github.com/bbmumford/cfgload
```

## Usage

### Basic Loading

```go
package main

import (
    "log"
    "github.com/bbmumford/cfgload"
)

type AppConfig struct {
    Name    string `yaml:"name" json:"name"`
    Port    int    `yaml:"port" json:"port"`
    Debug   bool   `yaml:"debug" json:"debug"`
}

func main() {
    var cfg AppConfig
    
    err := cfgload.Load(&cfg, cfgload.LoadOptions{
        URL: "file:///etc/myapp/config.yaml",
    })
    if err != nil {
        log.Fatalf("Failed to load config: %v", err)
    }
    
    log.Printf("Loaded config: %s on port %d", cfg.Name, cfg.Port)
}
```

### With Validation

Implement `Validate() error` on your config type for automatic validation:

```go
type AppConfig struct {
    Name    string `yaml:"name"`
    Timeout string `yaml:"timeout"`
}

func (c *AppConfig) Validate() error {
    if c.Name == "" {
        return fmt.Errorf("name is required")
    }
    if _, err := time.ParseDuration(c.Timeout); err != nil {
        return fmt.Errorf("invalid timeout: %w", err)
    }
    return nil
}

// Validation happens automatically during Load()
cfgload.Load(&cfg, cfgload.LoadOptions{
    URL: "file://config.yaml",
})
```

### With Registry

Register configs for global access:

```go
// Load and register
cfgload.Load(&cfg, cfgload.LoadOptions{
    URL:      "file://config.yaml",
    Register: true,
    Name:     "myapp",  // Optional; defaults to type name
})

// Access from anywhere
appCfg, ok := cfgload.GetAs[*AppConfig]("myapp")
if !ok {
    log.Fatal("config not found")
}

// Or panic if not found (for critical configs)
appCfg := cfgload.MustGetAs[*AppConfig]("myapp")
```

### With Signature Verification

For secure configuration delivery:

```go
cfgload.Load(&cfg, cfgload.LoadOptions{
    URL:             "https://config.example.com/myapp.yaml",
    AuthToken:       os.Getenv("CONFIG_TOKEN"),
    VerifySignature: true,
    PublicKey:       os.Getenv("CONFIG_PUBLIC_KEY"),  // Base64-encoded Ed25519 key
})
```

### With Environment Fallback

Fall back to environment variables if URL fetch fails:

```go
cfgload.Load(&cfg, cfgload.LoadOptions{
    URL:              "https://config.example.com/myapp.yaml",
    AllowEnvFallback: true,
    EnvLoader: func(target interface{}) error {
        cfg := target.(*AppConfig)
        cfg.Name = os.Getenv("APP_NAME")
        cfg.Port, _ = strconv.Atoi(os.Getenv("APP_PORT"))
        return nil
    },
})
```

### With Auto-Reload

Automatically reload configuration periodically:

```go
cfgload.Load(&cfg, cfgload.LoadOptions{
    URL:            "file://config.yaml",
    EnableReload:   true,
    ReloadInterval: 5 * time.Minute,
    Register:       true,
    Name:           "myapp",
    OnReload: func(target interface{}) {
        log.Println("Config reloaded successfully")
    },
    OnReloadError: func(err error) {
        log.Printf("Reload failed: %v", err)
    },
})

// Later, stop reloading
cfgload.StopReload(&cfg)
```

### Subscribe to Changes

Get notified when a config is updated:

```go
unsubscribe, err := cfgload.SubscribeAs[*AppConfig]("myapp", func(cfg *AppConfig) {
    log.Printf("Config updated: %s", cfg.Name)
    // React to config changes
})
if err != nil {
    log.Fatal(err)
}

// Later, unsubscribe
unsubscribe()
```

### Register Simple Values

Register non-config values for global access:

```go
// Register a simple value
cfgload.Register("platform.cookie.name", "session_id")

// Retrieve it
name, ok := cfgload.Get("platform.cookie.name")
```

## API Reference

### LoadOptions

```go
type LoadOptions struct {
    URL              string                       // Config source (file:// or https://)
    AuthToken        string                       // Authorization token for remote URLs
    Format           parse.Format                 // Format: yaml, json, or auto (default)
    VerifySignature  bool                         // Enable signature verification
    PublicKey        string                       // Base64-encoded Ed25519 public key
    AllowEnvFallback bool                         // Fall back to env vars on fetch failure
    EnvLoader        func(target interface{}) error // Populate target from env vars
    Validator        func(target interface{}) error // External validator (deprecated)
    EnableReload     bool                         // Enable periodic reloading
    ReloadInterval   time.Duration                // Reload frequency (default: 5m)
    OnReload         func(target interface{})     // Called after successful reload
    OnReloadError    func(err error)              // Called on reload failure
    Register         bool                         // Store in registry for global access
    Name             string                       // Registry key (default: type name)
}
```

### Loader Functions

```go
// Load configuration from URL into target struct
func Load(target interface{}, opts LoadOptions) error

// Load configuration, panic on error
func MustLoad(target interface{}, opts LoadOptions)

// Stop automatic reloading for a target
func StopReload(target interface{})
```

### Registry Functions

```go
// Get retrieves a config by name as interface{}
func Get(name string) (interface{}, bool)

// GetAs retrieves a config with type assertion
func GetAs[T any](name string) (T, bool)

// MustGetAs retrieves a config, panics if not found
func MustGetAs[T any](name string) T

// Exists checks if a config is registered
func Exists(name string) bool

// UpdateValue updates or registers a config
func UpdateValue(name string, value interface{}) error

// Register is a convenience wrapper for UpdateValue
func Register(name string, value interface{}) error

// Subscribe to config changes
func Subscribe(name string, fn func(interface{})) (func(), error)

// SubscribeAs is a type-safe subscribe
func SubscribeAs[T any](name string, fn func(T)) (func(), error)
```

## Supported Formats

### YAML

```yaml
name: myapp
port: 8080
database:
  host: localhost
  port: 5432
```

### JSON

```json
{
  "name": "myapp",
  "port": 8080,
  "database": {
    "host": "localhost",
    "port": 5432
  }
}
```

### Signed Config

For signature verification, add a `signature` field:

```yaml
name: myapp
port: 8080
signature: base64encodedSignature==
```

## URL Schemes

| Scheme | Example | Notes |
|--------|---------|-------|
| `file://` | `file:///etc/app/config.yaml` | Local file path |
| `https://` | `https://config.example.com/app.yaml` | Remote URL with optional auth |

## Best Practices

1. **Use `MustLoad` for critical configs**: Fail fast if essential config is missing
2. **Enable validation**: Implement `Validate()` on your config types
3. **Use the registry**: Register configs for global access across packages
4. **Subscribe to changes**: React to config updates in long-running services
5. **Sign production configs**: Use signature verification for secure config delivery
6. **Test with environment fallback**: Ensure your app works without remote config

## Testing

```go
func TestConfig(t *testing.T) {
    // Create temp config file
    tmp, _ := os.CreateTemp("", "config-*.yaml")
    tmp.WriteString("name: test\nport: 8080\n")
    tmp.Close()
    defer os.Remove(tmp.Name())
    
    var cfg AppConfig
    err := cfgload.Load(&cfg, cfgload.LoadOptions{
        URL: "file://" + tmp.Name(),
    })
    
    if err != nil {
        t.Fatalf("Load failed: %v", err)
    }
    if cfg.Name != "test" {
        t.Errorf("Expected name 'test', got '%s'", cfg.Name)
    }
}
```

## License

MIT
