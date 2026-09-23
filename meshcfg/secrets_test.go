/*
 * Copyright (c) 2026 HSTLES / ORBTR Pty Ltd. All Rights Reserved.
 * Queries: licensing@hstles.com
 */
package meshcfg

import (
	"encoding/base64"
	"errors"
	"reflect"
	"slices"
	"strings"
	"testing"
)

// fakeEnv is a lookup over a fixed environment that records each name read.
type fakeEnv struct {
	vals map[string]string
	read []string
}

func (e *fakeEnv) lookup(name string) (string, bool) {
	e.read = append(e.read, name)
	v, ok := e.vals[name]
	return v, ok
}

// hostConfig declares api.orbtr.ai's two shared groups by name alone, as a
// template that carries no secret does, and a pureLocal group, whose token
// variable FillSecrets must not read.
func hostConfig() Config {
	var c Config
	c.Networking.MeshDB = MeshDB{Enabled: true, Groups: []Group{
		{GroupName: "orbtr", DBNames: []string{"orbtr_endpoint"}, Databases: []Database{{Name: "orbtr_endpoint"}}},
		{GroupName: "orbtr-sessions", DBNames: []string{"orbtr-sessions"}, Databases: []Database{{Name: "orbtr-sessions"}}},
		{GroupName: "lad", DBNames: []string{"lad"}, PureLocal: true},
	}}
	return c
}

var otherKey = base64.StdEncoding.EncodeToString([]byte(strings.Repeat("o", 32)))

func hostEnv() *fakeEnv {
	return &fakeEnv{vals: map[string]string{
		"MESHDB_ORBTR_ENCRYPTION_KEY":                     testKey,
		"MESHDB_ORBTR_ORBTR_ENDPOINT_URL":                 "libsql://orbtr-endpoint.example",
		"MESHDB_ORBTR_ORBTR_ENDPOINT_AUTH_TOKEN":          "endpoint-token",
		"MESHDB_ORBTR_SESSIONS_ENCRYPTION_KEY":            otherKey,
		"MESHDB_ORBTR_SESSIONS_ORBTR_SESSIONS_URL":        "libsql://orbtr-sessions.example",
		"MESHDB_ORBTR_SESSIONS_ORBTR_SESSIONS_AUTH_TOKEN": "sessions-token",
		"MESHDB_LAD_ENCRYPTION_KEY":                       testKey,
		"MESHDB_LAD_AUTH_TOKEN":                           "never-read",
		EnvTursoAPIToken:                                  "org-token",
		EnvPartitionAuthToken:                             "partition-token",
	}}
}

// These names become deployment secret names; a change here renames them.
func TestEnvNameDerivesTheHostVariables(t *testing.T) {
	for _, tc := range []struct {
		group, db string
		field     SecretField
		want      string
	}{
		{"orbtr", "", FieldEncryptionKey, "MESHDB_ORBTR_ENCRYPTION_KEY"},
		{"orbtr", "", FieldPriorEncryptionKey, "MESHDB_ORBTR_PRIOR_ENCRYPTION_KEY"},
		{"orbtr", "", FieldAuthToken, "MESHDB_ORBTR_AUTH_TOKEN"},
		{"orbtr", "orbtr_endpoint", FieldURL, "MESHDB_ORBTR_ORBTR_ENDPOINT_URL"},
		{"orbtr", "orbtr_endpoint", FieldAuthToken, "MESHDB_ORBTR_ORBTR_ENDPOINT_AUTH_TOKEN"},
		{"orbtr-sessions", "", FieldEncryptionKey, "MESHDB_ORBTR_SESSIONS_ENCRYPTION_KEY"},
		{"orbtr-sessions", "orbtr-sessions", FieldURL, "MESHDB_ORBTR_SESSIONS_ORBTR_SESSIONS_URL"},
		{"orbtr-sessions", "orbtr-sessions", FieldAuthToken, "MESHDB_ORBTR_SESSIONS_ORBTR_SESSIONS_AUTH_TOKEN"},
	} {
		if got := EnvName(tc.group, tc.db, tc.field); got != tc.want {
			t.Errorf("EnvName(%q, %q, %s) = %s, want %s", tc.group, tc.db, tc.field, got, tc.want)
		}
	}
	for _, f := range []SecretField{FieldEncryptionKey, FieldPriorEncryptionKey, FieldAuthToken} {
		if EnvName("orbtr", "", f) == EnvName("orbtr-sessions", "", f) {
			t.Errorf("groups orbtr and orbtr-sessions share the %s variable", f)
		}
	}
}

func TestFillSecretsTakesEmptySettingsFromTheEnvironment(t *testing.T) {
	c := hostConfig()
	if err := c.Networking.MeshDB.ValidateResolved(); !errors.Is(err, ErrNoEncryptionKey) {
		t.Fatalf("before FillSecrets: ValidateResolved = %v, want ErrNoEncryptionKey", err)
	}
	env := hostEnv()
	if err := c.FillSecrets(env.lookup); err != nil {
		t.Fatalf("FillSecrets = %v", err)
	}
	m := c.Networking.MeshDB
	for _, f := range []struct{ name, got, want string }{
		{"orbtr key", m.Groups[0].EncryptionKey, testKey},
		{"orbtr_endpoint url", m.Groups[0].Databases[0].URL, "libsql://orbtr-endpoint.example"},
		{"orbtr_endpoint token", m.Groups[0].Databases[0].AuthToken, "endpoint-token"},
		{"orbtr-sessions key", m.Groups[1].EncryptionKey, otherKey},
		{"orbtr-sessions url", m.Groups[1].Databases[0].URL, "libsql://orbtr-sessions.example"},
		{"orbtr-sessions token", m.Groups[1].Databases[0].AuthToken, "sessions-token"},
		{"lad key", m.Groups[2].EncryptionKey, testKey},
		{"lad token", m.Groups[2].AuthToken, ""},
		{"turso apiToken", m.Turso.APIToken, "org-token"},
		{"tenantDB authToken", m.PartitionDB.AuthToken, "partition-token"},
	} {
		if f.got != f.want {
			t.Errorf("%s holds the wrong value after FillSecrets", f.name)
		}
	}
	if slices.Contains(env.read, "MESHDB_LAD_AUTH_TOKEN") {
		t.Error("a pureLocal group's token variable was read")
	}
	if err := m.ValidateResolved(); err != nil {
		t.Fatalf("after FillSecrets: ValidateResolved = %v, want nil", err)
	}
}

func TestFillSecretsKeepsADocumentValue(t *testing.T) {
	c := hostConfig()
	g := &c.Networking.MeshDB.Groups[0]
	g.EncryptionKey = otherKey
	g.Databases[0].URL = "libsql://from-the-document.example"
	g.Databases[0].AuthToken = "document-token"
	c.Networking.MeshDB.Turso.APIToken = "document-org-token"
	env := hostEnv()
	if err := c.FillSecrets(env.lookup); err != nil {
		t.Fatalf("FillSecrets = %v", err)
	}
	g = &c.Networking.MeshDB.Groups[0]
	if g.EncryptionKey != otherKey || g.Databases[0].URL != "libsql://from-the-document.example" ||
		g.Databases[0].AuthToken != "document-token" || c.Networking.MeshDB.Turso.APIToken != "document-org-token" {
		t.Fatal("FillSecrets replaced a value the document carries")
	}
	for _, name := range []string{"MESHDB_ORBTR_ENCRYPTION_KEY", "MESHDB_ORBTR_ORBTR_ENDPOINT_URL", EnvTursoAPIToken} {
		if slices.Contains(env.read, name) {
			t.Errorf("%s was read although the document sets its setting", name)
		}
	}
	if c.Networking.MeshDB.Groups[1].EncryptionKey != otherKey {
		t.Fatal("a setting the document leaves empty was not filled beside the kept ones")
	}
}

func TestFillSecretsRefusesNamesThatShareAVariable(t *testing.T) {
	for name, groups := range map[string][]Group{
		"a-b and a_b keys": {{GroupName: "a-b"}, {GroupName: "a_b"}},
		"database token and group token": {
			{GroupName: "a", Databases: []Database{{Name: "b"}}},
			{GroupName: "a_b"},
		},
		"prior key and key": {{GroupName: "a"}, {GroupName: "a_prior"}},
	} {
		c := Config{Networking: Networking{MeshDB: MeshDB{Enabled: true, Groups: groups}}}
		env := &fakeEnv{}
		err := c.FillSecrets(env.lookup)
		if !errors.Is(err, ErrSecretNameCollision) {
			t.Errorf("%s: FillSecrets = %v, want ErrSecretNameCollision", name, err)
			continue
		}
		if len(env.read) != 0 {
			t.Errorf("%s: %d variables read before the refusal", name, len(env.read))
		}
	}
	err := (&Config{Networking: Networking{MeshDB: MeshDB{Enabled: true, Groups: []Group{{GroupName: "a-b"}, {GroupName: "a_b"}}}}}).FillSecrets((&fakeEnv{}).lookup)
	for _, want := range []string{"MESHDB_A_B_ENCRYPTION_KEY", "groups[a-b].encryptionKey", "groups[a_b].encryptionKey"} {
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("collision error %v does not name %s", err, want)
		}
	}

	c := Config{Networking: Networking{MeshDB: MeshDB{Enabled: true, Groups: []Group{{GroupName: "a-b"}, {GroupName: "a-c"}}}}}
	if err := c.FillSecrets((&fakeEnv{}).lookup); err != nil {
		t.Fatalf("distinct names: FillSecrets = %v, want nil", err)
	}
}

func TestFillSecretsRefusesAMalformedKeyFromTheEnvironment(t *testing.T) {
	const bad = "not-a-32-byte-key-value"
	for _, name := range []string{"MESHDB_ORBTR_ENCRYPTION_KEY", "MESHDB_ORBTR_PRIOR_ENCRYPTION_KEY"} {
		c := hostConfig()
		env := hostEnv()
		env.vals[name] = bad
		err := c.FillSecrets(env.lookup)
		if !errors.Is(err, ErrBadEncryptionKey) {
			t.Errorf("%s malformed: FillSecrets = %v, want ErrBadEncryptionKey", name, err)
			continue
		}
		if !strings.Contains(err.Error(), name) || !strings.Contains(err.Error(), "networking.meshdb.groups[orbtr].") {
			t.Errorf("error %q does not name %s and its setting", err, name)
		}
		if strings.Contains(err.Error(), bad) {
			t.Errorf("error %q carries the value", err)
		}
	}
	c := hostConfig()
	env := hostEnv()
	env.vals["MESHDB_ORBTR_PRIOR_ENCRYPTION_KEY"] = otherKey
	if err := c.FillSecrets(env.lookup); err != nil {
		t.Fatalf("32-byte keys: FillSecrets = %v, want nil", err)
	}
}

func TestFillSecretsOnADisabledMeshDBReadsNothing(t *testing.T) {
	c := hostConfig()
	c.Networking.MeshDB.Enabled = false
	env := hostEnv()
	if err := c.FillSecrets(env.lookup); err != nil {
		t.Fatalf("disabled: FillSecrets = %v, want nil", err)
	}
	if len(env.read) != 0 || c.Networking.MeshDB.Groups[0].EncryptionKey != "" {
		t.Fatalf("disabled: %d variables read, key filled = %t", len(env.read), c.Networking.MeshDB.Groups[0].EncryptionKey != "")
	}
	c.Networking.MeshDB.Enabled = true
	if err := c.FillSecrets(env.lookup); err != nil || len(env.read) == 0 {
		t.Fatalf("enabled: FillSecrets = %v with %d variables read, want reads", err, len(env.read))
	}
}

// A struct tag names one variable, so only the two deployment-wide tokens keep
// one, and each names the variable FillSecrets reads for it.
func TestTheKeptEnvTagsNameTheVariablesFillSecretsReads(t *testing.T) {
	tag := func(v any, field string) string {
		f, ok := reflect.TypeOf(v).FieldByName(field)
		if !ok {
			t.Fatalf("%T has no field %s", v, field)
		}
		return f.Tag.Get("env")
	}
	if got := tag(Turso{}, "APIToken"); got != EnvTursoAPIToken {
		t.Errorf("Turso.APIToken env tag = %q, want %q", got, EnvTursoAPIToken)
	}
	if got := tag(PartitionDB{}, "AuthToken"); got != EnvPartitionAuthToken {
		t.Errorf("PartitionDB.AuthToken env tag = %q, want %q", got, EnvPartitionAuthToken)
	}
	for _, f := range []struct {
		v     any
		field string
	}{
		{Group{}, "AuthToken"}, {Group{}, "EncryptionKey"}, {Group{}, "PriorEncryptionKey"},
		{Database{}, "URL"}, {Database{}, "AuthToken"},
	} {
		if got := tag(f.v, f.field); got != "" {
			t.Errorf("%T.%s carries env tag %q, which would serve every group", f.v, f.field, got)
		}
	}
}
