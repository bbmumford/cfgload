package cfgload_test

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/bbmumford/cfgload"
	"github.com/bbmumford/envreq"
)

type envAuth struct {
	Token string `yaml:"token" env:"FALLBACK_TEST_TOKEN,sensitive,required"`
	User  string `yaml:"user" env:"FALLBACK_TEST_USER"`
}

type envGroup struct {
	Key string `yaml:"key" env:"FALLBACK_TEST_GROUP_KEY"`
}

type envTarget struct {
	Name     string     `yaml:"name"`
	Endpoint string     `yaml:"endpoint" env:"FALLBACK_TEST_ENDPOINT"`
	Auth     envAuth    `yaml:"auth"`
	Groups   []envGroup `yaml:"groups"`
	Extra    *envAuth   `yaml:"extra"`
	Ignored  string     `yaml:"-" env:"FALLBACK_TEST_IGNORED"`
	hidden   string     //nolint:unused
}

type recordingLookup struct {
	vals map[string]string
	read []cfgload.EnvVar
}

func (r *recordingLookup) lookup(v cfgload.EnvVar) (string, bool) {
	r.read = append(r.read, v)
	s, ok := r.vals[v.Name]
	return s, ok
}

func writeDoc(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "doc.yaml")
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return "file://" + p
}

// A setting the document carries is kept and its variable is never read; a
// setting it leaves empty, or holds only whitespace, is read and trimmed.
func TestLoadFillsOnlyWhatTheDocumentLeavesEmpty(t *testing.T) {
	url := writeDoc(t, "name: svc\nendpoint: \"https://doc.example\"\nauth:\n  user: \"  \"\ngroups:\n  - key: \"\"\n  - key: kept\n")
	env := &recordingLookup{vals: map[string]string{
		"FALLBACK_TEST_ENDPOINT":  "https://env.example",
		"FALLBACK_TEST_TOKEN":     "  tok  ",
		"FALLBACK_TEST_USER":      "env-user",
		"FALLBACK_TEST_GROUP_KEY": "env-key",
	}}
	var c envTarget
	if err := cfgload.Load(&c, cfgload.LoadOptions{URL: url, EnvLookup: env.lookup}); err != nil {
		t.Fatalf("Load = %v", err)
	}
	if c.Endpoint != "https://doc.example" {
		t.Errorf("endpoint = %q, want the document's", c.Endpoint)
	}
	if c.Auth.Token != "tok" || c.Auth.User != "env-user" {
		t.Errorf("auth = %+v, want the trimmed token and the variable's user", c.Auth)
	}
	if c.Groups[0].Key != "env-key" || c.Groups[1].Key != "kept" {
		t.Errorf("groups = %+v", c.Groups)
	}
	if c.Ignored != "" {
		t.Errorf("a yaml:\"-\" field was filled")
	}
	var names []string
	for _, v := range env.read {
		names = append(names, v.Path+"="+v.Name)
		if v.Name == "FALLBACK_TEST_ENDPOINT" && v.Path == "endpoint" {
			t.Errorf("endpoint's variable was read though the document set it")
		}
	}
	want := []string{"auth.token=FALLBACK_TEST_TOKEN", "auth.user=FALLBACK_TEST_USER", "groups[0].key=FALLBACK_TEST_GROUP_KEY"}
	if !reflect.DeepEqual(names, want) {
		t.Errorf("variables read = %v, want %v", names, want)
	}
}

// A required setting that neither source supplies fails the load, naming the
// setting and the variable and carrying no value.
func TestLoadRefusesAMissingRequiredSetting(t *testing.T) {
	url := writeDoc(t, "name: svc\nauth:\n  user: secret-looking-user\n")
	env := &recordingLookup{vals: map[string]string{"FALLBACK_TEST_TOKEN": "   "}}
	var c envTarget
	err := cfgload.Load(&c, cfgload.LoadOptions{URL: url, EnvLookup: env.lookup})
	if !errors.Is(err, cfgload.ErrRequiredSetting) {
		t.Fatalf("Load = %v, want ErrRequiredSetting", err)
	}
	if !strings.Contains(err.Error(), "auth.token") || !strings.Contains(err.Error(), "FALLBACK_TEST_TOKEN") {
		t.Errorf("error %q does not name the setting and its variable", err)
	}
	if strings.Contains(err.Error(), "secret-looking-user") {
		t.Errorf("error %q carries a value", err)
	}
}

// The fill runs before the target's Validate, so Validate sees the filled
// value.
type validatedTarget struct {
	Token string `yaml:"token" env:"FALLBACK_TEST_VALIDATED"`
}

func (v *validatedTarget) Validate() error {
	if v.Token == "" {
		return errors.New("token empty at Validate")
	}
	return nil
}

func TestValidateSeesTheFilledValue(t *testing.T) {
	url := writeDoc(t, "token: \"\"\n")
	env := &recordingLookup{vals: map[string]string{"FALLBACK_TEST_VALIDATED": "v"}}
	var c validatedTarget
	if err := cfgload.Load(&c, cfgload.LoadOptions{URL: url, EnvLookup: env.lookup}); err != nil {
		t.Fatalf("Load = %v", err)
	}
}

// When the fetch fails and the fallback is allowed, the tags still fill the
// target and a required setting is still enforced.
func TestTheFetchFallbackFillsFromTags(t *testing.T) {
	env := &recordingLookup{vals: map[string]string{"FALLBACK_TEST_TOKEN": "t"}}
	var c envTarget
	if err := cfgload.Load(&c, cfgload.LoadOptions{AllowEnvFallback: true, EnvLookup: env.lookup}); err != nil {
		t.Fatalf("Load = %v", err)
	}
	if c.Auth.Token != "t" {
		t.Errorf("token = %q", c.Auth.Token)
	}
	var d envTarget
	err := cfgload.Load(&d, cfgload.LoadOptions{AllowEnvFallback: true, EnvLookup: (&recordingLookup{}).lookup})
	if !errors.Is(err, cfgload.ErrRequiredSetting) {
		t.Fatalf("Load without the token = %v, want ErrRequiredSetting", err)
	}
}

// With no lookup the variables are read through envreq, registered optional and
// with the tag's sensitivity, so cfgload rather than envreq enforces required.
func TestTheDefaultLookupReadsThroughEnvreq(t *testing.T) {
	envreq.Reset()
	t.Cleanup(envreq.Reset)
	t.Setenv("FALLBACK_TEST_TOKEN", "from-env")
	var c envTarget
	if err := cfgload.FillFromEnv(&c, nil); err != nil {
		t.Fatalf("FillFromEnv = %v", err)
	}
	if c.Auth.Token != "from-env" {
		t.Fatalf("token = %q", c.Auth.Token)
	}
	var found bool
	for _, r := range envreq.CheckAll() {
		if r.Name == "FALLBACK_TEST_TOKEN" {
			found = true
			if !r.Optional || !r.Sensitive {
				t.Errorf("requirement = optional %t sensitive %t, want both", r.Optional, r.Sensitive)
			}
		}
	}
	if !found {
		t.Errorf("FALLBACK_TEST_TOKEN was not registered with envreq")
	}
}

type badTagged struct {
	Count int `yaml:"count" env:"FALLBACK_TEST_COUNT"`
}

type badOption struct {
	Token string `yaml:"token" env:"FALLBACK_TEST_TOKEN,secret"`
}

func TestAMalformedTagIsRefused(t *testing.T) {
	if err := cfgload.FillFromEnv(&badTagged{}, (&recordingLookup{}).lookup); err == nil || !strings.Contains(err.Error(), "count") {
		t.Errorf("non-string tag: %v", err)
	}
	if err := cfgload.FillFromEnv(&badOption{}, (&recordingLookup{}).lookup); err == nil || !strings.Contains(err.Error(), "secret") {
		t.Errorf("unknown option: %v", err)
	}
}

func TestEnvFieldsListsTheTags(t *testing.T) {
	c := envTarget{Groups: []envGroup{{}}, Extra: &envAuth{}}
	got, err := cfgload.EnvFields(&c)
	if err != nil {
		t.Fatal(err)
	}
	var paths []string
	for _, v := range got {
		paths = append(paths, v.Path)
	}
	want := []string{"endpoint", "auth.token", "auth.user", "groups[0].key", "extra.token", "extra.user"}
	if !reflect.DeepEqual(paths, want) {
		t.Errorf("paths = %v, want %v", paths, want)
	}
	if !got[1].Sensitive || !got[1].Required || got[2].Sensitive {
		t.Errorf("options = %+v", got[1:3])
	}
}

// A target that is not a pointer to a struct carries no tags.
func TestANonStructTargetIsLeftAlone(t *testing.T) {
	m := map[string]any{}
	if err := cfgload.FillFromEnv(&m, nil); err != nil {
		t.Fatal(err)
	}
}
