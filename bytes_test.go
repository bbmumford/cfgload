package cfgload_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/bbmumford/cfgload"
)

// A document handed as bytes verifies under whichever of the caller's keys signed
// it, and the judge is handed that key and the signed header before the target is
// set; a document no key verifies, a tampered one and a refused one leave the
// target as it was, and a load without a judge or a key is refused.
func TestLoadBytesVerifiesUnderTheCallersKeysAndIsJudged(t *testing.T) {
	priv, pub := signingKey(t)
	_, other := signingKey(t)
	doc := []byte(signedYAMLDoc(t, stampedFields("a", 9), func(f map[string]interface{}) string { return signYAML(t, priv, f) }))

	var handed cfgload.Verified
	judge := func(v cfgload.Verified) error { handed = v; return nil }
	var target judgedDoc
	if err := cfgload.LoadBytes(&target, doc, cfgload.BytesOptions{PublicKeys: []string{other, pub}, Judge: judge}); err != nil {
		t.Fatal(err)
	}
	if target.Name != "a" || handed.PublicKey != pub || handed.Header.Version != 9 || !handed.Stamped {
		t.Fatalf("target %+v, judge handed %+v", target, handed)
	}

	before := judgedDoc{Name: "before"}
	for name, try := range map[string]struct {
		data []byte
		opts cfgload.BytesOptions
	}{
		"no key verifies": {doc, cfgload.BytesOptions{PublicKeys: []string{other}, Judge: judge}},
		"tampered":        {[]byte(strings.Replace(string(doc), "name: a", "name: b", 1)), cfgload.BytesOptions{PublicKeys: []string{pub}, Judge: judge}},
		"refused":         {doc, cfgload.BytesOptions{PublicKeys: []string{pub}, Judge: func(cfgload.Verified) error { return errors.New("no") }}},
		"no judge":        {doc, cfgload.BytesOptions{PublicKeys: []string{pub}}},
		"no key":          {doc, cfgload.BytesOptions{Judge: judge}},
	} {
		got := before
		if err := cfgload.LoadBytes(&got, try.data, try.opts); err == nil {
			t.Errorf("%s: loaded", name)
		}
		if got != before {
			t.Errorf("%s: the target became %+v", name, got)
		}
	}
}
