/*
 * Copyright (c) 2026 HSTLES / ORBTR Pty Ltd. All Rights Reserved.
 * Queries: licensing@hstles.com
 */

// Package appcfg is the settings template an HTTP application loads at boot:
// what it is called, how it logs, how it serves, and whether it is gated.
//
// It carries no scope at all — not an identity level, and not a partition
// either. A request's partition is established per request from the credential
// it proves, so a config-carried one could only name the deployment, which is a
// different thing wearing the same word. AppSettings says the same at the point
// where such a field would otherwise sit.
//
// A document that still sets an old scope key is not rejected: unmarshalling
// ignores a key no field claims, so the value is read by nothing. That is why
// such a key can be left in place — it is inert, not pending.
package appcfg

// Config is the whole template. Every section is optional in YAML; a missing
// section leaves its fields at their zero values, which each field's own
// documentation treats as a deliberate state rather than an error.
type Config struct {
	App         AppSettings         `yaml:"app" json:"app"`
	UI          UISettings          `yaml:"ui" json:"ui"`
	Security    SecuritySettings    `yaml:"security" json:"security"`
	Mesh        MeshSettings        `yaml:"mesh" json:"mesh"`
	Maintenance MaintenanceSettings `yaml:"maintenance" json:"maintenance"`
	// Signature covers the document when the loader is configured to verify it.
	Signature string `yaml:"signature,omitempty" json:"signature,omitempty"`
}

// AppSettings is what the application calls itself and how it reports.
type AppSettings struct {
	Name        string `yaml:"name" json:"name"`
	Domain      string `yaml:"domain" json:"domain"`
	Environment string `yaml:"environment" json:"environment"`
	LogLevel    string `yaml:"logLevel" json:"logLevel"`
	// DebugLog is separate from LogLevel because it turns on verbose tracing
	// rather than lowering a threshold.
	DebugLog bool `yaml:"debugLog" json:"debugLog"`

	// (No partition here. A deployment does not have one: a request's partition is
	// established per-request from the credential it proves. A config-carried
	// partition could only ever name the deployment, which is a different thing
	// wearing the same word.)

	// AllowedCORSDomains each admit the exact host and its subdomains.
	AllowedCORSDomains []string `yaml:"allowedCORSDomains" json:"allowedCORSDomains"`
}

type UISettings struct {
	TemplateCacheEnabled bool   `yaml:"templateCacheEnabled" json:"templateCacheEnabled"`
	StaticCacheMaxAge    string `yaml:"staticCacheMaxAge" json:"staticCacheMaxAge"`
}

type SecuritySettings struct {
	CookieName string `yaml:"cookieName" json:"cookieName"`
}

// MeshSettings gates the bootstrap endpoints. Only nodes that others bootstrap
// FROM should expose them; an ordinary application answering bootstrap requests
// advertises a role it cannot serve.
type MeshSettings struct {
	ExposeBootstrap bool `yaml:"exposeBootstrap" json:"exposeBootstrap"`
}

// MaintenanceSettings closes the application to everyone not allow-listed.
// AllowedIPs empty means nobody bypasses — an allow-list that admitted everyone
// when unset would open the gate precisely when it is meant to be shut.
type MaintenanceSettings struct {
	Enabled    bool     `yaml:"enabled" json:"enabled"`
	Reason     string   `yaml:"reason" json:"reason"`
	AllowedIPs []string `yaml:"allowedIPs" json:"allowedIPs"`
}
