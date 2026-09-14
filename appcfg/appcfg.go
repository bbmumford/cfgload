/*
 * Copyright (c) 2026 HSTLES / ORBTR Pty Ltd. All Rights Reserved.
 * Queries: licensing@hstles.com
 */

// Package appcfg is the settings template an HTTP application loads at boot:
// what it is called, how it logs, how it serves, and whether it is gated.
//
// It holds NO identity level. The one scope it carries is an opaque Partition
// string this package never interprets — no notion of owner, customer, org or
// tenant attaches to it here. What the value MEANS is the consuming
// application's to decide, which is what lets one template serve applications
// that disagree about how their world is divided.
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
