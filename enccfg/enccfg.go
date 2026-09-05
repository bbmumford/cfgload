/*
 * Copyright (c) 2026 HSTLES / ORBTR Pty Ltd. All Rights Reserved.
 * Queries: licensing@hstles.com
 */

// Package enccfg is the at-rest key material an application loads: which key
// each domain currently encrypts with, and which older versions must still
// decrypt.
//
// It describes keys and says nothing about whose data they protect — there is no
// owner, tenant or partition here. A domain is a free-form label the consuming
// application chooses ("sessions", "pii"), and this package never interprets it.
package enccfg

import "fmt"

// Config maps each domain label to its keys.
type Config struct {
	Domains map[string]DomainKeys `yaml:"domains" json:"domains"`
}

// DomainKeys is one domain's key set. Keys holds every version that must still
// DECRYPT; CurrentVersion names the single one that ENCRYPTS. Retiring a version
// from Keys makes everything written under it unreadable, so removal is a
// data-lifecycle decision rather than cleanup.
type DomainKeys struct {
	CurrentVersion string         `yaml:"currentVersion" json:"currentVersion"`
	Keys           map[string]Key `yaml:"keys" json:"keys"`
	RotationPolicy string         `yaml:"rotationPolicy,omitempty" json:"rotationPolicy,omitempty"`
}

// Key is one version's material in exactly one of two forms.
//
// WrappedKey is the form to prefer: it stores the key sealed by a KEK, so
// reading the config file off disk does not yield usable key material. Key is
// the plaintext form, kept because deployments still carry it.
//
// Status reports rotation state — active, inactive, or compromised — and is the
// consumer's to act on; this package only records it.
type Key struct {
	Key        string `yaml:"key,omitempty" json:"key,omitempty"`
	WrappedKey string `yaml:"wrappedKey,omitempty" json:"wrappedKey,omitempty"`
	Status     string `yaml:"status" json:"status"`
}

// Validate rejects a config that cannot be resolved unambiguously.
//
// EXACTLY ONE of Key and WrappedKey must be set per version. Both set is the
// dangerous case and the reason this is an error rather than a preference: an
// operator wrapping a key is trying to stop the plaintext existing, and leaving
// the old field populated means the file still contains what the wrapping was
// meant to remove — while everything continues to work, so nothing reveals it.
// Neither set is also an error, since a version present in Keys is a promise
// that data written under it can still be read.
//
// A CurrentVersion naming a version absent from Keys is rejected too: it would
// fail at the first encrypt rather than at load, when the operator who made the
// change is no longer watching.
func (c Config) Validate() error {
	for domain, dk := range c.Domains {
		if dk.CurrentVersion == "" {
			return fmt.Errorf("enccfg: domain %q has no currentVersion", domain)
		}
		if _, ok := dk.Keys[dk.CurrentVersion]; !ok {
			return fmt.Errorf("enccfg: domain %q currentVersion %q is not in keys", domain, dk.CurrentVersion)
		}
		for version, k := range dk.Keys {
			hasPlain, hasWrapped := k.Key != "", k.WrappedKey != ""
			switch {
			case hasPlain && hasWrapped:
				return fmt.Errorf("enccfg: domain %q version %q sets both key and wrappedKey; "+
					"the plaintext must be removed once a wrapped form exists", domain, version)
			case !hasPlain && !hasWrapped:
				return fmt.Errorf("enccfg: domain %q version %q has no key material", domain, version)
			}
		}
	}
	return nil
}
