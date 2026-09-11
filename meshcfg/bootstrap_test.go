package meshcfg

import (
	"encoding/json"
	"testing"
)

func TestBootstrapValidate(t *testing.T) {
	good := Bootstrap{Version: 1, Epoch: 1700000000, Seeds: []string{"seed.example:9993", "10.0.0.1:9993"}}
	if err := good.Validate(); err != nil {
		t.Fatalf("a well-formed manifest must validate: %v", err)
	}
	bad := map[string]Bootstrap{
		"zero version": {Version: 0, Epoch: 1, Seeds: []string{"a:1"}},
		"zero epoch":   {Version: 1, Epoch: 0, Seeds: []string{"a:1"}},
		"no seeds":     {Version: 1, Epoch: 1},
		"empty seed":   {Version: 1, Epoch: 1, Seeds: []string{"a:1", ""}},
		"no port":      {Version: 1, Epoch: 1, Seeds: []string{"seedhost"}},
		"too short":    {Version: 1, Epoch: 1, Seeds: []string{"a:"}},
	}
	for name, b := range bad {
		if err := b.Validate(); err == nil {
			t.Errorf("%s: Validate accepted %+v", name, b)
		}
	}
}

// TestBootstrapKeepsTheManifestKeys pins the JSON keys a signed manifest
// carries, so a published manifest decodes into every field.
func TestBootstrapKeepsTheManifestKeys(t *testing.T) {
	var b Bootstrap
	raw := `{"version":2,"epoch":1700000001,"seeds":["a:1"],"signature":"sig"}`
	if err := json.Unmarshal([]byte(raw), &b); err != nil {
		t.Fatal(err)
	}
	if b.Version != 2 || b.Epoch != 1700000001 || len(b.Seeds) != 1 || b.Seeds[0] != "a:1" || b.Signature != "sig" {
		t.Fatalf("manifest fields not decoded: %+v", b)
	}
}
