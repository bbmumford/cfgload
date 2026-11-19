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

	// EnvLookup reads the variable a field's env tag names when the loaded
	// value leaves that field empty; see FillFromEnv. If nil, each variable is
	// read through envreq as an optional requirement.
	EnvLookup EnvLookup

	// Validator is called after parsing to validate the configuration
	// If nil, no validation is performed (use type's Validate() method manually)
	Validator func(target interface{}) error

	// Judge is handed each verified document before Load commits it (Verified:
	// its header, the key its signature verified under and the decoded,
	// validated value). An error refuses the document: Load returns it with the
	// caller's target as it was, and a reload keeps the value in force and
	// reports it to OnReloadError. Every reload calls it again. Load refuses a
	// Judge on a load whose signature it does not verify, since a header no
	// signature covers can be written by anyone who serves the document; a load
	// that falls back to the environment has no document and calls no Judge.
	Judge func(Verified) error

	// EnableReload enables automatic periodic reloading. The reload starts even
	// when the first load fails: it then loads every ReloadInterval until one
	// load succeeds, and goes on reloading from there, until StopReload.
	EnableReload bool

	// ReloadInterval is how often to reload (default: 5 minutes)
	ReloadInterval time.Duration

	// OnReload is called with each value a reload commits: every reload that
	// loads after a first Load that succeeded, and also the first load that
	// succeeds after a first Load that failed, since that caller has been handed
	// no value. The value is a pointer of the target's type that the loader
	// never writes again; the target holds an equal value.
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

// reloadState is one target's watch: stop ends it, and mu orders a reload's
// commit into the target against StopReload, so that nothing is written into
// the target once StopReload has returned.
type reloadState struct {
	stop    chan struct{}
	mu      sync.Mutex
	stopped bool
}

// reloadStates holds the watch of each target that has one; StopReload removes
// the target's entry, so a stopped target holds no state and can be watched
// again.
var (
	reloadStates = make(map[interface{}]*reloadState)
	reloadMu     sync.Mutex
)

// Load is the universal config loader
// It fetches, parses, optionally verifies, and validates configuration
//
// Load decodes the document into a copy of what target holds, fills it from
// the environment, validates it and hands it to the Judge, and only then sets
// target to it, so a document any step refuses leaves target as the caller had
// it. Fields the document leaves out keep the caller's values: a caller may
// fill target with defaults, or a document to layer this one over, first.
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
	t := reflect.ValueOf(target)
	if t.Kind() != reflect.Ptr || t.IsNil() {
		return fmt.Errorf("target must be a non-nil pointer, got %T", target)
	}
	if opts.Judge != nil && !opts.VerifySignature {
		return fmt.Errorf("a judged load verifies the document's signature: VerifySignature is off")
	}

	// The watch is reserved before the first load so a StopReload made while
	// it runs ends the reload it would start.
	var state *reloadState
	if opts.EnableReload {
		state = reserveReload(target)
	}

	value, err := loadValue(target, opts, false, true)
	if err != nil {
		if state != nil {
			if opts.URL == "" {
				// Nothing to load again: the watch is released, not retried.
				StopReload(target)
			} else {
				startReload(target, state, opts, true)
			}
		}
		return err
	}
	t.Elem().Set(value.Elem())

	// Register in registry if requested (after successful load/validation)
	if opts.Register {
		if err := UpdateValue(registryName(target, opts), target); err != nil {
			if state != nil {
				startReload(target, state, opts, false)
			}
			return fmt.Errorf("failed to register config '%s': %w", registryName(target, opts), err)
		}
	}

	if state != nil {
		startReload(target, state, opts, false)
	}
	return nil
}

// registryName is the registry key a load registers under.
func registryName(target interface{}, opts LoadOptions) string {
	if opts.Name != "" {
		return opts.Name
	}
	return keyFor(target, "")
}

// loadValue loads one document and returns it as a new pointer of target's
// type, without touching target. fresh starts the value empty, as a reload
// does, so a field the document leaves out reads empty rather than as the last
// load left it; otherwise it starts as a deep copy of what target holds, as a
// first load does, so the caller's defaults and layers stay under the
// document. allowFallback lets a failed fetch fall back to the environment.
func loadValue(target interface{}, opts LoadOptions, fresh, allowFallback bool) (reflect.Value, error) {
	elem := reflect.ValueOf(target).Elem()
	value := reflect.New(elem.Type())
	if !fresh {
		value.Elem().Set(deepCopy(elem))
	}
	v := value.Interface()

	// Set default format
	format := opts.Format
	if format == "" {
		format = parse.FormatAuto
	}

	var data []byte
	var err error

	// Fetch config data if URL is provided
	if opts.URL != "" {
		data, err = fetch.Fetch(opts.URL, opts.AuthToken)
	} else if !(allowFallback && opts.AllowEnvFallback) {
		return reflect.Value{}, fmt.Errorf("URL is required")
	} else {
		// URL is empty but fallback is allowed, treat as fetch error
		err = fmt.Errorf("URL is empty")
	}

	if err != nil {
		if !(allowFallback && opts.AllowEnvFallback) {
			return reflect.Value{}, fmt.Errorf("failed to fetch config: %w", err)
		}
		if opts.URL != "" {
			log.Printf("Failed to fetch config from %s: %v, falling back to environment variables", opts.URL, err)
		}
		if opts.EnvLoader != nil {
			if envErr := opts.EnvLoader(v); envErr != nil {
				return reflect.Value{}, fmt.Errorf("fetch failed and env fallback failed: fetch=%v, env=%v", err, envErr)
			}
		}
		if fillErr := FillFromEnv(v, opts.EnvLookup); fillErr != nil {
			return reflect.Value{}, fmt.Errorf("fetch failed and env fallback failed: fetch=%v, env=%w", err, fillErr)
		}
		if opts.Validator != nil {
			if valErr := opts.Validator(v); valErr != nil {
				return reflect.Value{}, fmt.Errorf("env config validation failed: %w", valErr)
			}
		}
		if validator, ok := v.(interface{ Validate() error }); ok {
			if err := validator.Validate(); err != nil {
				return reflect.Value{}, fmt.Errorf("env config validation failed: %w", err)
			}
		}
		return value, nil
	}

	// Verify signature if enabled
	var header Header
	var stamped bool
	if opts.VerifySignature {
		if opts.PublicKey == "" {
			return reflect.Value{}, fmt.Errorf("publicKey is required when verifySignature is enabled")
		}
		if header, stamped, err = signedHeader(data, format, opts.PublicKey, target); err != nil {
			return reflect.Value{}, fmt.Errorf("signature verification failed: %w", err)
		}
	}

	// Parse config data
	if err := parse.Parse(data, v, format); err != nil {
		return reflect.Value{}, fmt.Errorf("failed to parse config: %w", err)
	}

	// Fill every env-tagged setting the document left empty, so Validate below
	// sees the value the endpoint will run with.
	if err := FillFromEnv(v, opts.EnvLookup); err != nil {
		return reflect.Value{}, err
	}

	// Automatically call Validate() if the type implements it
	if validator, ok := v.(interface{ Validate() error }); ok {
		if err := validator.Validate(); err != nil {
			return reflect.Value{}, fmt.Errorf("validation failed: %w", err)
		}
	}

	// Legacy: Validate if external validator provided (deprecated, use type's Validate() method)
	if opts.Validator != nil {
		if err := opts.Validator(v); err != nil {
			return reflect.Value{}, fmt.Errorf("validation failed: %w", err)
		}
	}

	if opts.Judge != nil {
		if err := opts.Judge(Verified{Header: header, Stamped: stamped, PublicKey: opts.PublicKey, Value: v}); err != nil {
			return reflect.Value{}, fmt.Errorf("the document was refused: %w", err)
		}
	}
	return value, nil
}

// signedHeader verifies data's signature under publicKey, checks that data decodes
// into target's type as the signed form does, and answers the header read from the
// signed form, never from the document's own bytes.
func signedHeader(data []byte, format parse.Format, publicKey string, target interface{}) (Header, bool, error) {
	reread, rereadIsJSON, err := verify.Reread(data, publicKey)
	if err != nil {
		return Header{}, false, err
	}
	if err := loadsAsSigned(data, format, reread, rereadIsJSON, target); err != nil {
		return Header{}, false, err
	}
	return ParseHeader(reread)
}

// BytesOptions configures LoadBytes.
type BytesOptions struct {
	// Format is the document's format (yaml, json, auto); empty is auto.
	Format parse.Format

	// PublicKeys are the base64 Ed25519 keys the signature may verify under,
	// tried in order: the caller's authenticated signers (for a device, the keys
	// its trust chain authenticates). The key that verifies is the one the Judge
	// is handed.
	PublicKeys []string

	// Validator is called after parsing, beside the type's own Validate.
	Validator func(target interface{}) error

	// Judge decides each verified document, as Load's Judge does: its header,
	// the key it verified under and the decoded value. Required.
	Judge func(Verified) error
}

// LoadBytes loads a signed document received as bytes, over an RPC rather than
// from a URL: Load's entry for documents a caller is handed. The signature must
// verify under one of PublicKeys and the Judge must admit the verified header,
// key and value before target is set; a refusal leaves target as it was. Nothing
// is fetched, no environment is read and nothing reloads.
func LoadBytes(target interface{}, data []byte, opts BytesOptions) error {
	if target == nil {
		return fmt.Errorf("target cannot be nil")
	}
	t := reflect.ValueOf(target)
	if t.Kind() != reflect.Ptr || t.IsNil() {
		return fmt.Errorf("target must be a non-nil pointer, got %T", target)
	}
	if opts.Judge == nil {
		return fmt.Errorf("a document received as bytes is judged: no Judge")
	}
	if len(opts.PublicKeys) == 0 {
		return fmt.Errorf("a document received as bytes verifies under a key: no PublicKeys")
	}
	format := opts.Format
	if format == "" {
		format = parse.FormatAuto
	}
	var (
		header  Header
		stamped bool
		key     string
		lastErr error
	)
	for _, k := range opts.PublicKeys {
		h, st, err := signedHeader(data, format, k, target)
		if err != nil {
			lastErr = err
			continue
		}
		header, stamped, key = h, st, k
		break
	}
	if key == "" {
		return fmt.Errorf("signature verification failed under every key: %w", lastErr)
	}
	value := reflect.New(t.Elem().Type())
	v := value.Interface()
	if err := parse.Parse(data, v, format); err != nil {
		return fmt.Errorf("failed to parse config: %w", err)
	}
	if validator, ok := v.(interface{ Validate() error }); ok {
		if err := validator.Validate(); err != nil {
			return fmt.Errorf("validation failed: %w", err)
		}
	}
	if opts.Validator != nil {
		if err := opts.Validator(v); err != nil {
			return fmt.Errorf("validation failed: %w", err)
		}
	}
	if err := opts.Judge(Verified{Header: header, Stamped: stamped, PublicKey: key, Value: v}); err != nil {
		return fmt.Errorf("the document was refused: %w", err)
	}
	t.Elem().Set(value.Elem())
	return nil
}

// MustLoad is like Load but panics on error
// Useful for initialization where config is critical
func MustLoad(target interface{}, opts LoadOptions) {
	if err := Load(target, opts); err != nil {
		panic(fmt.Sprintf("failed to load config: %v", err))
	}
}

// reserveReload returns a new watch state for target, or nil when target is
// already watched, so a second Load with EnableReload on one target starts no
// second reload.
func reserveReload(target interface{}) *reloadState {
	reloadMu.Lock()
	defer reloadMu.Unlock()
	if _, exists := reloadStates[target]; exists {
		return nil
	}
	state := &reloadState{stop: make(chan struct{})}
	reloadStates[target] = state
	return state
}

// startReload runs target's watch on state: every interval it loads the
// document again with opts and, on success, commits it to target, registers it
// and hands it to OnReload. firstPending is set when the first Load failed:
// each load then starts from the caller's value, as that first Load did, until
// one succeeds, and from then on from an empty value.
func startReload(target interface{}, state *reloadState, opts LoadOptions, firstPending bool) {
	interval := opts.ReloadInterval
	if interval <= 0 {
		interval = 5 * time.Minute
	}

	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()

		for {
			select {
			case <-state.stop:
				return
			case <-ticker.C:
			}
			value, err := loadValue(target, opts, !firstPending, false)
			if err != nil {
				if opts.OnReloadError != nil {
					opts.OnReloadError(err)
				} else {
					log.Printf("Config reload failed: %v", err)
				}
				continue
			}
			state.mu.Lock()
			if state.stopped {
				state.mu.Unlock()
				return
			}
			reflect.ValueOf(target).Elem().Set(value.Elem())
			state.mu.Unlock()
			firstPending = false
			if opts.Register {
				if err := UpdateValue(registryName(target, opts), value.Interface()); err != nil {
					log.Printf("Config reload: failed to register config '%s': %v", registryName(target, opts), err)
				}
			}
			if opts.OnReload != nil {
				opts.OnReload(value.Interface())
			}
		}
	}()
}

// StopReload stops the reload goroutine for a target and removes its state, so
// a later Load with EnableReload on the same target starts a new watch. Once it
// returns no reload commits into target; an OnReload already running finishes.
// Calling it for a target with no watch does nothing.
func StopReload(target interface{}) {
	reloadMu.Lock()
	state, exists := reloadStates[target]
	if exists {
		delete(reloadStates, target)
	}
	reloadMu.Unlock()
	if !exists {
		return
	}
	state.mu.Lock()
	if !state.stopped {
		state.stopped = true
		close(state.stop)
	}
	state.mu.Unlock()
}

// deepCopy returns a copy of v that shares no pointer, slice, map or interface
// value with it, so a decode into the copy cannot write through to v. Load
// copies the caller's target with it before the first load decodes over it,
// and commits the copy only when the load succeeds. Unexported fields are
// copied as they are, since no decoder writes them.
func deepCopy(v reflect.Value) reflect.Value {
	switch v.Kind() {
	case reflect.Ptr:
		if v.IsNil() {
			return reflect.Zero(v.Type())
		}
		out := reflect.New(v.Type().Elem())
		out.Elem().Set(deepCopy(v.Elem()))
		return out
	case reflect.Interface:
		if v.IsNil() {
			return reflect.Zero(v.Type())
		}
		out := reflect.New(v.Type()).Elem()
		out.Set(deepCopy(v.Elem()))
		return out
	case reflect.Struct:
		out := reflect.New(v.Type()).Elem()
		out.Set(v)
		for i := 0; i < v.NumField(); i++ {
			if out.Field(i).CanSet() {
				out.Field(i).Set(deepCopy(v.Field(i)))
			}
		}
		return out
	case reflect.Slice:
		if v.IsNil() {
			return reflect.Zero(v.Type())
		}
		out := reflect.MakeSlice(v.Type(), v.Len(), v.Len())
		for i := 0; i < v.Len(); i++ {
			out.Index(i).Set(deepCopy(v.Index(i)))
		}
		return out
	case reflect.Array:
		out := reflect.New(v.Type()).Elem()
		for i := 0; i < v.Len(); i++ {
			out.Index(i).Set(deepCopy(v.Index(i)))
		}
		return out
	case reflect.Map:
		if v.IsNil() {
			return reflect.Zero(v.Type())
		}
		out := reflect.MakeMapWithSize(v.Type(), v.Len())
		iter := v.MapRange()
		for iter.Next() {
			out.SetMapIndex(iter.Key(), deepCopy(iter.Value()))
		}
		return out
	}
	return v
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
