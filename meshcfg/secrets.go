/*
 * Copyright (c) 2026 HSTLES / ORBTR Pty Ltd. All Rights Reserved.
 * Queries: licensing@hstles.com
 */
package meshcfg

import (
	"errors"
	"fmt"
	"strings"
)

// The mesh document carries no key or token values: cfgload.Load fetches it
// from a file or URL, its signature proves integrity only, and anyone who can
// fetch it can read it. Each secret instead comes from an environment variable
// whose name derives from the group and database names. The caller reads the
// variables (an endpoint through its own requirement registry) and hands them
// to FillSecrets as lookup; this package reads no environment itself.

// SecretField is a setting FillSecrets may supply. Its value is the setting's
// document key, which errors name and EnvName derives the variable suffix from.
type SecretField string

// The settings of a group or of one of its databases that FillSecrets supplies.
const (
	FieldEncryptionKey      SecretField = "encryptionKey"
	FieldPriorEncryptionKey SecretField = "priorEncryptionKey"
	FieldAuthToken          SecretField = "authToken"
	FieldURL                SecretField = "url"
)

// Variables of the two deployment-wide tokens. The env tags on Turso.APIToken
// and PartitionDB.AuthToken name these, and FillSecrets reads them.
const (
	EnvTursoAPIToken      = "TURSO_API_TOKEN"
	EnvPartitionAuthToken = "TURSO_TENANT_AUTH_TOKEN"
)

// ErrSecretNameCollision reports two settings whose variable names derive to
// the same string, so one variable would supply both.
var ErrSecretNameCollision = errors.New("meshcfg: two settings map to one environment variable")

// EnvName is the variable that supplies field for a group (db empty) or for one
// of its databases: MESHDB_<G>_<FIELD> or MESHDB_<G>_<D>_<FIELD>. <G> and <D>
// are the names upper-cased with '-' written as '_'; <FIELD> is the field's
// document key in upper snake case. Group orbtr-sessions, database
// orbtr-sessions, field url gives MESHDB_ORBTR_SESSIONS_ORBTR_SESSIONS_URL.
//
// FillSecrets reads a group's encryptionKey, priorEncryptionKey and authToken,
// and a database's url and authToken; no other combination is read.
func EnvName(group, db string, field SecretField) string {
	parts := []string{"MESHDB", envWord(group)}
	if db != "" {
		parts = append(parts, envWord(db))
	}
	return strings.Join(append(parts, upperSnake(string(field))), "_")
}

func envWord(name string) string {
	return strings.ToUpper(strings.ReplaceAll(name, "-", "_"))
}

// upperSnake turns a camelCase document key into UPPER_SNAKE.
func upperSnake(key string) string {
	var b strings.Builder
	for i, r := range key {
		if i > 0 && r >= 'A' && r <= 'Z' {
			b.WriteByte('_')
		}
		b.WriteRune(r)
	}
	return strings.ToUpper(b.String())
}

// settingPath is the document path of a group's (db empty) or a database's
// field, so an error tells the operator where the setting lives.
func settingPath(group, db string, field SecretField) string {
	if db == "" {
		return fmt.Sprintf("networking.meshdb.groups[%s].%s", group, field)
	}
	return fmt.Sprintf("networking.meshdb.groups[%s].databases[%s].%s", group, db, field)
}

// secret is one setting FillSecrets may supply: where it lives in the document,
// the variable that supplies it, the field itself, and the check a supplied
// value must pass (nil for none).
type secret struct {
	path  string
	env   string
	dst   *string
	check func(string) error
}

func checkKey(v string) error {
	_, err := DecodeEncryptionKey(v)
	return err
}

// secrets lists every setting the environment may supply, in document order.
// A pureLocal group has no url or token to supply.
func (m *MeshDB) secrets() []secret {
	var out []secret
	for gi := range m.Groups {
		g := &m.Groups[gi]
		n := g.GroupName
		out = append(out,
			secret{settingPath(n, "", FieldEncryptionKey), EnvName(n, "", FieldEncryptionKey), &g.EncryptionKey, checkKey},
			secret{settingPath(n, "", FieldPriorEncryptionKey), EnvName(n, "", FieldPriorEncryptionKey), &g.PriorEncryptionKey, checkKey},
		)
		if g.PureLocal {
			continue
		}
		out = append(out, secret{settingPath(n, "", FieldAuthToken), EnvName(n, "", FieldAuthToken), &g.AuthToken, nil})
		for di := range g.Databases {
			d := &g.Databases[di]
			out = append(out,
				secret{settingPath(n, d.Name, FieldURL), EnvName(n, d.Name, FieldURL), &d.URL, nil},
				secret{settingPath(n, d.Name, FieldAuthToken), EnvName(n, d.Name, FieldAuthToken), &d.AuthToken, nil},
			)
		}
	}
	return append(out,
		secret{"networking.meshdb.turso.apiToken", EnvTursoAPIToken, &m.Turso.APIToken, nil},
		secret{"networking.meshdb.tenantDB.authToken", EnvPartitionAuthToken, &m.PartitionDB.AuthToken, nil},
	)
}

// FillSecrets supplies, through lookup, every secret setting of an enabled
// MeshDB that the configuration leaves empty: each group's encryptionKey and
// priorEncryptionKey, a shared group's authToken, and each Databases entry's
// url and authToken, from the variables EnvName derives; Turso.APIToken from
// EnvTursoAPIToken; PartitionDB.AuthToken from EnvPartitionAuthToken.
//
// A value the configuration carries is kept and its variable is not read, so
// the document wins. A variable that is unset or empty supplies nothing. A key
// taken from the environment must decode to 32 bytes. Two settings whose
// variable names derive to the same string are refused before any variable is
// read, because that variable would silently supply both. A disabled MeshDB
// reads nothing. After an error the configuration may be partly filled.
//
// It enforces no requirement; MeshDB.ValidateResolved does, afterwards. Errors
// name the variable and the setting, never a value.
func (c *Config) FillSecrets(lookup func(name string) (string, bool)) error {
	m := &c.Networking.MeshDB
	if !m.Enabled {
		return nil
	}
	if lookup == nil {
		return errors.New("meshcfg: FillSecrets needs a lookup")
	}
	all := m.secrets()
	owner := make(map[string]string, len(all))
	for _, s := range all {
		if prev, ok := owner[s.env]; ok && prev != s.path {
			return fmt.Errorf("%w: %s would supply both %s and %s", ErrSecretNameCollision, s.env, prev, s.path)
		}
		owner[s.env] = s.path
	}
	for _, s := range all {
		if *s.dst != "" {
			continue
		}
		v, ok := lookup(s.env)
		if !ok || v == "" {
			continue
		}
		if s.check != nil {
			if err := s.check(v); err != nil {
				return fmt.Errorf("%w: %s, which supplies %s", err, s.env, s.path)
			}
		}
		*s.dst = v
	}
	return nil
}
