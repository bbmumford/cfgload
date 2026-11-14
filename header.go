package cfgload

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// HeaderKey is the top-level key under which a signed document carries its
// header. The header sits inside the bytes the signature covers, as every
// other key but the signature does.
const HeaderKey = "header"

// Header is what a document's signer stamps into the signed bytes: the subject
// the document is for, its version and when it stops being valid. cfgload
// parses it from the form the signature was verified over and hands it to
// LoadOptions.Judge; what any of it means is the judge's to decide.
type Header struct {
	Subject   string    `yaml:"subject" json:"subject"`
	Version   uint64    `yaml:"version" json:"version"`
	ExpiresAt time.Time `yaml:"expiresAt" json:"expiresAt"`
}

// Verified is what Load hands LoadOptions.Judge for one document: the header
// the verified bytes carry (Stamped is false, and Header zero, for a document
// that carries none), the base64 public key its signature verified under, and
// the value decoded from it, already filled from the environment and validated
// but not yet committed to the caller's target.
type Verified struct {
	Header    Header
	Stamped   bool
	PublicKey string
	Value     interface{}
}

// ParseHeader reads the header data carries, JSON or YAML: the header and
// true, or false when the document has no HeaderKey or it is null. It verifies
// nothing; Load calls it on the form the signature was checked over, and a
// signer calls it to read the header of a document it re-signs. A header that
// does not decode into Header is an error.
func ParseHeader(data []byte) (Header, bool, error) {
	trimmed := strings.TrimSpace(string(data))
	if strings.HasPrefix(trimmed, "{") || strings.HasPrefix(trimmed, "[") {
		var doc map[string]json.RawMessage
		if err := json.Unmarshal(data, &doc); err != nil {
			return Header{}, false, fmt.Errorf("the document does not decode: %w", err)
		}
		msg, ok := doc[HeaderKey]
		if !ok || string(msg) == "null" {
			return Header{}, false, nil
		}
		var h Header
		if err := json.Unmarshal(msg, &h); err != nil {
			return Header{}, false, fmt.Errorf("the document's %s does not decode: %w", HeaderKey, err)
		}
		return h, true, nil
	}
	var holder map[string]interface{}
	if err := yaml.Unmarshal(data, &holder); err != nil {
		return Header{}, false, fmt.Errorf("the document does not decode: %w", err)
	}
	raw, ok := holder[HeaderKey]
	if !ok || raw == nil {
		return Header{}, false, nil
	}
	encoded, err := yaml.Marshal(raw)
	if err != nil {
		return Header{}, false, fmt.Errorf("the document's %s does not decode: %w", HeaderKey, err)
	}
	var h Header
	if err := yaml.Unmarshal(encoded, &h); err != nil {
		return Header{}, false, fmt.Errorf("the document's %s does not decode: %w", HeaderKey, err)
	}
	return h, true, nil
}
