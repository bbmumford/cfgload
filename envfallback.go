package cfgload

import (
	"errors"
	"fmt"
	"reflect"
	"strings"

	"github.com/bbmumford/envreq"
)

// EnvVar is one setting a target's env tag names, as FillFromEnv reads it.
//
// The tag is written `env:"NAME"`, optionally followed by the options
// ",sensitive" and ",required": `env:"API_TOKEN,sensitive,required"`.
type EnvVar struct {
	// Name is the environment variable the tag names.
	Name string
	// Path is the setting's place in the document, spelled with its yaml
	// keys, as "server.auth.token" or "groups[1].key". Errors name it.
	Path string
	// Sensitive marks a value no requirement report or log line may print.
	Sensitive bool
	// Required makes a setting that neither the document nor the variable
	// supplies fail the load.
	Required bool
}

// EnvLookup reads one variable for FillFromEnv and reports whether it is set.
// LoadOptions.EnvLookup carries one; a nil lookup reads through envreq.
type EnvLookup func(v EnvVar) (string, bool)

// ErrRequiredSetting reports a required setting that neither the document nor
// its variable supplied.
var ErrRequiredSetting = errors.New("cfgload: required setting is empty")

// FillFromEnv supplies every string field of target that carries an env tag
// and is empty from the variable the tag names, then refuses a required field
// that is still empty.
//
// A field the document filled is kept and its variable is not read, so the
// document wins. A field holding only whitespace counts as empty, and is left
// empty when its variable supplies nothing. A variable that is unset or blank
// supplies nothing, and a supplied value is trimmed. The refusal wraps
// ErrRequiredSetting and names the setting's path and its variable, never a
// value.
//
// Load calls it after the document parses, and after EnvLoader when the fetch
// fails, so the target's Validate sees the filled value. A caller that holds a
// value without loading a document calls it directly.
//
// Nested structs, non-nil pointers to structs and the struct elements of slices
// are walked. A target that is not a pointer to a struct carries no tags and is
// left alone. A tag on a field that is not a string is refused, since only a
// string field has one reading of a variable's text.
func FillFromEnv(target interface{}, lookup EnvLookup) error {
	vars, err := envFields(target)
	if err != nil {
		return err
	}
	if lookup == nil {
		lookup = envreqLookup(target)
	}
	var missing []string
	for _, f := range vars {
		if strings.TrimSpace(f.dst.String()) != "" {
			continue
		}
		f.dst.SetString("")
		if v, ok := lookup(f.EnvVar); ok {
			if v = strings.TrimSpace(v); v != "" {
				f.dst.SetString(v)
				continue
			}
		}
		if f.Required {
			missing = append(missing, fmt.Sprintf("%s (set it in the document or in %s)", f.Path, f.Name))
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("%w: %s", ErrRequiredSetting, strings.Join(missing, "; "))
	}
	return nil
}

// EnvFields lists every setting target's env tags name, in field order, so a
// caller can report or register them before loading.
func EnvFields(target interface{}) ([]EnvVar, error) {
	fs, err := envFields(target)
	if err != nil {
		return nil, err
	}
	out := make([]EnvVar, len(fs))
	for i, f := range fs {
		out[i] = f.EnvVar
	}
	return out, nil
}

// envField is an EnvVar bound to the field it fills.
type envField struct {
	EnvVar
	dst reflect.Value
}

func envFields(target interface{}) ([]envField, error) {
	v := reflect.ValueOf(target)
	if !v.IsValid() || v.Kind() != reflect.Pointer || v.IsNil() || v.Elem().Kind() != reflect.Struct {
		return nil, nil
	}
	var out []envField
	if err := walkEnv(v.Elem(), "", &out); err != nil {
		return nil, err
	}
	return out, nil
}

func walkEnv(v reflect.Value, path string, out *[]envField) error {
	t := v.Type()
	for i := 0; i < t.NumField(); i++ {
		sf := t.Field(i)
		if !sf.IsExported() {
			continue
		}
		key, skip := yamlKey(sf)
		if skip {
			continue
		}
		p := joinPath(path, key)
		fv := v.Field(i)
		if tag, ok := sf.Tag.Lookup("env"); ok {
			ev, err := parseEnvTag(tag, p)
			if err != nil {
				return err
			}
			if fv.Kind() != reflect.String {
				return fmt.Errorf("cfgload: %s carries env tag %q on a %s field; only a string field can take a variable", p, ev.Name, fv.Kind())
			}
			*out = append(*out, envField{EnvVar: ev, dst: fv})
			continue
		}
		if err := walkEnvValue(fv, p, out); err != nil {
			return err
		}
	}
	return nil
}

func walkEnvValue(v reflect.Value, path string, out *[]envField) error {
	switch v.Kind() {
	case reflect.Struct:
		return walkEnv(v, path, out)
	case reflect.Pointer:
		if v.IsNil() || v.Elem().Kind() != reflect.Struct {
			return nil
		}
		return walkEnv(v.Elem(), path, out)
	case reflect.Slice, reflect.Array:
		for i := 0; i < v.Len(); i++ {
			if err := walkEnvValue(v.Index(i), fmt.Sprintf("%s[%d]", path, i), out); err != nil {
				return err
			}
		}
	}
	return nil
}

// yamlKey returns the document key a field is spelled with, "" for a field
// yaml inlines into its parent, and skip for a field yaml ignores.
func yamlKey(sf reflect.StructField) (key string, skip bool) {
	tag := sf.Tag.Get("yaml")
	name, opts, _ := strings.Cut(tag, ",")
	if name == "-" {
		return "", true
	}
	if strings.Contains(","+opts+",", ",inline,") || (sf.Anonymous && name == "") {
		return "", false
	}
	if name == "" {
		name = strings.ToLower(sf.Name)
	}
	return name, false
}

func joinPath(path, key string) string {
	switch {
	case key == "":
		return path
	case path == "":
		return key
	}
	return path + "." + key
}

func parseEnvTag(tag, path string) (EnvVar, error) {
	parts := strings.Split(tag, ",")
	ev := EnvVar{Name: strings.TrimSpace(parts[0]), Path: path}
	if ev.Name == "" {
		return EnvVar{}, fmt.Errorf("cfgload: %s carries an env tag with no variable name", path)
	}
	for _, o := range parts[1:] {
		switch strings.TrimSpace(o) {
		case "sensitive":
			ev.Sensitive = true
		case "required":
			ev.Required = true
		default:
			return EnvVar{}, fmt.Errorf("cfgload: %s carries env tag option %q; the options are sensitive and required", path, o)
		}
	}
	return ev, nil
}

// envreqLookup is the lookup FillFromEnv uses when the caller passes none: one
// optional envreq requirement per variable, sourced to the target's type, so
// the process's environment report lists it. cfgload itself enforces Required
// after every source has been read, which keeps an absent variable a load
// error rather than an envreq exit.
func envreqLookup(target interface{}) EnvLookup {
	source := "cfgload"
	if t := reflect.TypeOf(target); t != nil {
		source = "cfgload:" + t.String()
	}
	return func(v EnvVar) (string, bool) {
		r := envreq.Check(envreq.Requirement{
			Name:        v.Name,
			Source:      source,
			Description: "supplies " + v.Path + " when the document leaves it empty",
			Optional:    true,
			Sensitive:   v.Sensitive,
		})
		return r.Value, r.Present
	}
}
