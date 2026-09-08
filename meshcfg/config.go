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

// Config is the whole mesh configuration a file deserialises into.
type Config struct {
	Networking Networking `json:"networking" yaml:"networking"`
	Dispatch   Dispatch   `json:"dispatch" yaml:"dispatch"`
}

// Dispatch tunes the caller side. A hot callee declares which of its methods are
// safe to answer from a caller-side cache, so a repeated read returns without a
// mesh round trip.
type Dispatch struct {
	CacheableHandlers []CacheableHandler `json:"cacheableHandlers,omitempty" yaml:"cacheableHandlers,omitempty"`
}

// CacheableHandler marks one method cacheable at the caller for a bounded TTL.
//
// IDEMPOTENT READS ONLY. A cached response is served without reaching the callee,
// so a method with side effects listed here silently stops performing them.
type CacheableHandler struct {
	// FQN identifies the method, in whatever form the dispatcher matches on. This
	// package compares it as an opaque key and never parses it.
	FQN string `json:"fqn" yaml:"fqn"`
	// TTL bounds staleness. Long enough to absorb a burst of identical reads, short
	// enough that a stale answer stays acceptable.
	TTL string `json:"ttl" yaml:"ttl"`
}

// ErrBadTTL reports a cacheable-handler TTL that will not parse or is not positive.
var ErrBadTTL = errors.New("meshcfg: unusable cacheable-handler TTL")

// CacheableHandlerMap renders the list as the FQN→TTL map a dispatcher consumes.
//
// An entry with an empty FQN, an unparseable TTL, or a non-positive one is DROPPED
// rather than defaulted: the safe reading of a broken cache directive is "do not
// cache this", never "cache it for some duration nobody wrote". Validate is what
// reports those entries, so a typo is a startup failure instead of a silently
// uncached method.
func (d Dispatch) CacheableHandlerMap() map[string]time.Duration {
	out := make(map[string]time.Duration, len(d.CacheableHandlers))
	for _, h := range d.CacheableHandlers {
		if h.FQN == "" || h.TTL == "" {
			continue
		}
		ttl, err := time.ParseDuration(h.TTL)
		if err != nil || ttl <= 0 {
			continue
		}
		out[h.FQN] = ttl
	}
	return out
}

// Validate reports the first unusable cacheable-handler entry.
//
// It exists because CacheableHandlerMap drops them silently, which is the right
// runtime behaviour and the wrong startup behaviour: dropped means the method is
// simply not cached, so a mistyped TTL costs latency nobody can see and nobody
// attributes to a config typo.
func (d Dispatch) Validate() error {
	for i, h := range d.CacheableHandlers {
		if h.FQN == "" {
			return fmt.Errorf("%w: entry %d has no FQN", ErrBadTTL, i)
		}
		if h.TTL == "" {
			return fmt.Errorf("%w: %s has no TTL", ErrBadTTL, h.FQN)
		}
		ttl, err := time.ParseDuration(h.TTL)
		if err != nil {
			return fmt.Errorf("%w: %s = %q", ErrBadTTL, h.FQN, h.TTL)
		}
		if ttl <= 0 {
			return fmt.Errorf("%w: %s = %q is not positive", ErrBadTTL, h.FQN, h.TTL)
		}
	}
	return nil
}

// Validate checks the whole configuration.
func (c Config) Validate() error {
	if err := c.Networking.Validate(); err != nil {
		return err
	}
	return c.Dispatch.Validate()
}

// ParseDuration returns s parsed, or fallback when s is empty OR malformed.
//
// The fallback is the CALLER's, not this package's: a consumer wanting a 10s dial
// timeout where the schema default is 5s passes its own, and the value it names in
// its own code is the one that applies. The package-level Default* constants back
// the typed accessors instead, for a consumer that has no opinion.
//
// Malformed falls back rather than erroring so a running node degrades instead of
// dying; Config.Validate is what refuses to let the typo start.
func ParseDuration(s string, fallback time.Duration) time.Duration {
	return duration(s, fallback)
}
