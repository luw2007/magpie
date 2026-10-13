package provider

// A backup carries providers.json's entries as stored, including quota bindings.
import (
	"fmt"
	"maps"
	"slices"
)

// Stored is the providers as providers.json keeps them: keys included,
// signed-in accounts only as the model picks the user made for them. A
// file that can't be read is an error, never none: a backup or a sync
// carrying none would take every provider away where it is put back.
func Stored() ([]Provider, error) {
	f, err := read()
	return f.Providers, err
}

// Restore merges providers while retaining this computer's usage configuration.
func Restore(ps []Provider) (added, replaced int, err error) {
	return RestoreConfiguration(ps, nil, nil, nil, false)
}

// StoredGroups is the groups as saved: the user's own, and the found ones
// the user removed. As with Stored, a file that can't be read is an error.
func StoredGroups() ([]Group, error) {
	f, err := read()
	return f.Groups, err
}

// StoredUsageSources returns the usage sources configured.
func StoredUsageSources() []UsageSource { return load().Sources }

// StoredQuotaPools returns the quota pools configured.
func StoredQuotaPools() []QuotaPool { return load().QuotaPools }

// RestoreConfiguration merges a complete configuration before validating any
// references. keyed says the configuration carries credentials: a provider in
// it with none of its own then clears the ones stored here. Without keys
// (keyed false) such a provider retains those already stored here.
func RestoreConfiguration(ps []Provider, gs []Group, sources []UsageSource, pools []QuotaPool, keyed bool) (added, replaced int, err error) {
	f, err := usageConfig()
	if err != nil {
		return 0, 0, err
	}
	if err = backupUsage(&f, sources, pools, false); err != nil {
		return 0, 0, err
	}
	for _, p := range ps {
		p.IconURL = ""
		if p.ID == "" || p.ID != Slug(p.ID) || p.ID == "magpie" {
			continue
		}
		i := slices.IndexFunc(f.Providers, func(x Provider) bool { return x.ID == p.ID })
		if i < 0 {
			f.Providers = append(f.Providers, p)
			added++
			continue
		}
		preserveBackupKeys(&p, f.Providers[i], keyed)
		f.Providers[i] = p
		replaced++
	}
	for _, g := range gs {
		if g.ID == "" || g.ID != GroupSlug(g.ID) {
			continue
		}
		g.Auto = false
		if i := slices.IndexFunc(f.Groups, func(x Group) bool { return x.ID == g.ID }); i >= 0 {
			f.Groups[i] = g
		} else {
			f.Groups = append(f.Groups, g)
		}
	}
	if err = validateBackupReferences(f); err != nil {
		return 0, 0, err
	}
	return added, replaced, store(f)
}

// Mirror replaces providers and groups, retaining the usage configuration.
func Mirror(ps []Provider, gs []Group) error {
	f, err := usageConfig()
	if err != nil {
		return err
	}
	return MirrorConfiguration(ps, gs, f.Sources, f.QuotaPools, false)
}

// MirrorConfiguration replaces the complete linked configuration in one write.
// keyed is RestoreConfiguration's: credentials came with the configuration.
func MirrorConfiguration(ps []Provider, gs []Group, sources []UsageSource, pools []QuotaPool, keyed bool) error {
	f, err := usageConfig()
	if err != nil {
		return err
	}
	if err = backupUsage(&f, sources, pools, true); err != nil {
		return err
	}
	here := f.Providers
	f.Providers = nil
	for _, p := range ps {
		p.IconURL = ""
		if p.ID == "" || p.ID != Slug(p.ID) || p.ID == "magpie" {
			continue
		}
		if i := slices.IndexFunc(here, func(x Provider) bool { return x.ID == p.ID }); i >= 0 {
			preserveBackupKeys(&p, here[i], keyed)
		}
		f.Providers = append(f.Providers, p)
	}
	f.Groups = slices.DeleteFunc(slices.Clone(gs), func(g Group) bool { return g.ID == "" || g.ID != GroupSlug(g.ID) })
	if err = validateBackupReferences(f); err != nil {
		return err
	}
	return store(f)
}

// PreserveBackupKeys retains omitted credentials without changing key identities.
// keyed says p came with credentials, so one holding none of its own is an
// explicit clear and keeps nothing of h.
func PreserveBackupKeys(p *Provider, h Provider, keyed bool) { preserveBackupKeys(p, h, keyed) }

func preserveBackupKeys(p *Provider, h Provider, keyed bool) {
	keyless := p.Key == "" && !slices.ContainsFunc(p.Keys, func(k KeyAccount) bool { return k.Key != "" })
	if keyless && keyed {
		return
	}
	// The provider-wide secrets (balance token, access key pair) belong to the
	// account its selected primary credential signs in to; a spare key shared
	// with this machine's does not make them the same account (KeepSecrets).
	pk := primaryKey(*p)
	sameAccount := keyless || pk != "" && pk == primaryKey(h)
	if keyless {
		p.Headers = maps.Clone(p.Headers)
		for name, value := range h.Headers {
			if _, set := p.Headers[name]; !set && SecretHeader(name) {
				if p.Headers == nil {
					p.Headers = map[string]string{}
				}
				p.Headers[name] = value
			}
		}
	}
	if sameAccount && p.BalanceToken == "" {
		p.BalanceToken = h.BalanceToken
	}
	if sameAccount && p.AccessKeyID == "" && p.SecretAccessKey == "" {
		p.AccessKeyID, p.SecretAccessKey = h.AccessKeyID, h.SecretAccessKey
	}
	if p.Key == "" && len(p.Keys) == 0 {
		p.Keys = slices.Clone(h.Keys)
	}
	p.Keys = slices.Clone(p.Keys)
	for i := range p.Keys {
		if p.Keys[i].Key == "" {
			if j := slices.IndexFunc(h.Keys, func(k KeyAccount) bool { return k.ID == p.Keys[i].ID }); j >= 0 {
				p.Keys[i].Key = h.Keys[j].Key
			}
		}
	}
	if keyless {
		for _, k := range h.Keys {
			if !slices.ContainsFunc(p.Keys, func(in KeyAccount) bool { return in.ID == k.ID }) {
				p.Keys = append(p.Keys, k)
			}
		}
	}
	p.normalizeKeys()
}

func backupUsage(f *file, sources []UsageSource, pools []QuotaPool, mirror bool) error {
	here := f.Sources
	if mirror {
		f.Sources = nil
		f.QuotaPools = nil
	}
	for _, s := range sources {
		if s.Credential == "" && s.CredentialEnv == "" {
			if i := slices.IndexFunc(here, func(x UsageSource) bool { return x.ID == s.ID }); i >= 0 {
				s.Credential = here[i].Credential
			}
		}
		// Missing credentials are intentional in a keyless backup. Validate all
		// other fields without inventing a persisted credential.
		if err := validateSourceFields(s, false); err != nil {
			return fmt.Errorf("source %s: %w", s.ID, err)
		}
		if i := slices.IndexFunc(f.Sources, func(x UsageSource) bool { return x.ID == s.ID }); i >= 0 {
			f.Sources[i] = s
		} else {
			f.Sources = append(f.Sources, s)
		}
	}
	for _, p := range pools {
		if i := slices.IndexFunc(f.QuotaPools, func(x QuotaPool) bool { return x.ID == p.ID }); i >= 0 {
			f.QuotaPools[i] = p
		} else {
			f.QuotaPools = append(f.QuotaPools, p)
		}
	}
	for _, p := range f.QuotaPools {
		if err := validatePool(p, f.Sources); err != nil {
			return err
		}
	}
	return nil
}

func validateBackupReferences(f file) error {
	for _, p := range f.Providers {
		for _, k := range p.Keys {
			for _, ref := range k.PoolRefs {
				if !slices.ContainsFunc(f.QuotaPools, func(q QuotaPool) bool { return q.ID == ref }) {
					return fmt.Errorf("provider %s key %s references missing pool %s", p.ID, k.ID, ref)
				}
			}
		}
	}
	return nil
}
