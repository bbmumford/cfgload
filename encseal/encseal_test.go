/*
 * Copyright (c) 2026 ORBTR AI Pty Ltd. All Rights Reserved.
 */

package encseal

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// keys is a KeyProvider backed by a literal table, so a test states the key
// state it is exercising rather than arranging a registry to imply it.
type keys struct {
	current  map[string]string
	material map[string]string
	sealable map[string]bool
	curErr   error
	keyErr   error
}

func (k keys) CurrentVersion(_ context.Context, domain string) (string, error) {
	return k.current[domain], k.curErr
}

func (k keys) Key(_ context.Context, domain, version string) ([]byte, bool, error) {
	if k.keyErr != nil {
		return nil, false, k.keyErr
	}
	m, ok := k.material[domain+"/"+version]
	return []byte(m), ok, nil
}

func (k keys) Sealable(_ context.Context, domain, version string) bool {
	v, ok := k.sealable[domain+"/"+version]
	return ok && v
}

func provider() keys {
	return keys{
		current:  map[string]string{"sessions": "v2"},
		material: map[string]string{"sessions/v1": "old-key-material", "sessions/v2": "new-key-material"},
		sealable: map[string]bool{"sessions/v2": true},
	}
}

func ctx() context.Context { return context.Background() }

func TestSealOpensBackToTheSameValue(t *testing.T) {
	s := Sealer{Provider: provider()}
	blob, err := s.Seal(ctx(), "hello", "sessions")
	if err != nil {
		t.Fatalf("seal: %v", err)
	}
	if blob == "hello" || !strings.HasPrefix(blob, prefix+delim) {
		t.Fatalf("blob %q does not look sealed", blob)
	}
	got, err := s.Open(ctx(), blob, "sessions")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if got != "hello" {
		t.Fatalf("opened %q, want hello", got)
	}
}

// The blob must name the version that sealed it, or a rotation makes older data
// unopenable.
func TestSealedBlobNamesItsVersion(t *testing.T) {
	blob, err := Sealer{Provider: provider()}.Seal(ctx(), "hello", "sessions")
	if err != nil {
		t.Fatal(err)
	}
	version, _, err := split(blob)
	if err != nil {
		t.Fatal(err)
	}
	if version != "v2" {
		t.Fatalf("blob names version %q, want the domain's current v2", version)
	}
}

// A version retired from SEALING must still OPEN, which is the whole reason old
// versions are kept. Sealable gates writing only.
func TestOpenUsesTheBlobsVersionNotTheCurrentOne(t *testing.T) {
	p := provider()
	// Seal under v1 by making it briefly the current, sealable version.
	p.current = map[string]string{"sessions": "v1"}
	p.sealable = map[string]bool{"sessions/v1": true}
	blob, err := Sealer{Provider: p}.Seal(ctx(), "older", "sessions")
	if err != nil {
		t.Fatal(err)
	}

	// Now v2 is current and v1 seals nothing — the blob must still open.
	got, err := Sealer{Provider: provider()}.Open(ctx(), blob, "sessions")
	if err != nil {
		t.Fatalf("a retired version failed to OPEN, so rotation would destroy data: %v", err)
	}
	if got != "older" {
		t.Fatalf("opened %q, want older", got)
	}
}

// The deny leg for writing: a version the provider will not seal with must be
// refused rather than used.
func TestSealRefusesAnUnsealableVersion(t *testing.T) {
	p := provider()
	p.sealable = map[string]bool{}
	_, err := Sealer{Provider: p}.Seal(ctx(), "hello", "sessions")
	if !errors.Is(err, ErrNotSealable) {
		t.Fatalf("err = %v, want ErrNotSealable", err)
	}
}

func TestSealRefusesAnUnconfiguredDomain(t *testing.T) {
	_, err := Sealer{Provider: provider()}.Seal(ctx(), "hello", "unknown")
	if !errors.Is(err, ErrNoDomain) {
		t.Fatalf("err = %v, want ErrNoDomain", err)
	}
}

// A domain with no material must refuse, never seal under an empty key.
func TestSealRefusesWhenMaterialIsMissing(t *testing.T) {
	p := provider()
	p.material = map[string]string{}
	_, err := Sealer{Provider: p}.Seal(ctx(), "hello", "sessions")
	if !errors.Is(err, ErrNoKey) {
		t.Fatalf("err = %v, want ErrNoKey", err)
	}
}

// A blob sealed for one domain must not open as another. Without the domain in
// the AEAD's additional data, moving ciphertext between domains is undetectable.
func TestABlobDoesNotOpenUnderAnotherDomain(t *testing.T) {
	p := provider()
	p.current["other"] = "v2"
	p.material["other/v2"] = "new-key-material" // same key material on purpose
	p.sealable["other/v2"] = true

	blob, err := Sealer{Provider: p}.Seal(ctx(), "hello", "sessions")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := (Sealer{Provider: p}).Open(ctx(), blob, "other"); err == nil {
		t.Fatal("a blob sealed for one domain opened as another, even with identical key material")
	}
}

// Plaintext reaching Open must be reported, not returned as though it had been
// sealed — otherwise an unsealed field reads as a successfully opened one.
func TestOpenRefusesSomethingThatWasNeverSealed(t *testing.T) {
	for _, blob := range []string{"hello", "", "es1:", "es1:v2", "xx:v2:payload", "es1::payload"} {
		if _, err := (Sealer{Provider: provider()}).Open(ctx(), blob, "sessions"); !errors.Is(err, ErrMalformed) {
			t.Fatalf("Open(%q) err = %v, want ErrMalformed", blob, err)
		}
	}
}

// A tampered payload must fail authentication rather than yield altered plaintext.
func TestATamperedBlobDoesNotOpen(t *testing.T) {
	s := Sealer{Provider: provider()}
	blob, err := s.Seal(ctx(), "hello", "sessions")
	if err != nil {
		t.Fatal(err)
	}
	tampered := blob[:len(blob)-1] + string(flip(blob[len(blob)-1]))
	if _, err := s.Open(ctx(), tampered, "sessions"); err == nil {
		t.Fatal("a tampered blob opened")
	}
}

func flip(b byte) byte {
	if b == 'A' {
		return 'B'
	}
	return 'A'
}

func TestShouldReSealComparesTheBlobsVersionToCurrent(t *testing.T) {
	p := provider()
	p.current = map[string]string{"sessions": "v1"}
	p.sealable = map[string]bool{"sessions/v1": true}
	old, err := Sealer{Provider: p}.Seal(ctx(), "x", "sessions")
	if err != nil {
		t.Fatal(err)
	}
	fresh, err := Sealer{Provider: provider()}.Seal(ctx(), "x", "sessions")
	if err != nil {
		t.Fatal(err)
	}
	s := Sealer{Provider: provider()} // current is v2
	if got, _ := s.ShouldReSeal(ctx(), old, "sessions"); !got {
		t.Fatal("a blob under a superseded version should re-seal")
	}
	if got, _ := s.ShouldReSeal(ctx(), fresh, "sessions"); got {
		t.Fatal("a blob under the current version should not re-seal")
	}
}

// The zero value has no key material; returning plaintext would be the dangerous
// reading of that.
func TestZeroSealerRefusesEveryCall(t *testing.T) {
	var s Sealer
	if _, err := s.Seal(ctx(), "hello", "sessions"); !errors.Is(err, ErrNoProvider) {
		t.Fatalf("Seal err = %v, want ErrNoProvider", err)
	}
	if _, err := s.Open(ctx(), "es1:v2:x", "sessions"); !errors.Is(err, ErrNoProvider) {
		t.Fatalf("Open err = %v, want ErrNoProvider", err)
	}
	if _, err := s.ShouldReSeal(ctx(), "es1:v2:x", "sessions"); !errors.Is(err, ErrNoProvider) {
		t.Fatalf("ShouldReSeal err = %v, want ErrNoProvider", err)
	}
}

// A provider error must surface, never be read as "no key".
func TestProviderErrorsSurface(t *testing.T) {
	p := provider()
	p.keyErr = errors.New("vault unreachable")
	if _, err := (Sealer{Provider: p}).Seal(ctx(), "hello", "sessions"); err == nil {
		t.Fatal("a provider read failure was swallowed")
	}
}

// A version carrying the delimiter would produce a blob Open splits wrongly, so
// it is named at seal time rather than surfacing later as unopenable data.
func TestSealRefusesAVersionContainingTheDelimiter(t *testing.T) {
	p := provider()
	p.current = map[string]string{"sessions": "v2:1"}
	p.material["sessions/v2:1"] = "k"
	p.sealable["sessions/v2:1"] = true
	if _, err := (Sealer{Provider: p}).Seal(ctx(), "hello", "sessions"); !errors.Is(err, ErrVersionDelim) {
		t.Fatalf("err = %v, want ErrVersionDelim", err)
	}
}
