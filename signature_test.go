package cfgload_test

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/bbmumford/cfgload"
)

// signedTarget is the document shape the signature tests load. Server and
// Routes are an object and a list of objects because those are the two shapes
// encoding/json fills in place when a key is written twice.
type signedTarget struct {
	Name   string `yaml:"name" json:"name"`
	Server struct {
		Host string `yaml:"host" json:"host"`
		Port int    `yaml:"port" json:"port"`
	} `yaml:"server" json:"server"`
	Routes []struct {
		Path  string `yaml:"path" json:"path"`
		Allow string `yaml:"allow" json:"allow"`
	} `yaml:"routes" json:"routes"`
}

// signingKey returns a fresh key pair with the public half encoded as
// LoadOptions.PublicKey takes it.
func signingKey(t *testing.T) (ed25519.PrivateKey, string) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	return priv, base64.StdEncoding.EncodeToString(pub)
}

// signJSON returns the signature Load verifies for a JSON document holding
// fields: the Ed25519 signature over the fields as encoding/json marshals a
// map, which is what the loader re-creates from the document it is given.
func signJSON(t *testing.T, priv ed25519.PrivateKey, fields map[string]interface{}) string {
	t.Helper()
	canonical, err := json.Marshal(fields)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return base64.StdEncoding.EncodeToString(ed25519.Sign(priv, canonical))
}

// signYAML is signJSON for a YAML document.
func signYAML(t *testing.T, priv ed25519.PrivateKey, fields map[string]interface{}) string {
	t.Helper()
	canonical, err := yaml.Marshal(fields)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return base64.StdEncoding.EncodeToString(ed25519.Sign(priv, canonical))
}

// loadSigned writes doc under name and loads it with signature verification.
func loadSigned(t *testing.T, name, doc, publicKey string) (signedTarget, error) {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(doc), 0o600); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
	var got signedTarget
	err := cfgload.Load(&got, cfgload.LoadOptions{URL: "file://" + path, VerifySignature: true, PublicKey: publicKey})
	return got, err
}

func signedFields() map[string]interface{} {
	return map[string]interface{}{
		"name":   "edge",
		"server": map[string]interface{}{"port": 8443},
		"routes": []interface{}{map[string]interface{}{"path": "/health"}},
	}
}

// A document carrying the signature of its own fields loads, in both formats,
// and one whose signed field was changed, or that carries no signature, does
// not.
func TestASignedDocumentLoadsAndAChangedOneDoesNot(t *testing.T) {
	priv, pub := signingKey(t)

	jsonDoc := `{"name":"edge","server":{"port":8443},"routes":[{"path":"/health"}],"signature":"` +
		signJSON(t, priv, signedFields()) + `"}`
	got, err := loadSigned(t, "c.json", jsonDoc, pub)
	if err != nil || got.Name != "edge" || got.Server.Port != 8443 || len(got.Routes) != 1 || got.Routes[0].Path != "/health" {
		t.Fatalf("a signed JSON document loaded as %+v, %v", got, err)
	}
	if _, err := loadSigned(t, "c.json", strings.Replace(jsonDoc, "8443", "8444", 1), pub); err == nil {
		t.Error("a JSON document with a changed signed value loaded")
	}

	yamlDoc := "name: edge\nserver:\n  port: 8443\nroutes:\n  - path: /health\nsignature: " +
		signYAML(t, priv, signedFields()) + "\n"
	got, err = loadSigned(t, "c.yaml", yamlDoc, pub)
	if err != nil || got.Name != "edge" || got.Server.Port != 8443 || len(got.Routes) != 1 || got.Routes[0].Path != "/health" {
		t.Fatalf("a signed YAML document loaded as %+v, %v", got, err)
	}
	if _, err := loadSigned(t, "c.yaml", strings.Replace(yamlDoc, "8443", "8444", 1), pub); err == nil {
		t.Error("a YAML document with a changed signed value loaded")
	}

	if _, err := loadSigned(t, "c.json", `{"name":"edge","server":{"port":8443}}`, pub); err == nil {
		t.Error("a JSON document with no signature loaded")
	}
	_, otherPub := signingKey(t)
	if _, err := loadSigned(t, "c.json", jsonDoc, otherPub); err == nil {
		t.Error("a JSON document loaded under a key that did not sign it")
	}
}

// A JSON document cannot carry a field its signature does not cover. The
// signature is checked over the document decoded into a map, where a key
// written twice keeps its last value, and the target is then filled from the
// document's own bytes, where encoding/json fills an object or a list element
// already set and so keeps what an earlier occurrence wrote. Without the
// refusal, whoever can alter a signed document in storage or on the way adds
// any field the signed object leaves unset, with no signing key.
func TestAJSONDocumentCannotCarryFieldsItsSignatureDoesNotCover(t *testing.T) {
	priv, pub := signingKey(t)
	sig := signJSON(t, priv, signedFields())

	for name, doc := range map[string]string{
		"an object key written twice": `{"name":"edge","server":{"host":"attacker.example"},"server":{"port":8443},` +
			`"routes":[{"path":"/health"}],"signature":"` + sig + `"}`,
		"a list key written twice": `{"name":"edge","server":{"port":8443},"routes":[{"allow":"everyone"}],` +
			`"routes":[{"path":"/health"}],"signature":"` + sig + `"}`,
		"a key written twice inside a nested object": `{"name":"edge","server":{"port":1,"port":8443},` +
			`"routes":[{"path":"/health"}],"signature":"` + sig + `"}`,
		"a key written twice inside a list element": `{"name":"edge","server":{"port":8443},` +
			`"routes":[{"path":"/x","path":"/health"}],"signature":"` + sig + `"}`,
	} {
		got, err := loadSigned(t, "c.json", doc, pub)
		if err == nil {
			t.Errorf("%s: the document loaded as %+v; a field no signature covers must refuse the load", name, got)
		}
	}

	// Two keys that differ only in letter case fill one field of the target,
	// the later one winning, while the map the signature is checked over
	// keeps both and sorts them. Their order in the document then decides the
	// value and is not signed.
	both := map[string]interface{}{"name": "edge", "Name": "other", "server": map[string]interface{}{"port": 8443}}
	bothSig := signJSON(t, priv, both)
	for _, doc := range []string{
		`{"name":"edge","Name":"other","server":{"port":8443},"signature":"` + bothSig + `"}`,
		`{"Name":"other","name":"edge","server":{"port":8443},"signature":"` + bothSig + `"}`,
	} {
		if got, err := loadSigned(t, "c.json", doc, pub); err == nil {
			t.Errorf("a document naming one field under two spellings loaded with name %q; its key order is unsigned", got.Name)
		}
	}
}

// signDocYAML signs doc as a signer holding only the document does: it decodes
// the document into a map, marshals the map and signs those bytes, which is the
// form the loader re-creates and checks.
func signDocYAML(t *testing.T, priv ed25519.PrivateKey, doc string) string {
	t.Helper()
	var fields map[string]interface{}
	if err := yaml.Unmarshal([]byte(doc), &fields); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return signYAML(t, priv, fields)
}

// Every document that loads under one signature loads the same value. The
// signature is checked over the document decoded into a map and marshalled
// again, and that form does not keep how a value was written: an unquoted 1.10
// is the number 1.1 there, 0123 is 83 and 2026-01-02 is an instant. A text
// field filled from the document's own bytes takes the characters as written,
// so without the check whoever can alter a signed document rewrites such a
// value into another spelling of the same number, under the same signature,
// and the field loads a different string. With it, of all the spellings one
// signature covers, only the one that reads back as the covered form loads,
// so an author who writes such a value unquoted is refused at the first load
// and quotes it, and from then on the signature covers its text.
func TestEveryDocumentUnderOneSignatureLoadsTheSameValue(t *testing.T) {
	priv, pub := signingKey(t)

	for name, tc := range map[string]struct {
		before, after string
		spellings     []string
		loads         string
	}{
		"a decimal in a text field":      {"name: ", "\nserver:\n  port: 8443\n", []string{"1.10", "1.1", "1.100", "11e-1"}, "1.1"},
		"a leading zero in a text field": {"name: edge\nserver:\n  host: ", "\n  port: 8443\n", []string{"0123", "83", "0o123", "0x53"}, "83"},
		"an exponent in a text field":    {"name: edge\nserver:\n  host: ", "\n  port: 8443\n", []string{"1e3", "1000.0", "1000", "1_000"}, "1000"},
		"a date in a text field":         {"name: ", "\nserver:\n  port: 8443\n", []string{"2026-01-02", "2026-01-02T00:00:00Z"}, "2026-01-02T00:00:00Z"},
		"a number-like text in a list":   {"name: edge\nroutes:\n  - path: ", "\n", []string{"007", "7", "0x7"}, "7"},
	} {
		sig := signDocYAML(t, priv, tc.before+tc.spellings[0]+tc.after)
		var loaded []signedTarget
		for _, spelling := range tc.spellings {
			got, err := loadSigned(t, "c.yaml", tc.before+spelling+tc.after+"signature: "+sig+"\n", pub)
			if spelling == tc.loads {
				if err != nil {
					t.Errorf("%s: the spelling %q, which reads back as the signed form, was refused: %v", name, spelling, err)
				}
				loaded = append(loaded, got)
				continue
			}
			if err == nil {
				t.Errorf("%s: the spelling %q loaded as %+v under a signature that covers %q", name, spelling, got, tc.loads)
				loaded = append(loaded, got)
			}
		}
		for _, got := range loaded[1:] {
			if !reflect.DeepEqual(got, loaded[0]) {
				t.Errorf("%s: two documents under one signature loaded %+v and %+v", name, loaded[0], got)
			}
		}
	}

	// Quoted, the value is text in both readings: the document loads, and a
	// change to it breaks the signature.
	quoted := "name: \"1.10\"\nserver:\n  host: \"0123\"\n  port: 8443\n"
	sig := signDocYAML(t, priv, quoted)
	got, err := loadSigned(t, "c.yaml", quoted+"signature: "+sig+"\n", pub)
	if err != nil || got.Name != "1.10" || got.Server.Host != "0123" || got.Server.Port != 8443 {
		t.Fatalf("a document with its number-like text quoted loaded as %+v, %v", got, err)
	}
	if _, err := loadSigned(t, "c.yaml", strings.Replace(quoted, "1.10", "1.1", 1)+"signature: "+sig+"\n", pub); err == nil {
		t.Error("a quoted value was changed and the document still loaded")
	}

	// A number written another way still fills a number field with the same
	// number, and a key with no value is the same absence in both readings, so
	// neither is refused: the check compares what loads, not how it is written.
	for name, doc := range map[string]string{
		"a hexadecimal port":      "name: edge\nserver:\n  port: 0x20FB\n",
		"an underscored port":     "name: edge\nserver:\n  port: 8_443\n",
		"a key with no value":     "name: edge\nserver:\n  port: 8443\nroutes:\n",
		"an object with no value": "name: edge\nserver:\n",
	} {
		sig := signDocYAML(t, priv, doc)
		if _, err := loadSigned(t, "c.yaml", doc+"signature: "+sig+"\n", pub); err != nil {
			t.Errorf("%s: the document was refused: %v", name, err)
		}
	}
}

// selfDescribing is a target that declares the document's signature as a
// field, as the endpoint spine's own config type does.
type selfDescribing struct {
	Name      string `yaml:"name" json:"name"`
	Signature string `yaml:"signature,omitempty" json:"signature,omitempty"`
}

// A target that declares the signature field loads, and holds the document's
// signature. The signature is not among the bytes it covers, so a comparison
// against those bytes alone would find the field set in the document and
// empty in the signed form, and would refuse every document loaded into such
// a type.
func TestATargetThatDeclaresTheSignatureFieldLoads(t *testing.T) {
	priv, pub := signingKey(t)
	fields := map[string]interface{}{"name": "edge"}

	for name, doc := range map[string]struct{ file, text, sig string }{
		"yaml": {"c.yaml", "name: edge\nsignature: ", signYAML(t, priv, fields)},
		"json": {"c.json", `{"name":"edge","signature":"`, signJSON(t, priv, fields)},
	} {
		text := doc.text + doc.sig + "\n"
		if name == "json" {
			text = doc.text + doc.sig + `"}`
		}
		path := filepath.Join(t.TempDir(), doc.file)
		if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
			t.Fatalf("write: %v", err)
		}
		var got selfDescribing
		err := cfgload.Load(&got, cfgload.LoadOptions{URL: "file://" + path, VerifySignature: true, PublicKey: pub})
		if err != nil || got.Name != "edge" || got.Signature != doc.sig {
			t.Errorf("%s: a target declaring the signature field loaded as %+v, %v", name, got, err)
		}
	}
}
