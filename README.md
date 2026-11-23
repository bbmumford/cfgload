# cfgload

cfgload is a Go package for configuration loading with support for multiple sources, formats, signature verification, and an in-process registry for global config access.

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

### With a Judge

A signed document may carry a header inside its signed bytes, under the
top-level key `header` (`HeaderKey`): `subject`, `version` and `expiresAt`
(`Header`). cfgload decides nothing from it. `LoadOptions.Judge` is handed a
`Verified` for each document after its signature verified and its value
validated, before the target is set: the header read from the form the
signature covers (`Stamped` false when there is none), the public key it
verified under, and the decoded value. An error refuses the document: `Load`
returns it with the target unchanged, and a reload keeps the value in force and
reports it to `OnReloadError`. A Judge on a load with `VerifySignature` off is
refused. `ParseHeader` reads a document's header without verifying it, for a
signer that stamps the next version of a document it re-signs.

```go
cfgload.Load(&cfg, cfgload.LoadOptions{
    URL:             "https://config.example.com/mesh.yaml",
    VerifySignature: true,
    PublicKey:       os.Getenv("CONFIG_PUBLIC_KEY"),
    Judge:           judge.Judge, // e.g. a trust-chain verifier's per-document judge
})
```

### Load Is All Or Nothing

`Load` decodes into a copy of the target, fills it from the environment,
validates it and hands it to the Judge, and only then sets the target: a
document refused at any step leaves the target as the caller had it. A first
load keeps the caller's values for fields the document leaves out, so a caller
can fill defaults, or a document to layer this one over, before it loads.

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

### Per-Setting Environment Fallback

A string field with an `env` tag is read from its variable through
[envreq](https://github.com/bbmumford/envreq) when the loaded document leaves it
empty. The document wins: a field it fills is kept and its variable is not read.
`sensitive` keeps the value out of envreq's reports; `required` fails the load
when neither source supplies the setting, and the error names the setting's
path and its variable, never a value.

```go
type DBConfig struct {
    URL   string `yaml:"url" env:"DB_URL,required"`
    Token string `yaml:"token" env:"DB_TOKEN,sensitive,required"`
}
```

The fill runs after the document parses (or after `EnvLoader` when the fetch
fails) and before `Validate()`. `LoadOptions.EnvLookup` replaces the envreq read,
and `FillFromEnv` and `EnvFields` apply and list the tags without loading a
document. Only string fields take a tag.

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

Each reload decodes the re-read into an empty value of the target's type, fills
it from the environment, validates it and judges it, and only on success sets
the target, registers it and hands `OnReload` that value (a pointer the loader
never writes again); a field the re-read document leaves out reads empty, and a
refused re-read leaves the value in force. The reload starts even when the
first load fails: it then loads every interval, from the caller's value as the
first load did, until one load succeeds, and hands that first value to
`OnReload`. `StopReload` ends the watch, retry included, and removes its state,
so the same target can be watched again.

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
    EnvLookup        EnvLookup                    // Reads env-tagged settings (default: envreq)
    Validator        func(target interface{}) error // External validator (deprecated)
    Judge            func(Verified) error         // Decides each verified document before it is committed
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

// Fill every empty env-tagged string field, refusing a missing required one
func FillFromEnv(target interface{}, lookup EnvLookup) error

// List the settings a target's env tags name
func EnvFields(target interface{}) ([]EnvVar, error)
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

## Dependencies

- `github.com/bbmumford/envreq`: the environment fallback declares each
  variable it reads through envreq.
- `gopkg.in/yaml.v3`. Signature checks use the standard library's Ed25519
  (`internal/verify`).

## Used by

Importers in the ORBTR estate:

- `github.com/ORBTR/Library/endpoint`, `brand`, `keyvault/enccfg`,
  `keyvault/kvboot` and `trustchain/signedconfig`: every endpoint's
  configuration and the signed configuration documents.
- `github.com/bbmumford/loom/node` and `node/capabilities`, and
  `github.com/bbmumford/flyio`.
- `github.com/ORBTR/api.orbtr.ai`, `github.com/ORBTR/help.orbtr.io`,
  `github.com/ORBTR/node.orbtr.io`, `github.com/ORBTR/orbtr.io`,
  `github.com/ORBTR/tools/keygen` and
  `github.com/ORBTR/constellation/domain/identity/trust/partitionkeys`.
- Tests only: packages of `github.com/ORBTR/cortex`,
  `github.com/ORBTR/constellation` and `github.com/ORBTR/Library`.

## Platforms and build

Pure Go, no cgo and no build tags. The module's `go` directive is 1.23.2.

## Licence

MIT. Copyright (c) 2026 Brandon Miller-Mumford. The full text is in
[LICENSE](LICENSE).

## Contributing

[AGENTS.md](AGENTS.md) holds the repository's two rules, for people and coding
agents alike: no temporary documents in the repository, and conventional commit
messages that stand on their own. [CLAUDE.md](CLAUDE.md) imports it for Claude
Code.
