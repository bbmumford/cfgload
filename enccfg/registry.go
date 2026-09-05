/*
 * Copyright (c) 2026 HSTLES / ORBTR Pty Ltd. All Rights Reserved.
 * Queries: licensing@hstles.com
 */
package enccfg

import "github.com/bbmumford/cfgload"

// RegistryKey is the name this config is registered under. It is exported
// because the registry is a shared namespace: a writer and a reader agree only
// by using the same string, and a literal typed twice is a handshake that
// compiles either way and fails at first use.
const RegistryKey = "encryption"

// Register stores cfg under RegistryKey. Registering an INVALID config is
// refused here rather than at first decrypt, because the caller that loaded it
// is the one able to fix it.
func Register(cfg *Config) error {
	if cfg == nil {
		return errNilConfig
	}
	if err := cfg.Validate(); err != nil {
		return err
	}
	return cfgload.Register(RegistryKey, cfg)
}

// FromRegistry returns the registered config, and whether one is present.
//
// The lookup is type-parameterised, so a config registered under this key with a
// DIFFERENT type reports absent rather than mismatched. That is worth knowing
// when migrating: a half-migrated pair — one side registering this type, the
// other reading another — looks exactly like "nothing was ever registered".
func FromRegistry() (*Config, bool) {
	return cfgload.GetAs[*Config](RegistryKey)
}

// CurrentVersion is the version a domain encrypts new data with, or "" when the
// domain is not configured.
func CurrentVersion(domain string) string {
	cfg, ok := FromRegistry()
	if !ok || cfg == nil {
		return ""
	}
	return cfg.Domains[domain].CurrentVersion
}

// KeyMaterial returns a version's key and whether it is the wrapped form.
// found is false when the domain or version is unknown — which callers must not
// treat as an empty key, since encrypting under one would produce data nothing
// can read back.
func KeyMaterial(domain, version string) (material string, wrapped bool, found bool) {
	cfg, ok := FromRegistry()
	if !ok || cfg == nil {
		return "", false, false
	}
	k, exists := cfg.Domains[domain].Keys[version]
	if !exists {
		return "", false, false
	}
	if k.WrappedKey != "" {
		return k.WrappedKey, true, true
	}
	return k.Key, false, k.Key != ""
}

// Usable reports whether a version may be used for NEW encryption.
//
// It answers false for an unknown domain, an unknown version, and an
// unregistered config — not only for a status that says so. This is the
// deliberate difference from asking "is it compromised": that question returns
// "no" when nothing is known, which reads as healthy and is the wrong direction
// for key material. Absence of evidence about a key is not evidence the key is
// fit to use.
func Usable(domain, version string) bool {
	cfg, ok := FromRegistry()
	if !ok || cfg == nil {
		return false
	}
	k, exists := cfg.Domains[domain].Keys[version]
	if !exists {
		return false
	}
	return k.Status == "active"
}

// Status returns a version's recorded status and whether it was found, so a
// caller can tell "marked inactive" from "not configured at all".
func Status(domain, version string) (string, bool) {
	cfg, ok := FromRegistry()
	if !ok || cfg == nil {
		return "", false
	}
	k, exists := cfg.Domains[domain].Keys[version]
	if !exists {
		return "", false
	}
	return k.Status, true
}

type constError string

func (e constError) Error() string { return string(e) }

const errNilConfig = constError("enccfg: refusing to register a nil config")
