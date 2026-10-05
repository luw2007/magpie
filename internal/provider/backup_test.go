package provider

import (

	"slices"
	"path/filepath"

	"testing"
)

// Restoring and syncing a keyless provider keep this machine's balance
// token. A token supplied explicitly, or keys supplied with none, replaces it.
func TestBackupBalanceToken(t *testing.T) {
	for _, op := range []struct {
		name string
		put  func([]Provider) error
	}{
		{"restore", func(ps []Provider) error { _, _, err := RestoreConfiguration(ps, nil, nil, nil); return err }},
		{"mirror", func(ps []Provider) error { return MirrorConfiguration(ps, nil, nil, nil) }},
	} {
		t.Run(op.name, func(t *testing.T) {
			for _, c := range []struct {
				name     string
				incoming Provider
				want     string
			}{
				{"without keys", Provider{}, "balance-here"},
				{"explicit token without keys", Provider{BalanceToken: "balance-new"}, "balance-new"},
				{"primary key with token", Provider{Key: "sk-new", BalanceToken: "balance-new"}, "balance-new"},
				{"primary key without token", Provider{Key: "sk-new"}, ""},
				{"spare key with token", Provider{Keys: []KeyAccount{{Key: "sk-new"}}, BalanceToken: "balance-new"}, "balance-new"},
				{"spare key without token", Provider{Keys: []KeyAccount{{Key: "sk-new"}}}, ""},
			} {
				t.Run(c.name, func(t *testing.T) {
					isolate(t)
					h := t.TempDir()
					t.Setenv("HOME", h)
					t.Setenv("USERPROFILE", h)
					t.Setenv("XDG_CONFIG_HOME", filepath.Join(h, ".config"))
					t.Setenv("XDG_CACHE_HOME", filepath.Join(h, ".cache"))
					t.Setenv("PATH", h)
					here := Provider{ID: "relay", Name: "Relay old", Chat: "https://old.example.com/v1",
						Keys: []KeyAccount{{ID: "main", Name: "main", Key: "sk-here", Protocol: Chat}, {ID: "spare", Name: "spare", Key: "sk-spare", Protocol: Anthropic}}, BalanceToken: "balance-here"}
					here.normalizeKeys()
					if err := Save(here); err != nil {
						t.Fatal(err)
					}
					p := c.incoming
					p.ID, p.Name, p.Chat = "relay", "Relay new", "https://new.example.com/v1"
					p.normalizeKeys()
					if err := op.put([]Provider{p}); err != nil {
						t.Fatal(err)
					}
					ps, _ := Stored()
					if len(ps) != 1 {
						t.Fatalf("providers: %+v", ps)
					}
					got := ps[0]
					if got.BalanceToken != c.want || got.Name != p.Name || got.Chat != p.Chat {
						t.Fatalf("restored: %+v, want balance token %q and the incoming name and URL", got, c.want)
					}
					wantKey := p.Key
					if wantKey == "" && len(p.Keys) == 0 {
						wantKey = here.Key
					}
					wantKeys := p.Keys
					if len(wantKeys) == 0 && p.Key == "" {
						wantKeys = here.Keys
					}
					if got.Key != wantKey || len(got.Keys) != len(wantKeys) {
						t.Fatalf("keys: %+v, want key %q and %d keys", got, wantKey, len(wantKeys))
					}
					for i := range got.Keys {
						if got.Keys[i].Key != wantKeys[i].Key || got.Keys[i].Name != wantKeys[i].Name ||
							got.Keys[i].Protocol != wantKeys[i].Protocol || got.Keys[i].Off != wantKeys[i].Off ||
							!slices.Equal(got.Keys[i].PoolRefs, wantKeys[i].PoolRefs) ||
							!slices.Equal(got.Keys[i].Models, wantKeys[i].Models) {
							t.Fatalf("key[%d]: %+v, want %+v", i, got.Keys[i], wantKeys[i])
						}
					}
				})
			}
		})
	}
}