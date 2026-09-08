/*
 * Copyright (c) 2026 HSTLES / ORBTR Pty Ltd. All Rights Reserved.
 * Queries: licensing@hstles.com
 */
package meshcfg

import (
	"errors"
	"testing"
)

func valid() Networking {
	return Networking{
		NetworkKeys: []string{"shared-1"},
		Partitions: []PartitionTransport{
			{Partition: "p1", NetworkKeys: []string{"p1-new", "p1-old"}},
			{Partition: "p2", NetworkKeys: []string{"p2-key"}, Dedicated: true, UDPPort: 9101},
		},
	}
}

func TestValidateAcceptsAWellFormedSet(t *testing.T) {
	if err := valid().Validate(); err != nil {
		t.Fatalf("a well-formed config was rejected: %v", err)
	}
}

func TestValidateRejectsDuplicatePartition(t *testing.T) {
	c := valid()
	c.Partitions = append(c.Partitions, PartitionTransport{Partition: "p1", NetworkKeys: []string{"other"}})
	// Two entries for one partition make the SENDING key depend on slice order.
	if err := c.Validate(); !errors.Is(err, ErrDuplicate) {
		t.Fatalf("error = %v, want ErrDuplicate", err)
	}
}

func TestValidateRejectsAPartitionWithNoKeys(t *testing.T) {
	c := valid()
	c.Partitions[0].NetworkKeys = nil
	// No keys means the partition transports unauthenticated while the config asserts isolation.
	if err := c.Validate(); !errors.Is(err, ErrNoKeys) {
		t.Fatalf("error = %v, want ErrNoKeys", err)
	}
}

func TestValidateRejectsAnEmptyKeyInTheSet(t *testing.T) {
	c := valid()
	c.Partitions[0].NetworkKeys = []string{"good", ""}
	if err := c.Validate(); !errors.Is(err, ErrEmptyKey) {
		t.Fatalf("error = %v, want ErrEmptyKey", err)
	}
}

func TestValidateRejectsDedicatedWithoutAPort(t *testing.T) {
	c := valid()
	c.Partitions[1].UDPPort = 0
	// Silently rejoins the shared port — the opposite of what Dedicated asked for.
	if err := c.Validate(); !errors.Is(err, ErrDedicatedNoPort) {
		t.Fatalf("error = %v, want ErrDedicatedNoPort", err)
	}
}

func TestValidateRejectsTwoDedicatedPartitionsOnOnePort(t *testing.T) {
	c := valid()
	c.Partitions = append(c.Partitions, PartitionTransport{
		Partition: "p3", NetworkKeys: []string{"p3"}, Dedicated: true, UDPPort: 9101,
	})
	// Config asserts isolation; the port says otherwise.
	if err := c.Validate(); !errors.Is(err, ErrPortCollision) {
		t.Fatalf("error = %v, want ErrPortCollision", err)
	}
}

func TestValidateRejectsAnEmptyPartitionName(t *testing.T) {
	c := valid()
	c.Partitions[0].Partition = ""
	if err := c.Validate(); !errors.Is(err, ErrNoPartition) {
		t.Fatalf("error = %v, want ErrNoPartition", err)
	}
}

func TestKeysForNeverFallsBackForAnUnlistedPartition(t *testing.T) {
	c := valid()
	keys, ok := c.KeysFor("not-configured")
	if ok || keys != nil {
		t.Fatalf("KeysFor returned (%v, %v) for an unlisted partition — handing it the shared "+
			"material would admit it to the mesh the per-partition keys exist to divide", keys, ok)
	}
	// And it must not be reachable by asking for the empty partition either.
	if _, ok := c.KeysFor(""); ok {
		t.Fatal("the empty partition resolved to a key set")
	}
}

func TestKeysForReturnsTheConfiguredSet(t *testing.T) {
	keys, ok := valid().KeysFor("p1")
	if !ok || len(keys) != 2 || keys[0] != "p1-new" {
		t.Fatalf("KeysFor(p1) = (%v, %v), want the rolling set newest-first", keys, ok)
	}
}

func TestSendKeyIsTheFirstOfTheRollingSet(t *testing.T) {
	k, ok := valid().SendKey("p1")
	if !ok || k != "p1-new" {
		t.Fatalf("SendKey(p1) = (%q, %v), want p1-new — the first key signs, all are accepted", k, ok)
	}
}

func TestSendKeyRefusesRatherThanReturningAZeroKey(t *testing.T) {
	if _, ok := valid().SendKey("not-configured"); ok {
		t.Fatal("SendKey succeeded for an unlisted partition; a caller would transmit under a zero key")
	}
	empty := Networking{Partitions: []PartitionTransport{{Partition: "p", NetworkKeys: []string{""}}}}
	if _, ok := empty.SendKey("p"); ok {
		t.Fatal("SendKey returned an empty key as usable")
	}
}

func TestZeroValueIsUndividedAndConfiguresNobody(t *testing.T) {
	var z Networking
	if z.Partitioned() {
		t.Fatal("zero Networking reported as partitioned")
	}
	if _, ok := z.KeysFor("anything"); ok {
		t.Fatal("zero Networking resolved a partition")
	}
	if err := z.Validate(); err != nil {
		t.Fatalf("zero Networking failed validation: %v — an undivided mesh is a legitimate config", err)
	}
}
