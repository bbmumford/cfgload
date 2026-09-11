package meshcfg

import (
	"fmt"
	"strings"
)

// Bootstrap is the signed seed manifest a node reads to find its first peers
// without DNS. cfgload calls Validate when it loads one, so a manifest with no
// usable seed stops the node at boot instead of leaving it with no one to dial.
type Bootstrap struct {
	Version   int      `json:"version" yaml:"version"`
	Epoch     int64    `json:"epoch" yaml:"epoch"`         // Unix seconds the manifest was issued, for freshness
	Seeds     []string `json:"seeds" yaml:"seeds"`         // "host:port" entries
	Signature string   `json:"signature" yaml:"signature"` // issuer signature over the canonical JSON
}

// Validate requires a positive version and epoch and at least one seed, each
// non-empty and in host:port form.
func (b *Bootstrap) Validate() error {
	if b.Version <= 0 {
		return fmt.Errorf("version must be > 0, got %d", b.Version)
	}
	if b.Epoch <= 0 {
		return fmt.Errorf("epoch must be > 0, got %d", b.Epoch)
	}
	if len(b.Seeds) == 0 {
		return fmt.Errorf("at least one seed peer is required")
	}
	for i, seed := range b.Seeds {
		if seed == "" {
			return fmt.Errorf("seed[%d] is empty", i)
		}
		if len(seed) < 3 || !strings.Contains(seed, ":") {
			return fmt.Errorf("seed[%d] has invalid format (expected host:port): %s", i, seed)
		}
	}
	return nil
}
