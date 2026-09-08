/*
 * Copyright (c) 2026 HSTLES / ORBTR Pty Ltd. All Rights Reserved.
 * Queries: licensing@hstles.com
 */
package meshcfg

import (
	"errors"
	"testing"
	"time"
)

func TestCacheableHandlerMapDropsUnusableEntries(t *testing.T) {
	d := Dispatch{CacheableHandlers: []CacheableHandler{
		{FQN: "good/Read", TTL: "30s"},
		{FQN: "", TTL: "30s"},        // no FQN
		{FQN: "noTTL/Read", TTL: ""}, // no TTL
		{FQN: "bad/Read", TTL: "30x"},
		{FQN: "zero/Read", TTL: "0s"},
		{FQN: "neg/Read", TTL: "-5s"},
	}}
	m := d.CacheableHandlerMap()
	// Dropping is right at runtime: the safe reading of a broken cache directive is
	// "do not cache", never "cache for some duration nobody wrote".
	if len(m) != 1 || m["good/Read"] != 30*time.Second {
		t.Fatalf("map = %v, want only good/Read=30s", m)
	}
}

func TestDispatchValidateReportsWhatTheMapSilentlyDrops(t *testing.T) {
	for _, tc := range []struct {
		name string
		h    CacheableHandler
	}{
		{"no FQN", CacheableHandler{FQN: "", TTL: "30s"}},
		{"no TTL", CacheableHandler{FQN: "x/Read", TTL: ""}},
		{"malformed", CacheableHandler{FQN: "x/Read", TTL: "30x"}},
		{"zero", CacheableHandler{FQN: "x/Read", TTL: "0s"}},
		{"negative", CacheableHandler{FQN: "x/Read", TTL: "-1s"}},
	} {
		d := Dispatch{CacheableHandlers: []CacheableHandler{tc.h}}
		if err := d.Validate(); !errors.Is(err, ErrBadTTL) {
			t.Errorf("%s: Validate error = %v, want ErrBadTTL — a dropped entry costs latency nobody attributes to a typo", tc.name, err)
		}
	}
}

func TestDispatchValidateAcceptsAWellFormedList(t *testing.T) {
	d := Dispatch{CacheableHandlers: []CacheableHandler{{FQN: "x/Read", TTL: "1m"}}}
	if err := d.Validate(); err != nil {
		t.Fatalf("a valid entry was rejected: %v", err)
	}
	if err := (Dispatch{}).Validate(); err != nil {
		t.Fatalf("an empty dispatch failed validation: %v — caching nothing is legitimate", err)
	}
}

func TestConfigValidateCoversBothHalves(t *testing.T) {
	// Networking half.
	c := Config{Networking: Networking{SyncInterval: "5x"}}
	if err := c.Validate(); !errors.Is(err, ErrBadDuration) {
		t.Fatalf("networking error = %v, want ErrBadDuration", err)
	}
	// Dispatch half.
	c = Config{Dispatch: Dispatch{CacheableHandlers: []CacheableHandler{{FQN: "x", TTL: "bad"}}}}
	if err := c.Validate(); !errors.Is(err, ErrBadTTL) {
		t.Fatalf("dispatch error = %v, want ErrBadTTL", err)
	}
	if err := (Config{}).Validate(); err != nil {
		t.Fatalf("zero Config failed validation: %v", err)
	}
}

func TestParseDurationUsesTheCallersFallbackNotThePackageDefault(t *testing.T) {
	// The point of exporting it: a consumer wanting 10s where the schema default is
	// 5s passes its own, and the value named in its own code is the one that applies.
	if got := ParseDuration("", 10*time.Second); got != 10*time.Second {
		t.Fatalf("empty -> %v, want the caller's 10s", got)
	}
	if got := ParseDuration("bad", 10*time.Second); got != 10*time.Second {
		t.Fatalf("malformed -> %v, want the caller's 10s (degrade, not die)", got)
	}
	if got := ParseDuration("90s", 10*time.Second); got != 90*time.Second {
		t.Fatalf("valid -> %v, want 90s — a real value must beat the fallback", got)
	}
	if got := ParseDuration("", DefaultVL1DialTimeout); got == 10*time.Second {
		t.Fatal("the package default and the caller default are not distinguishable in this test")
	}
}
