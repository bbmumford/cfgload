/*
 * Copyright (c) 2026 HSTLES / ORBTR Pty Ltd. All Rights Reserved.
 * Queries: licensing@hstles.com
 */
package meshcfg

import (
	"errors"
	"fmt"
	"time"
)

// The loadable mesh networking schema: what a yaml/json file deserialises into,
// which a consumer then maps onto its runtime types.
//
// It is deliberately SEPARATE from the runtime structs a mesh node holds. Those
// carry only what the node uses; these carry what an operator writes, including
// fields a runtime type has no place for (the anchor URLs) and the env bindings a
// loader needs. Keeping them apart is why this package can change its file format
// without touching a node's internals.
//
// Durations are STRINGS here because that is what a config file holds. Each has a
// typed accessor that applies a default, and Validate reports one that will not
// parse — so a typo is caught at boot instead of silently becoming the default.

// Networking is the mesh networking section.
type Networking struct {
	RaftPort          int    `json:"raftPort" yaml:"raftPort"`
	SyncInterval      string `json:"syncInterval" yaml:"syncInterval"`
	HeartbeatInterval string `json:"heartbeatInterval" yaml:"heartbeatInterval"`
	DebugLog          bool   `json:"debugLog" yaml:"debugLog"`

	// NetworkKeys carry traffic belonging to no partition — discovery, anchor and
	// STUN — and are the whole key set when Partitions is empty. They are NOT a
	// default for an unlisted partition; see KeysFor.
	NetworkKeys []string `json:"networkKeys" yaml:"networkKeys" env:"MESH_NETWORK_KEYS"`

	// Partitions are the per-partition transports. Empty means undivided.
	Partitions []PartitionTransport `json:"partitions,omitempty" yaml:"partitions,omitempty"`

	Anchor         Anchor         `json:"anchor" yaml:"anchor"`
	VL1            VL1            `json:"vl1" yaml:"vl1"`
	LAD            LAD            `json:"lad" yaml:"lad"`
	MeshDB         MeshDB         `json:"meshdb" yaml:"meshdb"`
	Health         Health         `json:"health" yaml:"health"`
	CircuitBreaker CircuitBreaker `json:"circuitBreaker" yaml:"circuitBreaker"`
	Audit          Audit          `json:"audit" yaml:"audit"`
	DataDir        string         `json:"dataDir" yaml:"dataDir"`
}

// Anchor configures bootstrapping from signed directory snapshots.
type Anchor struct {
	// URLs are the anchor endpoints to fetch snapshots from. A runtime anchor type
	// holds only the keys, so this field exists on the loadable side alone.
	URLs []string `json:"urls" yaml:"urls"`
	// PublicKey verifies snapshot signatures; PrivateKey signs them and is set only
	// on a node that IS an anchor.
	PublicKey  string `json:"publicKey" yaml:"publicKey" env:"MESH_ANCHOR_PUBLIC_KEY"`
	PrivateKey string `json:"privateKey" yaml:"privateKey" env:"MESH_ANCHOR_PRIVATE_KEY"`
}

// VL1 configures the UDP peer-to-peer overlay.
type VL1 struct {
	Enabled           bool   `json:"enabled" yaml:"enabled"`
	UDPPort           int    `json:"udpPort" yaml:"udpPort"`
	MaxPeers          int    `json:"maxPeers" yaml:"maxPeers"`
	DialTimeout       string `json:"dialTimeout" yaml:"dialTimeout"`
	KeepAliveInterval string `json:"keepAliveInterval" yaml:"keepAliveInterval"`
	EnableRelay       bool   `json:"enableRelay" yaml:"enableRelay"`
	RelayMaxRate      int    `json:"relayMaxRate" yaml:"relayMaxRate"`
	RelayTicketTTL    string `json:"relayTicketTTL" yaml:"relayTicketTTL"`
}

// LAD configures the ledger-backed directory.
type LAD struct {
	Enabled bool `json:"enabled" yaml:"enabled"`
	// Storage selects where discovery records live: "local" (embedded, survives
	// restart), "memory" (ephemeral), or "mesh" (replicated, needs RaftBindAddr).
	Storage            string `json:"storage" yaml:"storage"`
	SyncInterval       string `json:"syncInterval" yaml:"syncInterval"`
	LocalDir           string `json:"localDir" yaml:"localDir"`
	BootstrapPeers     string `json:"bootstrapPeers" yaml:"bootstrapPeers"`
	RetentionDays      int    `json:"retentionDays" yaml:"retentionDays"`
	RaftBindAddr       string `json:"raftBindAddr" yaml:"raftBindAddr"`
	CompactionInterval string `json:"compactionInterval" yaml:"compactionInterval"`
	// EnableBloomFilters is a pointer so "unset" is distinguishable from an explicit
	// false — the default is ON, and a bool would make omission look like opting out.
	EnableBloomFilters *bool `json:"enableBloomFilters" yaml:"enableBloomFilters"`
}

// MeshDB configures the replicated database.
type MeshDB struct {
	Enabled bool    `json:"enabled" yaml:"enabled"`
	Turso   Turso   `json:"turso" yaml:"turso"`
	Groups  []Group `json:"groups" yaml:"groups"`
	// The served document is signed, so it has to be regenerated and re-signed
	// with this key rather than after it: verification reads the document's own
	// keys, so an older document still verifies and then supplies nothing here,
	// leaving the group and token empty. That is silent — the signature is fine
	// and the field is simply absent — so the two move together.
	PartitionDB  PartitionDB `json:"partitionDB" yaml:"partitionDB"`
	SyncInterval string      `json:"syncInterval" yaml:"syncInterval"`
}

// Turso is the database platform account this deployment provisions under.
type Turso struct {
	APIToken string `json:"apiToken" yaml:"apiToken" env:"TURSO_API_TOKEN"`
	OrgName  string `json:"orgName" yaml:"orgName"`
}

// Group is one database group and the credentials that reach it.
type Group struct {
	GroupName     string   `json:"groupName" yaml:"groupName"`
	AuthToken     string   `json:"authToken" yaml:"authToken" env:"TURSO_AUTH_TOKEN"`
	DBNames       []string `json:"dbNames" yaml:"dbNames"`
	EncryptionKey string   `json:"encryptionKey" yaml:"encryptionKey" env:"MESHDB_ENCRYPTION_KEY"`
	PureLocal     bool     `json:"pureLocal" yaml:"pureLocal"`
}

// PartitionDB configures databases provisioned per PARTITION, using the opaque
// partition axis rather than a tenant level. This package never learns what a
// partition names — it carries the group and token a provisioner needs and nothing
// about who the partitions are.
type PartitionDB struct {
	GroupName string `json:"groupName" yaml:"groupName"`
	// AuthToken reaches this group only. It is separate from the control group's
	// token because a group that holds per-partition data should not be readable
	// with the credential that administers the platform account.
	//
	// The env name is what deployments actually set today. Renaming the VARIABLE is
	// a deployment change, not a code one, so this tag tracks reality rather than
	// naming a variable nobody exports — a tag that names the wrong one sends the
	// next reader to set a secret that is never read.
	AuthToken string `json:"authToken" yaml:"authToken" env:"TURSO_TENANT_AUTH_TOKEN"`
}

// Health configures liveness checking.
type Health struct {
	Enabled       bool   `json:"enabled" yaml:"enabled"`
	CheckInterval string `json:"checkInterval" yaml:"checkInterval"`
}

// CircuitBreaker configures failure-triggered cutoff.
type CircuitBreaker struct {
	Enabled   bool   `json:"enabled" yaml:"enabled"`
	Threshold int    `json:"threshold" yaml:"threshold"`
	ResetTime string `json:"resetTime" yaml:"resetTime"`
}

// Audit configures the audit log sink.
type Audit struct {
	Enabled bool   `json:"enabled" yaml:"enabled"`
	LogPath string `json:"logPath" yaml:"logPath"`
}

// ErrBadDuration reports a duration field that will not parse.
var ErrBadDuration = errors.New("meshcfg: unparseable duration")

// Defaults applied when a duration field is empty.
const (
	DefaultSyncInterval        = 5 * time.Minute
	DefaultHeartbeatInterval   = 30 * time.Second
	DefaultVL1DialTimeout      = 5 * time.Second
	DefaultVL1KeepAlive        = 5 * time.Second
	DefaultHealthCheckInterval = 30 * time.Second
	DefaultBreakerResetTime    = time.Minute
	DefaultLADSyncInterval     = 5 * time.Minute
	DefaultCompactionInterval  = 24 * time.Hour
)

// duration returns d parsed, or fallback when empty. A MALFORMED value returns the
// fallback too, so a running node degrades rather than crashing — Validate is what
// refuses to let a typo reach production in the first place.
func duration(s string, fallback time.Duration) time.Duration {
	if s == "" {
		return fallback
	}
	d, err := time.ParseDuration(s)
	if err != nil {
		return fallback
	}
	return d
}

func (n Networking) Sync() time.Duration { return duration(n.SyncInterval, DefaultSyncInterval) }
func (n Networking) Heartbeat() time.Duration {
	return duration(n.HeartbeatInterval, DefaultHeartbeatInterval)
}
func (v VL1) Dial() time.Duration      { return duration(v.DialTimeout, DefaultVL1DialTimeout) }
func (v VL1) KeepAlive() time.Duration { return duration(v.KeepAliveInterval, DefaultVL1KeepAlive) }
func (h Health) Interval() time.Duration {
	return duration(h.CheckInterval, DefaultHealthCheckInterval)
}
func (c CircuitBreaker) Reset() time.Duration { return duration(c.ResetTime, DefaultBreakerResetTime) }
func (l LAD) Sync() time.Duration             { return duration(l.SyncInterval, DefaultLADSyncInterval) }
func (l LAD) Compaction() time.Duration {
	return duration(l.CompactionInterval, DefaultCompactionInterval)
}

// BloomFilters reports whether query bloom filters are on, defaulting to TRUE when
// the field is unset — which is why the field is a pointer.
func (l LAD) BloomFilters() bool { return l.EnableBloomFilters == nil || *l.EnableBloomFilters }

// RaftAddr returns the LAD replication address, deriving it from RaftPort+1 when
// unset so a mesh-storage deployment that names only one port still binds a second
// one deterministically rather than colliding with the first.
func (n Networking) RaftAddr() string {
	if n.LAD.RaftBindAddr != "" {
		return n.LAD.RaftBindAddr
	}
	if n.RaftPort > 0 {
		return fmt.Sprintf(":%d", n.RaftPort+1)
	}
	return ":8301"
}

// Validate reports the first malformed duration, and delegates transport checks to
// Transports.Validate. It exists because every accessor above falls back silently:
// without this, "5x" and "5m" behave identically at runtime and the operator never
// learns which one they wrote.
func (n Networking) Validate() error {
	for _, f := range []struct {
		name, val string
	}{
		{"syncInterval", n.SyncInterval},
		{"heartbeatInterval", n.HeartbeatInterval},
		{"vl1.dialTimeout", n.VL1.DialTimeout},
		{"vl1.keepAliveInterval", n.VL1.KeepAliveInterval},
		{"vl1.relayTicketTTL", n.VL1.RelayTicketTTL},
		{"lad.syncInterval", n.LAD.SyncInterval},
		{"lad.compactionInterval", n.LAD.CompactionInterval},
		{"meshdb.syncInterval", n.MeshDB.SyncInterval},
		{"health.checkInterval", n.Health.CheckInterval},
		{"circuitBreaker.resetTime", n.CircuitBreaker.ResetTime},
	} {
		if f.val == "" {
			continue
		}
		if _, err := time.ParseDuration(f.val); err != nil {
			return fmt.Errorf("%w: %s = %q", ErrBadDuration, f.name, f.val)
		}
	}
	return n.validateTransports()
}
