/*
 * Copyright (c) 2026 ORBTR AI Pty Ltd. All Rights Reserved.
 */

package encseal

import (
	"context"
	"encoding/base64"
	"errors"
	"testing"

	"github.com/bbmumford/cfgload/enccfg"
)

// unwrapTo is an Unwrapper that records the ciphertext it was handed, so a test
// can assert the DECODED bytes reach it rather than the stored string.
type unwrapTo struct {
	key  []byte
	err  error
	seen []byte
}

func (u *unwrapTo) Unwrap(_ context.Context, wrapped []byte) ([]byte, error) {
	u.seen = append([]byte(nil), wrapped...)
	return u.key, u.err
}

// registerKeys puts one domain's keys in the registry. enccfg registration is
// process-global, so every case here uses its own domain rather than re-
// registering and racing the others.
func registerKeys(t *testing.T, domain string, k enccfg.Key) {
	t.Helper()
	cfg := &enccfg.Config{Domains: map[string]enccfg.DomainKeys{
		domain: {CurrentVersion: "v1", Keys: map[string]enccfg.Key{"v1": k}},
	}}
	if err := enccfg.Register(cfg); err != nil {
		t.Fatalf("register: %v", err)
	}
}

// Plaintext material passes through untouched — it is already the key.
func TestEnccfgProvider_PlaintextMaterialIsUsedDirectly(t *testing.T) {
	registerKeys(t, "plain", enccfg.Key{Key: "raw-key-material", Status: "active"})

	got, found, err := EnccfgProvider{}.Key(context.Background(), "plain", "v1")
	if err != nil || !found {
		t.Fatalf("Key = (%q,%v,%v), want the material found", got, found, err)
	}
	if string(got) != "raw-key-material" {
		t.Fatalf("material = %q, want it passed through unchanged", got)
	}
}

// The deny leg: wrapped material is sealed by a KEK this package does not hold,
// so without an Unwrapper it must be refused rather than used as a key. Sealing
// under a wrapped blob would produce data nothing can open.
func TestEnccfgProvider_WrappedWithoutUnwrapperIsRefused(t *testing.T) {
	registerKeys(t, "wrapped-none", enccfg.Key{
		WrappedKey: base64.StdEncoding.EncodeToString([]byte("kek-ciphertext")),
		Status:     "active",
	})

	_, found, err := EnccfgProvider{}.Key(context.Background(), "wrapped-none", "v1")
	if found {
		t.Fatal("wrapped material was reported as usable key material")
	}
	if !errors.Is(err, ErrWrapped) {
		t.Fatalf("err = %v, want ErrWrapped", err)
	}
}

// The Unwrapper receives DECODED ciphertext. enccfg stores it base64; decoding
// here keeps that storage detail out of every implementation.
func TestEnccfgProvider_UnwrapperReceivesDecodedCiphertext(t *testing.T) {
	registerKeys(t, "wrapped-ok", enccfg.Key{
		WrappedKey: base64.StdEncoding.EncodeToString([]byte("kek-ciphertext")),
		Status:     "active",
	})
	u := &unwrapTo{key: []byte("unwrapped-key")}

	got, found, err := EnccfgProvider{Unwrapper: u}.Key(context.Background(), "wrapped-ok", "v1")
	if err != nil || !found {
		t.Fatalf("Key = (%q,%v,%v), want the unwrapped key", got, found, err)
	}
	if string(got) != "unwrapped-key" {
		t.Fatalf("key = %q, want the Unwrapper's answer", got)
	}
	if string(u.seen) != "kek-ciphertext" {
		t.Fatalf("Unwrapper saw %q, want the DECODED ciphertext, not the stored base64", u.seen)
	}
}

// An unwrap failure must surface, never be read as "no key material".
func TestEnccfgProvider_UnwrapFailureSurfaces(t *testing.T) {
	registerKeys(t, "wrapped-err", enccfg.Key{
		WrappedKey: base64.StdEncoding.EncodeToString([]byte("kek-ciphertext")),
		Status:     "active",
	})

	_, found, err := EnccfgProvider{Unwrapper: &unwrapTo{err: errors.New("kms unreachable")}}.
		Key(context.Background(), "wrapped-err", "v1")
	if found || err == nil {
		t.Fatalf("Key = (_,%v,%v), want the unwrap failure surfaced", found, err)
	}
}

// An unknown domain reports absent rather than an empty key, which a caller must
// not seal under.
func TestEnccfgProvider_UnknownDomainIsAbsentNotEmpty(t *testing.T) {
	_, found, err := EnccfgProvider{}.Key(context.Background(), "never-registered", "v1")
	if found || err != nil {
		t.Fatalf("Key = (_,%v,%v), want absent with no error", found, err)
	}
}
