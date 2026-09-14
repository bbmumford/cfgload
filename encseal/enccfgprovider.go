/*
 * Copyright (c) 2026 ORBTR AI Pty Ltd. All Rights Reserved.
 */

package encseal

import (
	"context"
	"errors"
	"fmt"

	"github.com/bbmumford/cfgload/enccfg"
)

// ErrWrapped is returned for material a KEK sealed. enccfg records that a
// version's key is wrapped; unwrapping needs the KEK, which this package does
// not hold.
var ErrWrapped = errors.New("encseal: key material is wrapped and no Unwrapper is configured")

// Unwrapper turns KEK-wrapped material into the raw key. An application whose
// enccfg carries wrapped keys supplies one; without it wrapped material is
// refused rather than used.
type Unwrapper interface {
	Unwrap(ctx context.Context, wrapped string) ([]byte, error)
}

// EnccfgProvider answers from the enccfg registry, for applications whose key
// material is loaded from config.
//
// It is one KeyProvider among others: an application holding its keys in a vault
// implements the interface over that instead, and the Sealer is unchanged.
type EnccfgProvider struct {
	// Unwrapper resolves KEK-wrapped material. Nil refuses every wrapped
	// version — which is the safe direction, because the wrapped blob is not a
	// key and sealing under it would produce data nothing can open.
	Unwrapper Unwrapper
}

var _ KeyProvider = EnccfgProvider{}

// CurrentVersion reports the version enccfg says this domain seals with.
func (p EnccfgProvider) CurrentVersion(_ context.Context, domain string) (string, error) {
	return enccfg.CurrentVersion(domain), nil
}

// Key resolves one version's material, unwrapping it when enccfg reports the
// wrapped form.
func (p EnccfgProvider) Key(ctx context.Context, domain, version string) ([]byte, bool, error) {
	material, wrapped, found := enccfg.KeyMaterial(domain, version)
	if !found {
		return nil, false, nil
	}
	if !wrapped {
		return []byte(material), true, nil
	}
	if p.Unwrapper == nil {
		return nil, false, fmt.Errorf("%w: %s/%s", ErrWrapped, domain, version)
	}
	raw, err := p.Unwrapper.Unwrap(ctx, material)
	if err != nil {
		return nil, false, err
	}
	return raw, len(raw) > 0, nil
}

// Sealable defers to enccfg, which answers false for an unknown domain, an
// unknown version and an unregistered config — not only for a status that says
// so. Absence of evidence about a key is not evidence it is fit to use.
func (p EnccfgProvider) Sealable(_ context.Context, domain, version string) bool {
	return enccfg.Usable(domain, version)
}
