/*
 * Copyright (c) 2026 HSTLES / ORBTR Pty Ltd. All Rights Reserved.
 * Queries: licensing@hstles.com
 */
package meshcfg

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

var testKey = base64.StdEncoding.EncodeToString([]byte(strings.Repeat("k", 32)))

func sharedGroup() Group {
	return Group{
		GroupName:     "control",
		EncryptionKey: testKey,
		DBNames:       []string{"orbtr-control"},
		Databases: []Database{{
			Name:      "orbtr-control",
			URL:       "libsql://orbtr-control-orbtr.turso.io",
			AuthToken: "db-token",
		}},
		SyncInterval: "5s",
		Raft: &Raft{
			VoterServices:   []string{"node.orbtr.ai", "node.orbtr.io"},
			VoterCount:      5,
			BootstrapExpect: 3,
			Jobs:            map[string][]string{"audit-projector": {"api.orbtr.ai", "node.orbtr.io"}},
		},
	}
}

func enabled(groups ...Group) MeshDB { return MeshDB{Enabled: true, Groups: groups} }

func TestAValidSharedGroupPasses(t *testing.T) {
	if err := enabled(sharedGroup()).Validate(); err != nil {
		t.Fatalf("Validate = %v, want nil", err)
	}
}

var raftYAML = `
enabled: true
groups:
  - groupName: control
    encryptionKey: ` + testKey + `
    syncInterval: 5s
    databases:
      - name: orbtr-control
        url: libsql://orbtr-control-orbtr.turso.io
        authToken: db-token
    raft:
      voterServices: [node.orbtr.ai, node.orbtr.io]
      voterCount: 5
      bootstrapExpect: 3
      jobs:
        audit-projector: [api.orbtr.ai, node.orbtr.io]
`

func TestTheRaftBlockUnmarshalsFromYAMLAndJSON(t *testing.T) {
	var fromYAML MeshDB
	if err := yaml.Unmarshal([]byte(raftYAML), &fromYAML); err != nil {
		t.Fatal(err)
	}
	want := sharedGroup()
	want.DBNames = nil
	if !reflect.DeepEqual(fromYAML.Groups[0], want) {
		t.Fatalf("yaml group = %+v\nwant %+v", fromYAML.Groups[0], want)
	}
	if err := fromYAML.Validate(); err != nil {
		t.Fatalf("yaml document fails Validate: %v", err)
	}

	raw, err := json.Marshal(fromYAML)
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{`"voterServices"`, `"voterCount"`, `"bootstrapExpect"`, `"jobs"`, `"databases"`, `"syncInterval"`} {
		if !strings.Contains(string(raw), key) {
			t.Errorf("json form lacks %s: %s", key, raw)
		}
	}
	var fromJSON MeshDB
	if err := json.Unmarshal(raw, &fromJSON); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(fromJSON, fromYAML) {
		t.Fatalf("json round trip = %+v, want %+v", fromJSON, fromYAML)
	}
}

func TestAGroupWithNoKeyFails(t *testing.T) {
	g := sharedGroup()
	g.EncryptionKey = ""
	if err := enabled(g).Validate(); !errors.Is(err, ErrBadEncryptionKey) {
		t.Fatalf("Validate = %v, want ErrBadEncryptionKey", err)
	}
	local := Group{GroupName: "lad", DBNames: []string{"lad"}, PureLocal: true}
	if err := enabled(local).Validate(); !errors.Is(err, ErrBadEncryptionKey) {
		t.Fatalf("pureLocal group with no key: Validate = %v, want ErrBadEncryptionKey", err)
	}
}

func TestASixteenByteKeyFails(t *testing.T) {
	g := sharedGroup()
	g.EncryptionKey = base64.StdEncoding.EncodeToString(make([]byte, 16))
	if err := enabled(g).Validate(); !errors.Is(err, ErrBadEncryptionKey) {
		t.Fatalf("Validate = %v, want ErrBadEncryptionKey", err)
	}
	g = sharedGroup()
	g.PriorEncryptionKey = "not base64!"
	if err := enabled(g).Validate(); !errors.Is(err, ErrBadEncryptionKey) {
		t.Fatalf("bad prior key: Validate = %v, want ErrBadEncryptionKey", err)
	}
}

func TestAMalformedGroupSyncIntervalFails(t *testing.T) {
	for _, v := range []string{"5x", "-5s", "0s"} {
		g := sharedGroup()
		g.SyncInterval = v
		if err := enabled(g).Validate(); !errors.Is(err, ErrBadDuration) {
			t.Errorf("syncInterval %q: Validate = %v, want ErrBadDuration", v, err)
		}
	}
	n := Networking{MeshDB: enabled(func() Group { g := sharedGroup(); g.SyncInterval = "5x"; return g }())}
	if err := n.Validate(); !errors.Is(err, ErrBadDuration) {
		t.Fatalf("Networking.Validate = %v, want the group check to run", err)
	}
}

func TestASharedDatabaseNeedsAURLAndAToken(t *testing.T) {
	g := sharedGroup()
	g.Databases[0].URL = ""
	if err := enabled(g).Validate(); !errors.Is(err, ErrNoDatabaseURL) {
		t.Fatalf("no url: Validate = %v, want ErrNoDatabaseURL", err)
	}
	g = sharedGroup()
	g.DBNames = append(g.DBNames, "orbtr-sessions")
	if err := enabled(g).Validate(); !errors.Is(err, ErrNoDatabaseURL) {
		t.Fatalf("dbNames entry without a databases entry: Validate = %v, want ErrNoDatabaseURL", err)
	}
	g = sharedGroup()
	g.Databases[0].AuthToken = ""
	if err := enabled(g).Validate(); !errors.Is(err, ErrNoDatabaseToken) {
		t.Fatalf("no token: Validate = %v, want ErrNoDatabaseToken", err)
	}
	g.AuthToken = "group-token"
	if err := enabled(g).Validate(); err != nil {
		t.Fatalf("group token fallback: Validate = %v, want nil", err)
	}
}

func TestNamesThatWouldReachAPathAreRefused(t *testing.T) {
	for _, name := range []string{"", "../x", "Control", "a/b", ".hidden"} {
		g := sharedGroup()
		g.GroupName = name
		if err := enabled(g).Validate(); !errors.Is(err, ErrBadName) {
			t.Errorf("group %q: Validate = %v, want ErrBadName", name, err)
		}
	}
	g := sharedGroup()
	g.Raft.Jobs["Bad Job"] = []string{"api.orbtr.ai"}
	if err := enabled(g).Validate(); !errors.Is(err, ErrBadName) {
		t.Fatalf("job name: Validate = %v, want ErrBadName", err)
	}
}

func TestTheRaftBlockIsChecked(t *testing.T) {
	for name, mutate := range map[string]func(*Group){
		"no voters":          func(g *Group) { g.Raft.VoterServices = nil },
		"zero voterCount":    func(g *Group) { g.Raft.VoterCount = 0 },
		"expect above count": func(g *Group) { g.Raft.BootstrapExpect = 6 },
		"expect zero":        func(g *Group) { g.Raft.BootstrapExpect = 0 },
		"job with no host":   func(g *Group) { g.Raft.Jobs["x"] = nil },
		"pure local":         func(g *Group) { g.PureLocal = true },
		"two databases": func(g *Group) {
			g.Databases = append(g.Databases, Database{Name: "second", URL: "libsql://s", AuthToken: "t"})
		},
	} {
		g := sharedGroup()
		mutate(&g)
		if err := enabled(g).Validate(); !errors.Is(err, ErrBadRaft) {
			t.Errorf("%s: Validate = %v, want ErrBadRaft", name, err)
		}
	}
}

func TestADisabledMeshDBIsNotChecked(t *testing.T) {
	m := MeshDB{Groups: []Group{{GroupName: "control"}}}
	if err := m.Validate(); err != nil {
		t.Fatalf("disabled MeshDB: Validate = %v, want nil", err)
	}
}

func TestDuplicateGroupsAreRefused(t *testing.T) {
	if err := enabled(sharedGroup(), sharedGroup()).Validate(); !errors.Is(err, ErrDuplicateName) {
		t.Fatalf("Validate = %v, want ErrDuplicateName", err)
	}
}
