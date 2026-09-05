package enccfg

import (
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func valid() Config {
	return Config{Domains: map[string]DomainKeys{
		"sessions": {
			CurrentVersion: "v2",
			Keys: map[string]Key{
				"v1": {WrappedKey: "d3JhcHBlZC12MQ==", Status: "inactive"},
				"v2": {WrappedKey: "d3JhcHBlZC12Mg==", Status: "active"},
			},
		},
	}}
}

func TestValidConfigPasses(t *testing.T) {
	if err := valid().Validate(); err != nil {
		t.Fatalf("a well-formed config was rejected: %v", err)
	}
}

// BOTH forms set is the dangerous case: an operator wrapping a key is trying to
// stop the plaintext existing, and leaving the old field populated means the file
// still holds exactly what the wrapping was meant to remove — while the system
// keeps working, so nothing surfaces it.
func TestBothKeyFormsIsRejected(t *testing.T) {
	c := valid()
	dk := c.Domains["sessions"]
	dk.Keys["v2"] = Key{Key: "cGxhaW50ZXh0", WrappedKey: "d3JhcHBlZA==", Status: "active"}
	c.Domains["sessions"] = dk

	err := c.Validate()
	if err == nil {
		t.Fatal("a version carrying both plaintext and wrapped key was accepted")
	}
	if !strings.Contains(err.Error(), "both") {
		t.Errorf("error does not name the problem: %v", err)
	}
}

// A version listed in Keys is a promise that data written under it can be read.
// An entry with no material breaks that promise silently at decrypt time.
func TestVersionWithNoMaterialIsRejected(t *testing.T) {
	c := valid()
	dk := c.Domains["sessions"]
	dk.Keys["v3"] = Key{Status: "active"}
	c.Domains["sessions"] = dk
	if err := c.Validate(); err == nil {
		t.Error("a version with neither key nor wrappedKey was accepted")
	}
}

// A currentVersion absent from Keys fails at the first encrypt otherwise —
// long after the operator who changed it has stopped watching.
func TestCurrentVersionMustExist(t *testing.T) {
	c := valid()
	dk := c.Domains["sessions"]
	dk.CurrentVersion = "v9"
	c.Domains["sessions"] = dk
	if err := c.Validate(); err == nil {
		t.Error("a currentVersion naming no key was accepted")
	}
}

// The deployed YAML keys must keep loading; a renamed key leaves the field zero
// and the application starts with no key material rather than failing loudly.
func TestDeployedKeysParse(t *testing.T) {
	const doc = `
domains:
  sessions:
    currentVersion: v2
    rotationPolicy: 90d
    keys:
      v1:
        wrappedKey: d3JhcHBlZC12MQ==
        status: inactive
      v2:
        key: cGxhaW50ZXh0LXYy
        status: active
`
	var c Config
	if err := yaml.Unmarshal([]byte(doc), &c); err != nil {
		t.Fatalf("deployed document did not parse: %v", err)
	}
	d := c.Domains["sessions"]
	if d.CurrentVersion != "v2" || d.RotationPolicy != "90d" || len(d.Keys) != 2 {
		t.Fatalf("document did not load: %+v", d)
	}
	if d.Keys["v1"].WrappedKey == "" || d.Keys["v2"].Key == "" || d.Keys["v2"].Status != "active" {
		t.Errorf("key fields did not load: %+v", d.Keys)
	}
	if err := c.Validate(); err != nil {
		t.Errorf("a valid deployed document failed validation: %v", err)
	}
}
