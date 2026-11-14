package cfgload_test

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/bbmumford/cfgload"
)

type judgedDoc struct {
	Name string `yaml:"name" json:"name"`
}

func (d *judgedDoc) Validate() error {
	if d.Name == "" {
		return errors.New("judgedDoc: no name")
	}
	return nil
}

var judgedExpiry = time.Date(2031, 2, 3, 4, 5, 6, 0, time.UTC)

// stampedFields is a document's fields with a header in the signed bytes.
func stampedFields(name string, version uint64) map[string]interface{} {
	return map[string]interface{}{
		"name": name,
		cfgload.HeaderKey: map[string]interface{}{
			"subject":   "doc/one",
			"version":   version,
			"expiresAt": judgedExpiry,
		},
	}
}

// signedYAMLDoc returns fields as a YAML document signed the way the loader
// verifies.
func signedYAMLDoc(t *testing.T, fields map[string]interface{}, sign func(map[string]interface{}) string) string {
	t.Helper()
	doc := map[string]interface{}{}
	for k, v := range fields {
		doc[k] = v
	}
	doc["signature"] = sign(fields)
	out, err := yaml.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	return string(out)
}

// The judge is handed the header the signed bytes carry, the key the signature
// verified under and the decoded value, before the target is set; a refusal
// leaves the target as it was and is returned.
func TestTheJudgeIsHandedTheVerifiedHeaderBeforeTheTargetIsSet(t *testing.T) {
	priv, pub := signingKey(t)
	sign := func(f map[string]interface{}) string { return signYAML(t, priv, f) }
	path := filepath.Join(t.TempDir(), "doc.yaml")
	if err := os.WriteFile(path, []byte(signedYAMLDoc(t, stampedFields("a", 7), sign)), 0o600); err != nil {
		t.Fatal(err)
	}
	var got []cfgload.Verified
	judge := func(v cfgload.Verified) error {
		got = append(got, v)
		return nil
	}
	var target judgedDoc
	if err := cfgload.Load(&target, cfgload.LoadOptions{URL: "file://" + path, VerifySignature: true, PublicKey: pub, Judge: judge}); err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("judge called %d times; want once", len(got))
	}
	v := got[0]
	if !v.Stamped || v.Header.Subject != "doc/one" || v.Header.Version != 7 || !v.Header.ExpiresAt.Equal(judgedExpiry) {
		t.Fatalf("judge handed %+v; want the stamped header", v)
	}
	if v.PublicKey != pub {
		t.Fatal("judge handed another key than the one the signature verified under")
	}
	if d, ok := v.Value.(*judgedDoc); !ok || d.Name != "a" || d == &target {
		t.Fatalf("judge handed value %#v; want a decoded *judgedDoc that is not the target", v.Value)
	}

	refusal := errors.New("refused by the judge")
	target = judgedDoc{Name: "before"}
	err := cfgload.Load(&target, cfgload.LoadOptions{URL: "file://" + path, VerifySignature: true, PublicKey: pub,
		Judge: func(cfgload.Verified) error { return refusal }})
	if !errors.Is(err, refusal) {
		t.Fatalf("Load returned %v; want the judge's refusal", err)
	}
	if target.Name != "before" {
		t.Fatalf("a refused document set the target to %+v", target)
	}
}

// A JSON document's header is read the same way, and a document with no
// header is handed as unstamped.
func TestTheJudgeReadsAJSONHeaderAndAnUnstampedDocument(t *testing.T) {
	priv, pub := signingKey(t)
	fields := stampedFields("j", 3)
	fields[cfgload.HeaderKey].(map[string]interface{})["expiresAt"] = judgedExpiry.Format(time.RFC3339)
	doc := map[string]interface{}{}
	for k, v := range fields {
		doc[k] = v
	}
	doc["signature"] = signJSON(t, priv, fields)
	body, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "doc.json")
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatal(err)
	}
	var seen cfgload.Verified
	var target judgedDoc
	if err := cfgload.Load(&target, cfgload.LoadOptions{URL: "file://" + path, VerifySignature: true, PublicKey: pub,
		Judge: func(v cfgload.Verified) error { seen = v; return nil }}); err != nil {
		t.Fatal(err)
	}
	if !seen.Stamped || seen.Header.Version != 3 || !seen.Header.ExpiresAt.Equal(judgedExpiry) {
		t.Fatalf("judge handed %+v; want the JSON header", seen)
	}

	plain := map[string]interface{}{"name": "p"}
	upath := filepath.Join(t.TempDir(), "plain.yaml")
	if err := os.WriteFile(upath, []byte(signedYAMLDoc(t, plain, func(f map[string]interface{}) string { return signYAML(t, priv, f) })), 0o600); err != nil {
		t.Fatal(err)
	}
	seen = cfgload.Verified{Stamped: true}
	if err := cfgload.Load(&target, cfgload.LoadOptions{URL: "file://" + upath, VerifySignature: true, PublicKey: pub,
		Judge: func(v cfgload.Verified) error { seen = v; return nil }}); err != nil {
		t.Fatal(err)
	}
	if seen.Stamped || seen.Header != (cfgload.Header{}) {
		t.Fatalf("judge handed %+v for a document with no header; want it unstamped", seen)
	}
}

// A judge on a load that verifies no signature is refused, since anyone who
// serves the document could write its header.
func TestAJudgeNeedsAVerifiedLoad(t *testing.T) {
	path := filepath.Join(t.TempDir(), "doc.yaml")
	if err := os.WriteFile(path, []byte("name: a\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	var target judgedDoc
	err := cfgload.Load(&target, cfgload.LoadOptions{URL: "file://" + path, Judge: func(cfgload.Verified) error { return nil }})
	if err == nil || !strings.Contains(err.Error(), "VerifySignature") {
		t.Fatalf("Load returned %v; want a refusal naming VerifySignature", err)
	}
}

// Every reload is judged: a re-read the judge refuses keeps the value in force.
func TestEveryReloadIsJudged(t *testing.T) {
	priv, pub := signingKey(t)
	sign := func(f map[string]interface{}) string { return signYAML(t, priv, f) }
	path := filepath.Join(t.TempDir(), "doc.yaml")
	if err := os.WriteFile(path, []byte(signedYAMLDoc(t, stampedFields("v2", 2), sign)), 0o600); err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	var versions []uint64
	refused := make(chan struct{}, 8)
	judge := func(v cfgload.Verified) error {
		mu.Lock()
		versions = append(versions, v.Header.Version)
		mu.Unlock()
		if v.Header.Version < 2 {
			return errors.New("below version 2")
		}
		return nil
	}
	var target judgedDoc
	if err := cfgload.Load(&target, cfgload.LoadOptions{URL: "file://" + path, VerifySignature: true, PublicKey: pub, Judge: judge,
		EnableReload: true, ReloadInterval: 15 * time.Millisecond,
		OnReloadError: func(error) {
			select {
			case refused <- struct{}{}:
			default:
			}
		}}); err != nil {
		t.Fatal(err)
	}
	defer cfgload.StopReload(&target)
	if err := os.WriteFile(path, []byte(signedYAMLDoc(t, stampedFields("v1", 1), sign)), 0o600); err != nil {
		t.Fatal(err)
	}
	select {
	case <-refused:
	case <-time.After(3 * time.Second):
		t.Fatal("the older re-read was never refused")
	}
	cfgload.StopReload(&target)
	if target.Name != "v2" {
		t.Fatalf("target %+v after the judge refused a reload; want v2 in force", target)
	}
}

// ParseHeader reads a header from either form, reports a document with none,
// and refuses a header that does not decode.
func TestParseHeader(t *testing.T) {
	h, ok, err := cfgload.ParseHeader([]byte("header:\n  subject: s\n  version: 4\n  expiresAt: 2031-02-03T04:05:06Z\nname: x\n"))
	if err != nil || !ok || h.Subject != "s" || h.Version != 4 || !h.ExpiresAt.Equal(judgedExpiry) {
		t.Fatalf("yaml: %+v %v %v", h, ok, err)
	}
	h, ok, err = cfgload.ParseHeader([]byte(`{"header":{"subject":"s","version":4,"expiresAt":"2031-02-03T04:05:06Z"}}`))
	if err != nil || !ok || h.Version != 4 || !h.ExpiresAt.Equal(judgedExpiry) {
		t.Fatalf("json: %+v %v %v", h, ok, err)
	}
	if _, ok, err := cfgload.ParseHeader([]byte("name: x\n")); err != nil || ok {
		t.Fatalf("no header: %v %v", ok, err)
	}
	if _, ok, err := cfgload.ParseHeader([]byte("header: null\nname: x\n")); err != nil || ok {
		t.Fatalf("null header: %v %v", ok, err)
	}
	if _, _, err := cfgload.ParseHeader([]byte("header:\n  version: many\n")); err == nil {
		t.Fatal("a header whose version is not a number decoded")
	}
}
