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
// token that reaches it. An entry with only a Name declares the database and
// leaves its url and token to the environment (FillSecrets).
type Database struct {
	Name string `json:"name" yaml:"name"`
	// URL is the primary, e.g. libsql://<name>-<org>.<region>.turso.io. It is
	// carried in the configuration because the manager opening the replica
	// never calls the platform API to look it up. Its variable is
	// MESHDB_<G>_<D>_URL.
	URL string `json:"url" yaml:"url"`
	// AuthToken is scoped to this database. Empty means the group's AuthToken.
	// Its variable is MESHDB_<G>_<D>_AUTH_TOKEN.
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
	ErrNoEncryptionKey  = errors.New("meshcfg: group has no encryptionKey")
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

// Validate reports the first group whose document shape the replicated-database
// manager would refuse at boot. A disabled MeshDB is not checked.
//
// cfgload.Load runs it (through Config.Validate) on the signed document, before
// the environment has supplied any secret, so an empty key, url or token passes
// here while a key that is present and malformed does not. Names must be safe
// path elements and unique. A raft block needs voters, a bootstrap size no
// larger than the voter count, host services for every job and exactly one
// shared database to hold its fence rows. ValidateResolved is the complete
// check, run after FillSecrets.
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

// Validate reports the first shape requirement of one group that is not met;
// MeshDB.Validate says which requirements those are.
func (g Group) Validate() error {
	if g.EncryptionKey != "" {
		if _, err := DecodeEncryptionKey(g.EncryptionKey); err != nil {
			return fmt.Errorf("group %q: %w", g.GroupName, err)
		}
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
	declared := map[string]bool{}
	for _, d := range g.Databases {
		if !nameRE.MatchString(d.Name) {
			return fmt.Errorf("%w: group %q database %q", ErrBadName, g.GroupName, d.Name)
		}
		if declared[d.Name] {
			return fmt.Errorf("%w: group %q database %q", ErrDuplicateName, g.GroupName, d.Name)
		}
		declared[d.Name] = true
		names[d.Name] = true
	}
	if g.Raft != nil {
		if err := g.Raft.validate(g, len(names)); err != nil {
			return fmt.Errorf("group %q: %w", g.GroupName, err)
		}
	}
	return nil
}

// ValidateResolved reports the first thing the replicated-database manager
// would still refuse once FillSecrets has run: everything Validate checks, a
// 32-byte key for every group, and for every database of a shared group a
// Databases entry with a url and a token (its own or the group's). A disabled
// MeshDB is not checked.
//
// It is separate from Validate because cfgload.Load runs Validate on the signed
// document, which never carries these values; a caller that fills them from
// its environment runs this before handing the groups to loom. Each error names
// the document field and the variable that would supply it, never a value.
func (m MeshDB) ValidateResolved() error {
	if err := m.Validate(); err != nil {
		return err
	}
	if !m.Enabled {
		return nil
	}
	for _, g := range m.Groups {
		if err := g.validateResolved(); err != nil {
			return err
		}
	}
	return nil
}

func (g Group) validateResolved() error {
	name := g.GroupName
	if g.EncryptionKey == "" {
		return fmt.Errorf("%w: set %s or %s", ErrNoEncryptionKey,
			settingPath(name, "", FieldEncryptionKey), EnvName(name, "", FieldEncryptionKey))
	}
	if g.PureLocal {
		return nil
	}
	declared := map[string]bool{}
	for _, d := range g.Databases {
		declared[d.Name] = true
		if d.URL == "" {
			return fmt.Errorf("%w: set %s or %s", ErrNoDatabaseURL,
				settingPath(name, d.Name, FieldURL), EnvName(name, d.Name, FieldURL))
		}
		if d.AuthToken == "" && g.AuthToken == "" {
			return fmt.Errorf("%w: set %s or %s, or the group's %s or %s", ErrNoDatabaseToken,
				settingPath(name, d.Name, FieldAuthToken), EnvName(name, d.Name, FieldAuthToken),
				settingPath(name, "", FieldAuthToken), EnvName(name, "", FieldAuthToken))
		}
	}
	for _, n := range g.DBNames {
		if !declared[n] {
			return fmt.Errorf("%w: networking.meshdb.groups[%s].dbNames lists %q, which has no databases entry to carry its url",
				ErrNoDatabaseURL, name, n)
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
