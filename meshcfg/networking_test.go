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

func TestValidateCatchesAMalformedDuration(t *testing.T) {
	// The whole reason Validate exists: every accessor falls back silently, so
	// "5x" and "5m" behave identically at runtime and the operator never learns which
	// one they wrote.
	n := Networking{SyncInterval: "5x"}
	err := n.Validate()
	if !errors.Is(err, ErrBadDuration) {
		t.Fatalf("Validate error = %v, want ErrBadDuration", err)
	}
}

func TestValidateAcceptsEmptyDurations(t *testing.T) {
	// Empty means "use the default" and is a legitimate config, not a mistake.
	if err := (Networking{}).Validate(); err != nil {
		t.Fatalf("an all-defaults Networking failed validation: %v", err)
	}
}

func TestValidateReachesEveryDurationField(t *testing.T) {
	for _, tc := range []struct {
		name string
		n    Networking
	}{
		{"heartbeat", Networking{HeartbeatInterval: "nope"}},
		{"vl1.dial", Networking{VL1: VL1{DialTimeout: "nope"}}},
		{"vl1.keepalive", Networking{VL1: VL1{KeepAliveInterval: "nope"}}},
		{"vl1.relayTicketTTL", Networking{VL1: VL1{RelayTicketTTL: "nope"}}},
		{"lad.sync", Networking{LAD: LAD{SyncInterval: "nope"}}},
		{"lad.compaction", Networking{LAD: LAD{CompactionInterval: "nope"}}},
		{"meshdb.sync", Networking{MeshDB: MeshDB{SyncInterval: "nope"}}},
		{"health.check", Networking{Health: Health{CheckInterval: "nope"}}},
		{"breaker.reset", Networking{CircuitBreaker: CircuitBreaker{ResetTime: "nope"}}},
	} {
		if err := tc.n.Validate(); !errors.Is(err, ErrBadDuration) {
			t.Errorf("%s: Validate error = %v, want ErrBadDuration — an unchecked field is a silent default", tc.name, err)
		}
	}
}

func TestValidateDelegatesToTransports(t *testing.T) {
	n := Networking{Partitions: []PartitionTransport{{Partition: "p", NetworkKeys: nil}}}
	if err := n.Validate(); !errors.Is(err, ErrNoKeys) {
		t.Fatalf("Validate error = %v, want the transport check to run too", err)
	}
}

func TestDurationAccessorsDefaultWhenEmpty(t *testing.T) {
	n := Networking{}
	if got := n.Sync(); got != DefaultSyncInterval {
		t.Errorf("Sync = %v, want %v", got, DefaultSyncInterval)
	}
	if got := n.Heartbeat(); got != DefaultHeartbeatInterval {
		t.Errorf("Heartbeat = %v, want %v", got, DefaultHeartbeatInterval)
	}
	if got := (LAD{}).Compaction(); got != DefaultCompactionInterval {
		t.Errorf("Compaction = %v, want %v", got, DefaultCompactionInterval)
	}
}

func TestDurationAccessorsDegradeRatherThanPanicOnMalformed(t *testing.T) {
	// A running node must not crash on a bad string; Validate is the gate that
	// stops it reaching production, and the accessor is the safety net if it does.
	if got := (Networking{SyncInterval: "5x"}).Sync(); got != DefaultSyncInterval {
		t.Fatalf("Sync on a malformed value = %v, want the default", got)
	}
}

func TestDurationAccessorsHonourAValidValue(t *testing.T) {
	if got := (Networking{SyncInterval: "90s"}).Sync(); got != 90*time.Second {
		t.Fatalf("Sync = %v, want 90s — a valid value must not be overridden by the default", got)
	}
}

func TestBloomFiltersDefaultOnWhenUnset(t *testing.T) {
	// The field is a POINTER precisely so omission differs from an explicit false.
	if !(LAD{}).BloomFilters() {
		t.Fatal("unset EnableBloomFilters read as OFF; omission must not look like opting out")
	}
	off := false
	if (LAD{EnableBloomFilters: &off}).BloomFilters() {
		t.Fatal("an explicit false was ignored")
	}
	on := true
	if !(LAD{EnableBloomFilters: &on}).BloomFilters() {
		t.Fatal("an explicit true was ignored")
	}
}

func TestRaftAddrPrefersTheExplicitValue(t *testing.T) {
	n := Networking{RaftPort: 8300, LAD: LAD{RaftBindAddr: ":9999"}}
	if got := n.RaftAddr(); got != ":9999" {
		t.Fatalf("RaftAddr = %q, want the explicit :9999", got)
	}
}

func TestRaftAddrDerivesFromRaftPortPlusOne(t *testing.T) {
	// Deriving rather than reusing RaftPort is the point: a mesh-storage deployment
	// naming one port still binds a second deterministically instead of colliding.
	if got := (Networking{RaftPort: 8300}).RaftAddr(); got != ":8301" {
		t.Fatalf("RaftAddr = %q, want :8301", got)
	}
	if got := (Networking{}).RaftAddr(); got != ":8301" {
		t.Fatalf("RaftAddr with no port = %q, want the :8301 fallback", got)
	}
}
