package cfgload

import (
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/bbmumford/cfgload/internal/fetch"
	"github.com/bbmumford/cfgload/internal/parse"
	"github.com/bbmumford/cfgload/internal/verify"
)

// LoadOptions configures the Load operation
type LoadOptions struct {
	// URL is the configuration source (file:// or https://)
	URL string

	// AuthToken is the authorization token for remote URLs (optional for file://)
	AuthToken string

	// Format specifies the config format (yaml, json, auto)
	// If empty or "auto", format is auto-detected
	Format parse.Format

	// VerifySignature enables Ed25519 signature verification
	VerifySignature bool

	// PublicKey is the base64-encoded Ed25519 public key for signature verification
	// Required if VerifySignature is true
	PublicKey string

	// AllowEnvFallback enables environment variable fallback if URL fetch fails
	AllowEnvFallback bool

	// EnvLoader is called if AllowEnvFallback is true and URL fetch fails
	// The loader should populate the target from environment variables
	EnvLoader func(target interface{}) error

	// Validator is called after parsing to validate the configuration
	// If nil, no validation is performed (use type's Validate() method manually)
	Validator func(target interface{}) error

	// EnableReload enables automatic periodic reloading
	EnableReload bool

	// ReloadInterval is how often to reload (default: 5 minutes)
	ReloadInterval time.Duration

	// OnReload is called after each successful reload with the new config
	OnReload func(target interface{})

	// OnReloadError is called if reload fails
	OnReloadError func(err error)

	// Register controls whether the loaded configuration is stored in the
	// in-process registry for global access by name or type. When true, the
	// configuration is snapshotted and made available via GetAs/Subscribe.
	// If Name is empty, a default key based on the type is used.
	Register bool

	// Name is an optional registry key. If empty and Register is true,
	// a default key derived from the target's type is used.
	Name string
}

// reloadState tracks reload goroutines
type reloadState struct {
	stopChan chan struct{}
	running  bool
	mu       sync.Mutex
}

var (
	reloadStates = make(map[interface{}]*reloadState)
	reloadMu     sync.Mutex
)

// Load is the universal config loader
// It fetches, parses, optionally verifies, and validates configuration
//
// Example:
//
//	var cfg platform.Config
//	err := cfgload.Load(&cfg, cfgload.LoadOptions{
//	    URL:             "file:///etc/hstles/config-platform.yaml",
//	    VerifySignature: true,
//	    PublicKey:       "base64encodedkey...",
//	})
func Load(target interface{}, opts LoadOptions) error {
	if target == nil {
		return fmt.Errorf("target cannot be nil")
	}

	// Set default format
	if opts.Format == "" {
		opts.Format = parse.FormatAuto
	}

	var data []byte
	var err error

	// Fetch config data if URL is provided
	if opts.URL != "" {
		data, err = fetch.Fetch(opts.URL, opts.AuthToken)
	} else if !opts.AllowEnvFallback {
		return fmt.Errorf("URL is required")
	} else {
		// URL is empty but fallback is allowed, treat as fetch error
		err = fmt.Errorf("URL is empty")
	}

	if err != nil {
		// Try environment fallback if enabled
		if opts.AllowEnvFallback {
			if opts.URL != "" {
				log.Printf("Failed to fetch config from %s: %v, falling back to environment variables", opts.URL, err)
			}

			// If EnvLoader is provided, use it
			if opts.EnvLoader != nil {
				if envErr := opts.EnvLoader(target); envErr != nil {
					return fmt.Errorf("fetch failed and env fallback failed: fetch=%v, env=%v", err, envErr)
				}
			}

			// Proceed to validation (which may also handle env fallback)
			if opts.Validator != nil {
				if valErr := opts.Validator(target); valErr != nil {
					return fmt.Errorf("env config validation failed: %w", valErr)
				}
			}

			// Also check for self-validation
			if validator, ok := target.(interface{ Validate() error }); ok {
				if err := validator.Validate(); err != nil {
					return fmt.Errorf("env config validation failed: %w", err)
				}
			}

			// Register in registry if requested (after successful load/validation)
			if opts.Register {
				name := opts.Name
				if name == "" {
					name = keyFor(target, "")
				}
				if err := UpdateValue(name, target); err != nil {
					return fmt.Errorf("failed to register config '%s': %w", name, err)
				}
			}

			return nil
		}
		return fmt.Errorf("failed to fetch config: %w", err)
	}

	// Verify signature if enabled
	if opts.VerifySignature {
		if opts.PublicKey == "" {
			return fmt.Errorf("publicKey is required when verifySignature is enabled")
		}
		if err := verify.VerifySignature(data, opts.PublicKey); err != nil {
			return fmt.Errorf("signature verification failed: %w", err)
		}
	}

	// Parse config data
	if err := parse.Parse(data, target, opts.Format); err != nil {
		return fmt.Errorf("failed to parse config: %w", err)
	}

	// Automatically call Validate() if the type implements it
	if validator, ok := target.(interface{ Validate() error }); ok {
		if err := validator.Validate(); err != nil {
			return fmt.Errorf("validation failed: %w", err)
		}
	}

	// Legacy: Validate if external validator provided (deprecated, use type's Validate() method)
	if opts.Validator != nil {
		if err := opts.Validator(target); err != nil {
			return fmt.Errorf("validation failed: %w", err)
		}
	}

	// Start reload goroutine if enabled
	if opts.EnableReload {
		startReload(target, opts)
	}

	// Register in registry if requested (after successful load/validation)
	if opts.Register {
		name := opts.Name
		if name == "" {
			name = keyFor(target, "")
		}
		if err := UpdateValue(name, target); err != nil {
			return fmt.Errorf("failed to register config '%s': %w", name, err)
		}
	}

	return nil
}

// MustLoad is like Load but panics on error
// Useful for initialization where config is critical
func MustLoad(target interface{}, opts LoadOptions) {
	if err := Load(target, opts); err != nil {
		panic(fmt.Sprintf("failed to load config: %v", err))
	}
}

// startReload starts a background goroutine to periodically reload config
func startReload(target interface{}, opts LoadOptions) {
	reloadMu.Lock()
	defer reloadMu.Unlock()

	// Check if already reloading
	state, exists := reloadStates[target]
	if exists {
		state.mu.Lock()
		if state.running {
			state.mu.Unlock()
			return // Already running
		}
		state.mu.Unlock()
	} else {
		state = &reloadState{
			stopChan: make(chan struct{}),
		}
		reloadStates[target] = state
	}

	// Set default reload interval
	interval := opts.ReloadInterval
	if interval == 0 {
		interval = 5 * time.Minute
	}

	state.mu.Lock()
	state.running = true
	state.mu.Unlock()

	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()

		for {
			select {
			case <-ticker.C:
				// Reload config
				if err := Load(target, LoadOptions{
					URL:             opts.URL,
					AuthToken:       opts.AuthToken,
					Format:          opts.Format,
					VerifySignature: opts.VerifySignature,
					PublicKey:       opts.PublicKey,
					Validator:       opts.Validator,
					// registry settings
					Register: opts.Register,
					Name:     opts.Name,
					// Don't enable reload in the reload call
					EnableReload: false,
				}); err != nil {
					if opts.OnReloadError != nil {
						opts.OnReloadError(err)
					} else {
						log.Printf("Config reload failed: %v", err)
					}
				} else {
					if opts.OnReload != nil {
						opts.OnReload(target)
					}
				}
			case <-state.stopChan:
				state.mu.Lock()
				state.running = false
				state.mu.Unlock()
				return
			}
		}
	}()
}

// StopReload stops the reload goroutine for a target
func StopReload(target interface{}) {
	reloadMu.Lock()
	defer reloadMu.Unlock()

	state, exists := reloadStates[target]
	if !exists {
		return
	}

	state.mu.Lock()
	if state.running {
		close(state.stopChan)
		state.running = false
	}
	state.mu.Unlock()
}
