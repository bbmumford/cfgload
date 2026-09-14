/*
 * Copyright (c) 2026 ORBTR AI Pty Ltd. All Rights Reserved.
 */

// Package encseal seals and opens values under a per-domain key, using whatever
// KeyProvider the consuming application supplies.
//
// It knows nothing about whose data it protects: a domain is a free-form label
// the application chooses ("sessions", "pii"), and this package never interprets
// it. There is no owner, tenant or partition here.
//
// The split from key SELECTION is deliberate. enccfg already answers which key a
// domain currently seals with and which older versions must still open; an
// application whose key material lives elsewhere — a cloud key vault, say —
// answers the same questions from that source instead. This package performs the
// crypto and asks a KeyProvider for the rest, so neither choice is baked in.
package encseal

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
)

// KeyProvider answers the three questions sealing needs about a domain's keys.
//
// A provider is the seam between this package and wherever key material lives.
// enccfg backs one implementation; a vault-backed application supplies another.
// Every method takes ctx because a remote provider has to be cancellable — a
// seal that cannot be abandoned holds a request open on an unreachable vault.
type KeyProvider interface {
	// CurrentVersion names the version NEW data is sealed with. An empty
	// version means the domain is not configured, which Seal refuses rather
	// than treating as a default.
	CurrentVersion(ctx context.Context, domain string) (string, error)

	// Key returns raw material for one version. found is false for an unknown
	// domain or version, which callers must not read as an empty key: sealing
	// under one would produce data nothing can open.
	Key(ctx context.Context, domain, version string) (material []byte, found bool, err error)

	// Sealable reports whether a version may seal NEW data. It gates sealing
	// ONLY — a retired or compromised version must still OPEN, which is the
	// entire reason old versions are kept.
	Sealable(ctx context.Context, domain, version string) bool
}

// Refusals Seal and Open return.
var (
	ErrNoDomain     = errors.New("encseal: domain is not configured")
	ErrNoKey        = errors.New("encseal: no key material for that version")
	ErrNotSealable  = errors.New("encseal: version may not seal new data")
	ErrMalformed    = errors.New("encseal: value is not a sealed blob")
	ErrVersionDelim = errors.New("encseal: key version contains the field delimiter")
	ErrNoProvider   = errors.New("encseal: no key provider configured")
)

// prefix marks a sealed blob and names the format. Open refuses anything else
// rather than guessing, so a plaintext value that reaches Open is reported
// instead of being returned as though it had been sealed.
const prefix = "es1"

const delim = ":"

// Sealer seals under the keys its provider answers for. The zero value refuses
// every call, because a Sealer with no provider has no key material and
// returning plaintext would be the dangerous reading of that.
type Sealer struct {
	Provider KeyProvider
}

// Seal encrypts plaintext under the domain's current key and returns a blob that
// names the version that wrote it.
//
// The version travels in the blob because Open must resolve the key that sealed
// it, and a provider keeps older versions precisely so older data stays
// readable. A format that could not say which key wrote it would make every
// rotation a data-loss event.
func (s Sealer) Seal(ctx context.Context, plaintext, domain string) (string, error) {
	if s.Provider == nil {
		return "", ErrNoProvider
	}
	version, err := s.Provider.CurrentVersion(ctx, domain)
	if err != nil {
		return "", err
	}
	if version == "" {
		return "", fmt.Errorf("%w: %q", ErrNoDomain, domain)
	}
	// The blob is delimiter-separated and the version is not escaped, so a
	// version containing the delimiter would produce a blob Open would split
	// wrongly. Refusing here names the config error; accepting it would surface
	// later as an unopenable value.
	if strings.Contains(version, delim) {
		return "", fmt.Errorf("%w: %q", ErrVersionDelim, version)
	}
	if !s.Provider.Sealable(ctx, domain, version) {
		return "", fmt.Errorf("%w: %s/%s", ErrNotSealable, domain, version)
	}
	aead, err := s.aead(ctx, domain, version)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	sealed := aead.Seal(nonce, nonce, []byte(plaintext), aad(domain, version))
	return prefix + delim + version + delim + base64.RawURLEncoding.EncodeToString(sealed), nil
}

// Open decrypts a blob sealed for this domain.
//
// It resolves the key from the version the blob names, NOT from the domain's
// current version, so data written before a rotation still opens. Sealable is
// deliberately not consulted: a version retired from sealing must still open, or
// retiring it would destroy everything written under it.
func (s Sealer) Open(ctx context.Context, blob, domain string) (string, error) {
	if s.Provider == nil {
		return "", ErrNoProvider
	}
	version, payload, err := split(blob)
	if err != nil {
		return "", err
	}
	aead, err := s.aead(ctx, domain, version)
	if err != nil {
		return "", err
	}
	raw, err := base64.RawURLEncoding.DecodeString(payload)
	if err != nil {
		return "", fmt.Errorf("%w: %v", ErrMalformed, err)
	}
	if len(raw) < aead.NonceSize() {
		return "", ErrMalformed
	}
	nonce, ct := raw[:aead.NonceSize()], raw[aead.NonceSize():]
	plaintext, err := aead.Open(nil, nonce, ct, aad(domain, version))
	if err != nil {
		return "", err
	}
	return string(plaintext), nil
}

// ShouldReSeal reports whether a blob was sealed under a version the domain no
// longer seals with, so a caller can rewrite it under the current key.
//
// It answers on the blob's own version, and says nothing about whether the value
// can still be opened — a re-seal is a maintenance action, not a failure.
func (s Sealer) ShouldReSeal(ctx context.Context, blob, domain string) (bool, error) {
	if s.Provider == nil {
		return false, ErrNoProvider
	}
	version, _, err := split(blob)
	if err != nil {
		return false, err
	}
	current, err := s.Provider.CurrentVersion(ctx, domain)
	if err != nil {
		return false, err
	}
	if current == "" {
		return false, fmt.Errorf("%w: %q", ErrNoDomain, domain)
	}
	return version != current, nil
}

// aead derives the cipher for one domain+version.
//
// The provider's material is hashed to a fixed 32 bytes rather than used
// directly, so material of any length or encoding yields a valid AES-256 key.
// Without it, a provider returning a 31-byte or hex-encoded secret would fail at
// the first seal on a length error that says nothing about its cause.
func (s Sealer) aead(ctx context.Context, domain, version string) (cipher.AEAD, error) {
	material, found, err := s.Provider.Key(ctx, domain, version)
	if err != nil {
		return nil, err
	}
	if !found || len(material) == 0 {
		return nil, fmt.Errorf("%w: %s/%s", ErrNoKey, domain, version)
	}
	sum := sha256.Sum256(material)
	block, err := aes.NewCipher(sum[:])
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

// aad binds a blob to the domain AND version that sealed it. Without it a blob
// sealed for one domain would open as another, so moving ciphertext between
// domains would be undetectable.
func aad(domain, version string) []byte {
	return []byte(domain + delim + version)
}

// split reads the version and payload out of a blob, refusing anything that does
// not carry this format's prefix.
func split(blob string) (version, payload string, err error) {
	parts := strings.SplitN(blob, delim, 3)
	if len(parts) != 3 || parts[0] != prefix || parts[1] == "" || parts[2] == "" {
		return "", "", ErrMalformed
	}
	return parts[1], parts[2], nil
}
