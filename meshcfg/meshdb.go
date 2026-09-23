/*
 * Copyright (c) 2026 HSTLES / ORBTR Pty Ltd. All Rights Reserved.
 * Queries: licensing@hstles.com
 */
package meshcfg

import (
	"encoding/base64"
	"errors"
	"fmt"
	"regexp"
	"time"
)

// Database is one shared database of a group: where its primary is and the
// token that reaches it.
type Database struct {
	Name string `json:"name" yaml:"name"`
	// URL is the primary, e.g. libsql://<name>-<org>.<region>.turso.io. It is
	// carried in the document because the manager opening the replica never
	// calls the platform API to look it up.
	URL string `json:"url" yaml:"url"`
	// AuthToken is scoped to this database. Empty means the group's AuthToken.
	AuthToken string `json:"authToken,omitempty" yaml:"authToken,omitempty"`
}

// Raft is a group's singleton-job declaration. The group's one shared database
// gets a Raft cluster that decides which process runs each job; the cluster
// carries assignments, never rows.
type Raft struct {
	// VoterServices are the services whose instances may vote. An instance
	// votes only if it also holds the group key, since every Raft stream opens
	// with a proof derived from it.
	VoterServices []string `json:"voterServices" yaml:"voterServices"`
	// VoterCount is how many voters the leader keeps; further eligible
	// instances wait as non-voting standbys.
	VoterCount int `json:"voterCount" yaml:"voterCount"`
	// BootstrapExpect is how many eligible voters with no Raft state must be
	// visible before the cluster is first formed.
	BootstrapExpect int `json:"bootstrapExpect" yaml:"bootstrapExpect"`
	// Jobs maps each job name to the services allowed to run it.
	Jobs map[string][]string `json:"jobs" yaml:"jobs"`
}

// MeshDB validation errors, distinguishable so a boot check can say which
// requirement a group missed.
var (
	ErrBadName          = errors.New("meshcfg: group, database or job name must match ^[a-z0-9][a-z0-9_-]*$")
	ErrBadEncryptionKey = errors.New("meshcfg: encryptionKey must be base64 of exactly 32 bytes")
	ErrNoDatabaseURL    = errors.New("meshcfg: shared database has no url")
	ErrNoDatabaseToken  = errors.New("meshcfg: shared database has no auth token")
	ErrDuplicateName    = errors.New("meshcfg: name declared twice")
	ErrBadRaft          = errors.New("meshcfg: raft block is invalid")
)

// nameRE is the shape every group, database and job name takes. The manager
// joins group and database names into file paths, so the pattern admits no
// separator, dot or upper case.
var nameRE = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]*$`)

// EncryptionKeyLen is the only group key length accepted.
const EncryptionKeyLen = 32

// DecodeEncryptionKey returns the 32-byte key s encodes, or ErrBadEncryptionKey.
func DecodeEncryptionKey(s string) ([]byte, error) {
	key, err := base64.StdEncoding.DecodeString(s)
	if err != nil || len(key) != EncryptionKeyLen {
		return nil, ErrBadEncryptionKey
	}
	return key, nil
}

// Validate reports the first group that the replicated-database manager would
// refuse at boot. A disabled MeshDB is not checked.
//
// Every group needs a 32-byte key, because encryption at rest has no opt-out.
// A shared group needs a url and a token for each database, because the
// manager opens replicas from the document alone. A raft block needs voters, a
// bootstrap size no larger than the voter count, host services for every job
// and exactly one shared database to hold its fence rows.
func (m MeshDB) Validate() error {
	if !m.Enabled {
		return nil
	}
	seen := map[string]bool{}
	for _, g := range m.Groups {
		if !nameRE.MatchString(g.GroupName) {
			return fmt.Errorf("%w: group %q", ErrBadName, g.GroupName)
		}
		if seen[g.GroupName] {
			return fmt.Errorf("%w: group %q", ErrDuplicateName, g.GroupName)
		}
		seen[g.GroupName] = true
		if err := g.Validate(); err != nil {
			return err
		}
	}
	return nil
}

// Validate reports the first requirement of one group that is not met.
func (g Group) Validate() error {
	if _, err := DecodeEncryptionKey(g.EncryptionKey); err != nil {
		return fmt.Errorf("group %q: %w", g.GroupName, err)
	}
	if g.PriorEncryptionKey != "" {
		if _, err := DecodeEncryptionKey(g.PriorEncryptionKey); err != nil {
			return fmt.Errorf("group %q priorEncryptionKey: %w", g.GroupName, err)
		}
	}
	if g.SyncInterval != "" {
		d, err := time.ParseDuration(g.SyncInterval)
		if err != nil || d <= 0 {
			return fmt.Errorf("%w: group %q syncInterval = %q", ErrBadDuration, g.GroupName, g.SyncInterval)
		}
	}
	names := map[string]bool{}
	for _, n := range g.DBNames {
		if !nameRE.MatchString(n) {
			return fmt.Errorf("%w: group %q database %q", ErrBadName, g.GroupName, n)
		}
		if names[n] {
			return fmt.Errorf("%w: group %q database %q", ErrDuplicateName, g.GroupName, n)
		}
		names[n] = true
	}
	withURL := map[string]bool{}
	for _, d := range g.Databases {
		if !nameRE.MatchString(d.Name) {
			return fmt.Errorf("%w: group %q database %q", ErrBadName, g.GroupName, d.Name)
		}
		if withURL[d.Name] {
			return fmt.Errorf("%w: group %q database %q", ErrDuplicateName, g.GroupName, d.Name)
		}
		withURL[d.Name] = true
		names[d.Name] = true
		if g.PureLocal {
			continue
		}
		if d.URL == "" {
			return fmt.Errorf("%w: group %q database %q", ErrNoDatabaseURL, g.GroupName, d.Name)
		}
		if d.AuthToken == "" && g.AuthToken == "" {
			return fmt.Errorf("%w: group %q database %q", ErrNoDatabaseToken, g.GroupName, d.Name)
		}
	}
	if !g.PureLocal {
		for n := range names {
			if !withURL[n] {
				return fmt.Errorf("%w: group %q database %q", ErrNoDatabaseURL, g.GroupName, n)
			}
		}
	}
	if g.Raft != nil {
		if err := g.Raft.validate(g, len(names)); err != nil {
			return fmt.Errorf("group %q: %w", g.GroupName, err)
		}
	}
	return nil
}

func (r Raft) validate(g Group, databases int) error {
	if g.PureLocal {
		return fmt.Errorf("%w: a pureLocal group has no shared database to fence", ErrBadRaft)
	}
	if databases != 1 {
		return fmt.Errorf("%w: the group must hold exactly one database, it holds %d", ErrBadRaft, databases)
	}
	if len(r.VoterServices) == 0 {
		return fmt.Errorf("%w: voterServices is empty", ErrBadRaft)
	}
	if r.VoterCount < 1 {
		return fmt.Errorf("%w: voterCount must be at least 1", ErrBadRaft)
	}
	if r.BootstrapExpect < 1 || r.BootstrapExpect > r.VoterCount {
		return fmt.Errorf("%w: bootstrapExpect must be between 1 and voterCount", ErrBadRaft)
	}
	for name, hosts := range r.Jobs {
		if !nameRE.MatchString(name) {
			return fmt.Errorf("%w: job %q", ErrBadName, name)
		}
		if len(hosts) == 0 {
			return fmt.Errorf("%w: job %q names no host service", ErrBadRaft, name)
		}
	}
	return nil
}
