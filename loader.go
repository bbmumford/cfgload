package cfgload

import (
	"fmt"
	"log"
	"reflect"
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
		reread, rereadIsJSON, err := verify.Reread(data, opts.PublicKey)
		if err != nil {
			return fmt.Errorf("signature verification failed: %w", err)
		}
		if err := loadsAsSigned(data, opts.Format, reread, rereadIsJSON, target); err != nil {
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

// loadsAsSigned returns an error unless the document fills a target with the
// value the signature check's own reading of it fills it with. Load calls it
// after the signature has verified and before it fills the caller's target.
//
// verify.Reread checks the signature over the document decoded into a map
// and marshalled again, and returns that form with the signature field left
// in. The form keeps every key and value and drops how a value was written,
// so an unquoted 1.10 is the number 1.1 in it and 0123 is 83. Load fills the
// target from the document's own bytes, where a text field takes the
// characters as written. A document can therefore be rewritten, under one
// signature, into any other spelling of the same number, and a text field
// then loads a different string. Filling one fresh value of the target's type
// from each form and comparing them refuses every such document, altered or
// not, and leaves alone a value that loads the same from both, such as a
// number written in hexadecimal into a number field. The reread form carries
// the signature field because targets such as the endpoint spine's own config
// type declare it, and would otherwise differ on that field alone.
//
// Both values start empty, so what the caller's target already holds takes no
// part in the comparison. A target that is not a pointer, or a document that
// does not parse, is left for parse.Parse below to refuse.
func loadsAsSigned(data []byte, format parse.Format, reread []byte, rereadIsJSON bool, target interface{}) error {
	t := reflect.TypeOf(target)
	if t == nil || t.Kind() != reflect.Ptr {
		return nil
	}
	fromDocument := reflect.New(t.Elem())
	if err := parse.Parse(data, fromDocument.Interface(), format); err != nil {
		return nil
	}
	rereadFormat := parse.FormatYAML
	if rereadIsJSON {
		rereadFormat = parse.FormatJSON
	}
	fromReread := reflect.New(t.Elem())
	if err := parse.Parse(reread, fromReread.Interface(), rereadFormat); err != nil {
		return fmt.Errorf("the form of the document its signature was checked over does not load: %w", err)
	}
	if reflect.DeepEqual(fromDocument.Interface(), fromReread.Interface()) {
		return nil
	}
	where := firstDifference(fromDocument.Elem(), fromReread.Elem(), "")
	return fmt.Errorf("the document loads a value its signature does not determine%s: a text value that reads as a number or a date must be written in quotes", where)
}

// firstDifference names the first place two values of one type differ, as
// " (at Server.Host: "0123" in the document, "83" in the signed form)", for
// loadsAsSigned's error. It returns "" when it finds no place it can name.
func firstDifference(a, b reflect.Value, path string) string {
	if !a.IsValid() || !b.IsValid() || a.Type() != b.Type() {
		return ""
	}
	switch a.Kind() {
	case reflect.Ptr, reflect.Interface:
		if a.IsNil() || b.IsNil() {
			if a.IsNil() != b.IsNil() {
				return fmt.Sprintf(" (at %s: set in one reading and absent in the other)", path)
			}
			return ""
		}
		return firstDifference(a.Elem(), b.Elem(), path)
	case reflect.Struct:
		for i := 0; i < a.NumField(); i++ {
			if !a.Type().Field(i).IsExported() {
				continue
			}
			name := a.Type().Field(i).Name
			if path != "" {
				name = path + "." + name
			}
			if d := firstDifference(a.Field(i), b.Field(i), name); d != "" {
				return d
			}
		}
		return ""
	case reflect.Slice, reflect.Array:
		if a.Len() != b.Len() {
			return fmt.Sprintf(" (at %s: %d elements in the document, %d in the signed form)", path, a.Len(), b.Len())
		}
		for i := 0; i < a.Len(); i++ {
			if d := firstDifference(a.Index(i), b.Index(i), fmt.Sprintf("%s[%d]", path, i)); d != "" {
				return d
			}
		}
		return ""
	case reflect.Map:
		for _, k := range a.MapKeys() {
			bv := b.MapIndex(k)
			if !bv.IsValid() {
				return fmt.Sprintf(" (at %s: key %v is in the document and not in the signed form)", path, k)
			}
			if d := firstDifference(a.MapIndex(k), bv, fmt.Sprintf("%s[%v]", path, k)); d != "" {
				return d
			}
		}
		if a.Len() != b.Len() {
			return fmt.Sprintf(" (at %s: the signed form holds a key the document does not)", path)
		}
		return ""
	}
	if !a.CanInterface() || reflect.DeepEqual(a.Interface(), b.Interface()) {
		return ""
	}
	return fmt.Sprintf(" (at %s: %q in the document, %q in the signed form)", path, fmt.Sprint(a.Interface()), fmt.Sprint(b.Interface()))
}
