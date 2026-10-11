package verify

import (
	"bytes"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"gopkg.in/yaml.v3"
)

// SignedConfig represents a configuration with an embedded signature
type SignedConfig struct {
	Signature string `yaml:"signature,omitempty" json:"signature,omitempty"`
}

// VerifySignature verifies an Ed25519 signature on configuration data
// The signature field is extracted from the config, removed, and the remaining
// data is verified against the signature using the provided public key
func VerifySignature(data []byte, publicKeyBase64 string) error {
	_, _, err := Reread(data, publicKeyBase64)
	return err
}

// Reread verifies the document's signature as VerifySignature does and
// returns the document as that check read it: decoded into a map and
// marshalled again in the document's format, the signature field included.
// isJSON reports that format. The signature covers these bytes less the
// signature field. The loader fills a second target from them and compares it
// with the one filled from the document, because this form does not keep how
// a value was written and the document's own bytes do. The signature field
// stays in so that a target declaring it compares equal.
func Reread(data []byte, publicKeyBase64 string) (reread []byte, isJSON bool, err error) {
	if publicKeyBase64 == "" {
		return nil, false, fmt.Errorf("public key is required for signature verification")
	}

	// Decode public key
	publicKeyBytes, err := base64.StdEncoding.DecodeString(publicKeyBase64)
	if err != nil {
		return nil, false, fmt.Errorf("failed to decode public key: %w", err)
	}

	if len(publicKeyBytes) != ed25519.PublicKeySize {
		return nil, false, fmt.Errorf("invalid public key size: expected %d bytes, got %d",
			ed25519.PublicKeySize, len(publicKeyBytes))
	}

	publicKey := ed25519.PublicKey(publicKeyBytes)

	// Try to parse as JSON first, then YAML
	var signature string
	var dataWithoutSig []byte
	var whole []byte

	// Detect format
	trimmed := strings.TrimSpace(string(data))
	if strings.HasPrefix(trimmed, "{") || strings.HasPrefix(trimmed, "[") {
		// JSON format
		isJSON = true
		signature, dataWithoutSig, whole, err = extractSignatureJSON(data)
	} else {
		// YAML format
		signature, dataWithoutSig, whole, err = extractSignatureYAML(data)
	}

	if err != nil {
		return nil, false, fmt.Errorf("failed to extract signature: %w", err)
	}

	if signature == "" {
		return nil, false, fmt.Errorf("signature field not found in config")
	}

	// Decode signature
	signatureBytes, err := base64.StdEncoding.DecodeString(signature)
	if err != nil {
		return nil, false, fmt.Errorf("failed to decode signature: %w", err)
	}

	// Verify signature
	if !ed25519.Verify(publicKey, dataWithoutSig, signatureBytes) {
		return nil, false, fmt.Errorf("signature verification failed")
	}

	return whole, isJSON, nil
}

// extractSignatureJSON extracts signature from JSON and returns data without
// it, and the whole decoded document marshalled again with it.
func extractSignatureJSON(data []byte) (string, []byte, []byte, error) {
	if err := refuseRepeatedKeys(data); err != nil {
		return "", nil, nil, err
	}
	// Parse to extract signature
	var temp map[string]interface{}
	if err := json.Unmarshal(data, &temp); err != nil {
		return "", nil, nil, fmt.Errorf("failed to parse JSON: %w", err)
	}

	signature, ok := temp["signature"].(string)
	if !ok {
		return "", nil, nil, fmt.Errorf("signature field not found or not a string")
	}
	whole, err := json.Marshal(temp)
	if err != nil {
		return "", nil, nil, fmt.Errorf("failed to marshal data: %w", err)
	}

	// Remove signature and re-marshal
	delete(temp, "signature")
	dataWithoutSig, err := json.Marshal(temp)
	if err != nil {
		return "", nil, nil, fmt.Errorf("failed to marshal data without signature: %w", err)
	}

	return signature, dataWithoutSig, whole, nil
}

// extractSignatureYAML extracts signature from YAML and returns data without
// it, and the whole decoded document marshalled again with it.
func extractSignatureYAML(data []byte) (string, []byte, []byte, error) {
	// Parse to extract signature
	var temp map[string]interface{}
	if err := yaml.Unmarshal(data, &temp); err != nil {
		return "", nil, nil, fmt.Errorf("failed to parse YAML: %w", err)
	}

	signature, ok := temp["signature"].(string)
	if !ok {
		return "", nil, nil, fmt.Errorf("signature field not found or not a string")
	}
	whole, err := yaml.Marshal(temp)
	if err != nil {
		return "", nil, nil, fmt.Errorf("failed to marshal data: %w", err)
	}

	// Remove signature and re-marshal
	delete(temp, "signature")
	dataWithoutSig, err := yaml.Marshal(temp)
	if err != nil {
		return "", nil, nil, fmt.Errorf("failed to marshal data without signature: %w", err)
	}

	return signature, dataWithoutSig, whole, nil
}

// refuseRepeatedKeys returns an error when any object in the JSON document
// names one key twice, or two keys that differ only in letter case.
//
// VerifySignature checks the signature over the document decoded into a map,
// and the loader then fills its target from the document's own bytes. The two
// readings agree only while every key of an object is written once: a map
// keeps the last value of a repeated key, while encoding/json fills an object
// or a list element the target already holds, keeping what an earlier
// occurrence wrote; and it matches a struct field without regard to letter
// case, so two spellings of one key fill one field in the order they are
// written, an order the sorted map does not carry. A document with either is
// one whose loaded value its signature does not determine, so it is refused
// before the signature is read.
func refuseRepeatedKeys(data []byte) error {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	if err := walkJSONValue(dec, "document"); err != nil {
		return err
	}
	if _, err := dec.Token(); err != io.EOF {
		return fmt.Errorf("JSON document carries data after its value")
	}
	return nil
}

// walkJSONValue reads one value from dec, descending into objects and lists,
// and returns an error naming the first object that repeats a key. where is
// that value's place in the document, for the error text.
func walkJSONValue(dec *json.Decoder, where string) error {
	tok, err := dec.Token()
	if err != nil {
		return fmt.Errorf("failed to parse JSON: %w", err)
	}
	delim, ok := tok.(json.Delim)
	if !ok {
		return nil
	}
	switch delim {
	case '{':
		var seen []string
		for dec.More() {
			keyTok, err := dec.Token()
			if err != nil {
				return fmt.Errorf("failed to parse JSON: %w", err)
			}
			key, _ := keyTok.(string)
			for _, prior := range seen {
				if strings.EqualFold(prior, key) {
					return fmt.Errorf("JSON key %q is written twice in %s (as %q and %q): the signature does not determine which value loads", key, where, prior, key)
				}
			}
			seen = append(seen, key)
			if err := walkJSONValue(dec, where+"."+key); err != nil {
				return err
			}
		}
	case '[':
		for dec.More() {
			if err := walkJSONValue(dec, where+"[]"); err != nil {
				return err
			}
		}
	default:
		return nil
	}
	// The closing delimiter of the object or list just walked.
	if _, err := dec.Token(); err != nil {
		return fmt.Errorf("failed to parse JSON: %w", err)
	}
	return nil
}
