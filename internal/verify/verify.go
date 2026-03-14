package verify

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"fmt"
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
	if publicKeyBase64 == "" {
		return fmt.Errorf("public key is required for signature verification")
	}

	// Decode public key
	publicKeyBytes, err := base64.StdEncoding.DecodeString(publicKeyBase64)
	if err != nil {
		return fmt.Errorf("failed to decode public key: %w", err)
	}

	if len(publicKeyBytes) != ed25519.PublicKeySize {
		return fmt.Errorf("invalid public key size: expected %d bytes, got %d",
			ed25519.PublicKeySize, len(publicKeyBytes))
	}

	publicKey := ed25519.PublicKey(publicKeyBytes)

	// Try to parse as JSON first, then YAML
	var signature string
	var dataWithoutSig []byte

	// Detect format
	trimmed := strings.TrimSpace(string(data))
	if strings.HasPrefix(trimmed, "{") || strings.HasPrefix(trimmed, "[") {
		// JSON format
		signature, dataWithoutSig, err = extractSignatureJSON(data)
	} else {
		// YAML format
		signature, dataWithoutSig, err = extractSignatureYAML(data)
	}

	if err != nil {
		return fmt.Errorf("failed to extract signature: %w", err)
	}

	if signature == "" {
		return fmt.Errorf("signature field not found in config")
	}

	// Decode signature
	signatureBytes, err := base64.StdEncoding.DecodeString(signature)
	if err != nil {
		return fmt.Errorf("failed to decode signature: %w", err)
	}

	// Verify signature
	if !ed25519.Verify(publicKey, dataWithoutSig, signatureBytes) {
		return fmt.Errorf("signature verification failed")
	}

	return nil
}

// extractSignatureJSON extracts signature from JSON and returns data without it
func extractSignatureJSON(data []byte) (string, []byte, error) {
	// Parse to extract signature
	var temp map[string]interface{}
	if err := json.Unmarshal(data, &temp); err != nil {
		return "", nil, fmt.Errorf("failed to parse JSON: %w", err)
	}

	signature, ok := temp["signature"].(string)
	if !ok {
		return "", nil, fmt.Errorf("signature field not found or not a string")
	}

	// Remove signature and re-marshal
	delete(temp, "signature")
	dataWithoutSig, err := json.Marshal(temp)
	if err != nil {
		return "", nil, fmt.Errorf("failed to marshal data without signature: %w", err)
	}

	return signature, dataWithoutSig, nil
}

// extractSignatureYAML extracts signature from YAML and returns data without it
func extractSignatureYAML(data []byte) (string, []byte, error) {
	// Parse to extract signature
	var temp map[string]interface{}
	if err := yaml.Unmarshal(data, &temp); err != nil {
		return "", nil, fmt.Errorf("failed to parse YAML: %w", err)
	}

	signature, ok := temp["signature"].(string)
	if !ok {
		return "", nil, fmt.Errorf("signature field not found or not a string")
	}

	// Remove signature and re-marshal
	delete(temp, "signature")
	dataWithoutSig, err := yaml.Marshal(temp)
	if err != nil {
		return "", nil, fmt.Errorf("failed to marshal data without signature: %w", err)
	}

	return signature, dataWithoutSig, nil
}
