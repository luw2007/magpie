package provider

// A backup carries providers.json's entries as stored, including quota bindings,
// and puts them back on another machine (see internal/backup).

import (
	"context"
	"fmt"
	"maps"
	"slices"

	"github.com/yetone/magpie/internal/plugin"
)

// handOver is HandOver: a var so a test can see it asked.
var handOver = HandOver

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

// Carried is the plugins a backup or a sync carries: those added from npm
// or a git repository, on or off as the user left them, with their options:
// without keys, those whose names look like a key are left out, as a
// provider's secret headers are. A plugin added from a file or folder is
// this computer's alone, and a plugin's sign-ins (plugin-auth.json) stay
// here, as a subscription's do.
func Carried(keys bool) []plugin.Entry {
	var out []plugin.Entry
	for _, e := range plugin.Load().Plugins {
		if plugin.IsPath(e.Spec) {
			continue
		}
		if !keys {
			e.Options = withoutSecretOptions(e.Options)
		}
		out = append(out, e)
	}
	return out
}

// withoutSecretOptions is opts without the values whose names look like a
// key, at any depth: nil when nothing is left.
func withoutSecretOptions(opts map[string]any) map[string]any {
	var strip func(v any) any
	strip = func(v any) any {
		switch x := v.(type) {
		case map[string]any:
			out := map[string]any{}
			for k, v := range x {
				if !SecretHeader(k) {
					out[k] = strip(v)
				}
			}
			return out
		case []any:
			out := make([]any, len(x))
			for i, v := range x {
				out[i] = strip(v)
			}
			return out
		}
		return v
	}
	out, _ := strip(map[string]any(opts)).(map[string]any)
	if len(out) == 0 {
		return nil
	}
	return out
}

// AddPlugins installs the plugins another computer has (Carried) that
// aren't here, the way Settings → Plugins adds one, and hands each a
// deprecated built-in it serves (HandOver). It never takes a plugin away
// or changes one already here: a plugin removed on one computer stays on
// the others. It returns the specs added and why the others couldn't be.
func AddPlugins(ctx context.Context, es []plugin.Entry) (added []string, failed map[string]error) {
	here := map[string]bool{}
	for _, e := range plugin.Load().Plugins {
		here[plugin.Name(e.Spec)] = true
	}
	for _, e := range es {
		if e.Spec == "" || plugin.IsPath(e.Spec) || here[plugin.Name(e.Spec)] {
			continue
		}
		here[plugin.Name(e.Spec)] = true
		got, err := addPlugin(ctx, e.Spec)
		if err == nil && e.Off {
			err = plugin.SetOff(got.Spec, true)
		}
		if err == nil && len(e.Options) > 0 {
			err = plugin.SetOptions(got.Spec, e.Options)
		}
		if err != nil {
			if failed == nil {
				failed = map[string]error{}
			}
			failed[e.Spec] = err
			continue
		}
		added = append(added, e.Spec)
	}
	if len(added) > 0 {
		handOver(ctx, false)
	}
	return added, failed
}

// NotSignedIn is the subscriptions among ps (as a backup or a sync brings
// them: their model picks and settings alone) that no account here is
// signed in to: they are listed once one is, and are to be signed in to on
// this computer. A subscription the user removed from magpie is not one.
func NotSignedIn(ps []Provider) []string {
	have := map[string]bool{}
	for _, p := range All() {
		have[p.ID] = true
	}
	var out []string
	for _, p := range ps {
		if p.ID == "" || !OnlyPicks(p) || p.Hidden || have[p.ID] || slices.Contains(out, p.ID) {
			continue
		}
		out = append(out, p.ID)
	}
	return out
}

// OnlyPicks is whether p, as providers.json keeps it, is a subscription's
// model picks and settings alone, with no endpoint or key of its own: it
// is signed in to, never given a key.
func OnlyPicks(p Provider) bool { return !hasEndpoint(normalize(p)) }
