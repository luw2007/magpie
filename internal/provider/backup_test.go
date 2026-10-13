package provider

import (
	"path/filepath"
	"slices"
	"testing"
)

// Restoring and syncing a keyless provider keep this machine's balance
// token. A token supplied explicitly, or keys supplied with none, replaces it.
// A Volcengine access key (#1427) is kept the same way, the ID and Secret
// together.
func TestBackupBalanceToken(t *testing.T) {
	for _, op := range []struct {
		name string
		put  func([]Provider) error
	}{
		{"restore", func(ps []Provider) error { _, _, err := RestoreConfiguration(ps, nil, nil, nil, false); return err }},
		{"mirror", func(ps []Provider) error { return MirrorConfiguration(ps, nil, nil, nil, false) }},
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
						Keys: []KeyAccount{{ID: "main", Name: "main", Key: "sk-here", Protocol: Chat}, {ID: "spare", Name: "spare", Key: "sk-spare", Protocol: Anthropic}}, BalanceToken: "balance-here",
						AccessKeyID: "AK-here", SecretAccessKey: "SK-here"}
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
					wantKeys := p.Keys
					wantAK, wantSK := p.AccessKeyID, p.SecretAccessKey
					if len(wantKeys) == 0 {
						wantKeys = here.Keys
						wantAK, wantSK = here.AccessKeyID, here.SecretAccessKey
					}
					if got.AccessKeyID != wantAK || got.SecretAccessKey != wantSK {
						t.Fatalf("access key: %q %q, want %q %q", got.AccessKeyID, got.SecretAccessKey, wantAK, wantSK)
					}
					if len(got.Keys) != len(wantKeys) {
						t.Fatalf("keys: %+v, want %d keys", got, len(wantKeys))
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

// The provider-wide secrets (balance token, access key pair) belong to the
// account the selected primary credential signs in to. A spare key that both
// machines happen to hold does not make an incoming provider with another
// primary key the same account, so it must not inherit them (KeepSecrets'
// primary-key match); the same primary with a different spare does.
// A keyed restore or mirror holding no credential is an explicit clear; a
// keyless one retains this machine's.
func TestBackupSecretsFollowPrimaryCredential(t *testing.T) {
	type secrets struct{ token, ak, sk string }
	for _, op := range []struct {
		name string
		put  func(ps []Provider, keyed bool) error
	}{
		{"restore", func(ps []Provider, keyed bool) error {
			_, _, err := RestoreConfiguration(ps, nil, nil, nil, keyed)
			return err
		}},
		{"mirror", func(ps []Provider, keyed bool) error { return MirrorConfiguration(ps, nil, nil, nil, keyed) }},
	} {
		for _, c := range []struct {
			name     string
			incoming []KeyAccount
			keyed    bool
			want     secrets
		}{
			{"different primary, shared spare", []KeyAccount{{Key: "sk-new"}, {Key: "sk-spare"}}, true, secrets{}},
			{"different primary, shared spare, keyless flag", []KeyAccount{{Key: "sk-new"}, {Key: "sk-spare"}}, false, secrets{}},
			{"same primary, different spare", []KeyAccount{{Key: "sk-here"}, {Key: "sk-other"}}, true, secrets{"balance-here", "AK-here", "SK-here"}},
			{"spare of here is primary of incoming", []KeyAccount{{Key: "sk-spare"}}, true, secrets{}},
			{"keyed clear", nil, true, secrets{}},
			{"keyless retains", nil, false, secrets{"balance-here", "AK-here", "SK-here"}},
		} {
			t.Run(op.name+"/"+c.name, func(t *testing.T) {
				isolate(t)
				h := t.TempDir()
				t.Setenv("HOME", h)
				t.Setenv("USERPROFILE", h)
				t.Setenv("XDG_CONFIG_HOME", filepath.Join(h, ".config"))
				t.Setenv("XDG_CACHE_HOME", filepath.Join(h, ".cache"))
				t.Setenv("PATH", h)
				here := Provider{ID: "relay", Name: "Relay old", Chat: "https://old.example.com/v1",
					Keys:         []KeyAccount{{ID: "main", Key: "sk-here"}, {ID: "spare", Key: "sk-spare"}},
					BalanceToken: "balance-here", AccessKeyID: "AK-here", SecretAccessKey: "SK-here"}
				here.normalizeKeys()
				if err := Save(here); err != nil {
					t.Fatal(err)
				}
				p := Provider{ID: "relay", Name: "Relay new", Chat: "https://new.example.com/v1", Keys: slices.Clone(c.incoming)}
				p.normalizeKeys()
				if err := op.put([]Provider{p}, c.keyed); err != nil {
					t.Fatal(err)
				}
				ps, err := Stored()
				if err != nil || len(ps) != 1 {
					t.Fatalf("providers: %+v %v", ps, err)
				}
				got := ps[0]
				if (secrets{got.BalanceToken, got.AccessKeyID, got.SecretAccessKey}) != c.want {
					t.Fatalf("secrets: %q %q %q, want %+v", got.BalanceToken, got.AccessKeyID, got.SecretAccessKey, c.want)
				}
				wantKeys := len(c.incoming)
				if c.incoming == nil && !c.keyed {
					wantKeys = len(here.Keys)
				}
				if len(got.Keys) != wantKeys {
					t.Fatalf("keys: %+v, want %d", got.Keys, wantKeys)
				}
			})
		}
	}
}
