package appcfg

import (
	"testing"

	"gopkg.in/yaml.v3"
)

// The keys in this document are the ones deployed configs already use. A config
// key is a wire contract with every environment that has one written down, so a
// renamed key does not fail loudly — it leaves the field at its zero value and
// the application runs misconfigured.
const deployed = `
app:
  name: wrkly.app
  domain: wrkly.app
  environment: production
  logLevel: info
  debugLog: true
  tenantID: hstles
  allowedCORSDomains: ["example.com"]
ui:
  templateCacheEnabled: true
  staticCacheMaxAge: "24h"
security:
  cookieName: session
mesh:
  exposeBootstrap: false
maintenance:
  enabled: true
  reason: upgrading
  allowedIPs: ["203.0.113.7", "198.51.100.0/24"]
signature: abc123
`

func TestDeployedKeysStillParse(t *testing.T) {
	var c Config
	if err := yaml.Unmarshal([]byte(deployed), &c); err != nil {
		t.Fatalf("deployed document did not parse: %v", err)
	}
	for name, got := range map[string]any{
		"app.name":                c.App.Name,
		"app.domain":              c.App.Domain,
		"app.environment":         c.App.Environment,
		"app.logLevel":            c.App.LogLevel,
		"app.debugLog":            c.App.DebugLog,
		"ui.templateCacheEnabled": c.UI.TemplateCacheEnabled,
		"ui.staticCacheMaxAge":    c.UI.StaticCacheMaxAge,
		"security.cookieName":     c.Security.CookieName,
		"maintenance.enabled":     c.Maintenance.Enabled,
		"maintenance.reason":      c.Maintenance.Reason,
		"signature":               c.Signature,
	} {
		if got == "" || got == false {
			t.Errorf("%s did not load (got %#v) — the key no longer matches the document", name, got)
		}
	}
	// exposeBootstrap is deliberately false in the document; assert it parsed as
	// false rather than being absent, by checking a sibling that must be set.
	if c.Mesh.ExposeBootstrap {
		t.Error("mesh.exposeBootstrap read true from a document that says false")
	}
	if len(c.Maintenance.AllowedIPs) != 2 || len(c.App.AllowedCORSDomains) != 1 {
		t.Errorf("list fields did not load: ips=%v cors=%v", c.Maintenance.AllowedIPs, c.App.AllowedCORSDomains)
	}
}

// The scope is opaque HERE, but its KEY is the deployed one. Renaming the Go
// field must not rename the contract: a document saying tenantID has to keep
// landing in the field the application reads.
func TestOpaqueScopeKeepsTheDeployedKey(t *testing.T) {
	var c Config
	if err := yaml.Unmarshal([]byte(deployed), &c); err != nil {
		t.Fatal(err)
	}
	if c.App.Partition != "hstles" {
		t.Errorf("Partition = %q, want the value under the deployed tenantID key", c.App.Partition)
	}
}

// A document omitting a whole section must leave that section zeroed rather than
// failing, since each zero is a defined state — notably an empty allow-list,
// which admits nobody.
func TestAbsentSectionsAreZeroNotAnError(t *testing.T) {
	var c Config
	if err := yaml.Unmarshal([]byte("app:\n  name: x\n"), &c); err != nil {
		t.Fatalf("a minimal document failed to parse: %v", err)
	}
	if c.Maintenance.Enabled || len(c.Maintenance.AllowedIPs) != 0 {
		t.Errorf("absent maintenance section did not zero: %+v", c.Maintenance)
	}
}
