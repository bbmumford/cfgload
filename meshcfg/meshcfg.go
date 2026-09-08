/*
 * Copyright (c) 2026 HSTLES / ORBTR Pty Ltd. All Rights Reserved.
 * Queries: licensing@hstles.com
 */

// Package meshcfg carries mesh transport isolation keyed on an opaque partition.
//
// A partition is a string this package never interprets: it does not know what a
// partition names, whether partitions nest, or which one is privileged. The
// consumer supplies the vocabulary and the meaning, so the same config serves a
// deployment that partitions by customer, by region, by product, or not at all.
package meshcfg

import (
	"errors"
	"fmt"
)

// PartitionTransport isolates one partition's mesh traffic behind its own keys.
type PartitionTransport struct {
	// Partition is the opaque identifier this transport belongs to. It is compared
	// only for equality, never parsed, ranked, or split.
	Partition string `json:"partition" yaml:"partition"`

	// NetworkKeys are the partition's pre-shared keys, newest first. Rolling is the
	// point: the FIRST key signs outbound traffic while ALL of them are accepted
	// inbound, so a key can be introduced everywhere before anything sends with it
	// and retired only once nothing does. A single-key list is the degenerate case,
	// not the intended steady state.
	NetworkKeys []string `json:"networkKeys" yaml:"networkKeys"`

	// Dedicated gives this partition its own UDP port instead of sharing the common
	// one. It REQUIRES UDPPort: a dedicated transport with no port silently falls
	// back to the shared port, which is the exact opposite of what was asked for and
	// is invisible at runtime.
	Dedicated bool `json:"dedicated,omitempty" yaml:"dedicated,omitempty"`

	// UDPPort is the dedicated listener port. Ignored unless Dedicated.
	UDPPort int `json:"udpPort,omitempty" yaml:"udpPort,omitempty"`
}

// Validation errors. They are distinguishable because a caller loading config at
// boot usually wants to report which kind of mistake was made, not just that one was.
var (
	ErrNoPartition     = errors.New("meshcfg: transport entry has an empty partition")
	ErrDuplicate       = errors.New("meshcfg: two transport entries for one partition")
	ErrNoKeys          = errors.New("meshcfg: transport entry has no network keys")
	ErrDedicatedNoPort = errors.New("meshcfg: dedicated transport has no UDP port")
	ErrPortCollision   = errors.New("meshcfg: two dedicated transports share a UDP port")
	ErrEmptyKey        = errors.New("meshcfg: transport entry has an empty network key")
)

// Validate reports the first configuration mistake that would make the mesh behave
// differently from what the config says.
//
// Each check exists because its failure is silent at runtime: a duplicate partition
// makes the sending key depend on slice order, a missing key set means the partition
// transports unauthenticated, a dedicated transport with no port quietly rejoins the
// shared one, and two dedicated partitions on one port are not isolated at all
// despite the config asserting they are.
func (t Networking) validateTransports() error {
	seen := make(map[string]struct{}, len(t.Partitions))
	ports := make(map[int]string, len(t.Partitions))
	for i, p := range t.Partitions {
		if p.Partition == "" {
			return fmt.Errorf("%w: index %d", ErrNoPartition, i)
		}
		if _, dup := seen[p.Partition]; dup {
			return fmt.Errorf("%w: %q", ErrDuplicate, p.Partition)
		}
		seen[p.Partition] = struct{}{}

		if len(p.NetworkKeys) == 0 {
			return fmt.Errorf("%w: %q", ErrNoKeys, p.Partition)
		}
		for _, k := range p.NetworkKeys {
			if k == "" {
				return fmt.Errorf("%w: %q", ErrEmptyKey, p.Partition)
			}
		}

		if p.Dedicated {
			if p.UDPPort == 0 {
				return fmt.Errorf("%w: %q", ErrDedicatedNoPort, p.Partition)
			}
			if other, clash := ports[p.UDPPort]; clash {
				return fmt.Errorf("%w: %q and %q on %d", ErrPortCollision, other, p.Partition, p.UDPPort)
			}
			ports[p.UDPPort] = p.Partition
		}
	}
	return nil
}

// KeysFor returns the keys configured for a partition, and whether it is configured
// at all.
//
// It does NOT fall back to NetworkKeys for an unlisted partition, and that refusal
// is the isolation boundary: handing the shared keys to a partition nobody configured
// would admit it to the mesh on the same material every unpartitioned peer holds,
// which is precisely the isolation the per-partition keys exist to create. A caller
// that genuinely wants the undivided mesh reads NetworkKeys directly and says so.
//
// The zero Transports returns (nil, false) for every partition.
func (t Networking) KeysFor(partition string) ([]string, bool) {
	if partition == "" {
		return nil, false
	}
	for _, p := range t.Partitions {
		if p.Partition == partition {
			return p.NetworkKeys, true
		}
	}
	return nil, false
}

// Partitioned reports whether any per-partition transport is configured. When false
// the mesh is undivided and NetworkKeys carries everything.
func (t Networking) Partitioned() bool { return len(t.Partitions) > 0 }

// SendKey returns the key a partition signs outbound traffic with — the first of its
// rolling set. Absent partition, or a partition with no keys, yields "" and false so
// a caller cannot transmit under a zero key believing it configured one.
func (t Networking) SendKey(partition string) (string, bool) {
	keys, ok := t.KeysFor(partition)
	if !ok || len(keys) == 0 || keys[0] == "" {
		return "", false
	}
	return keys[0], true
}
