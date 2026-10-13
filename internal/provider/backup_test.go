package provider

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/yetone/magpie/internal/plugin"
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

// A backup or a sync carries the plugins added from npm or git, and the
// computer it comes to installs the ones it hasn't: the providers they
// serve come with them (yapechan on Discord: after a WebDAV sync, no
// providers showed until each was added again by hand). A plugin from a
// folder stays on its own computer, one already here is left as the user
// has it, and a key in an option goes only with the keys.
func TestPluginsCarried(t *testing.T) {
	isolate(t)
	h := t.TempDir()
	t.Setenv("HOME", h)
	t.Setenv("USERPROFILE", h)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(h, ".config"))
	t.Setenv("XDG_CACHE_HOME", filepath.Join(h, ".cache"))
	t.Setenv("PATH", h)
	list := filepath.Join(filepath.Dir(plugin.Dir()), "plugins.json")
	write := func(body string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(list), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(list, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write(`{"plugins":[
		{"spec":"@magpie-community/opencode-cursor-auth@0.3.0"},
		{"spec":"/Users/me/my-plugin"},
		{"spec":"@magpie-community/middleware-model-map","off":true,
		 "options":{"map":{"a":"b"},"apiKey":"sk-option","nested":{"authToken":"t","keep":1}}}]}`)

	keyless := Carried(false)
	if len(keyless) != 2 || keyless[0].Spec != "@magpie-community/opencode-cursor-auth@0.3.0" {
		t.Fatalf("carried: %+v, want the npm plugins only", keyless)
	}
	opts := keyless[1].Options
	if !keyless[1].Off || opts["apiKey"] != nil || opts["nested"].(map[string]any)["authToken"] != nil ||
		opts["map"] == nil || opts["nested"].(map[string]any)["keep"] == nil {
		t.Fatalf("carried without keys: %+v, want it off, its options with no key in them", keyless[1])
	}
	withKeys := Carried(true)
	if withKeys[1].Options["apiKey"] != "sk-option" {
		t.Fatalf("carried with keys: %+v, want the option's key too", withKeys[1])
	}

	// the other computer: the model-map middleware is here already, as the
	// user set it; the cursor plugin is not
	write(`{"plugins":[{"spec":"@magpie-community/middleware-model-map@1.0.0","options":{"map":{"x":"y"}}}]}`)
	oldAdd, oldHand := addPlugin, handOver
	t.Cleanup(func() { addPlugin, handOver = oldAdd, oldHand })
	var asked []string
	addPlugin = func(_ context.Context, spec string) (plugin.Entry, error) {
		asked = append(asked, spec)
		l := plugin.Load()
		l.Plugins = append(l.Plugins, plugin.Entry{Spec: spec})
		b, _ := json.Marshal(l)
		write(string(b))
		return plugin.Entry{Spec: spec}, nil
	}
	handed := 0
	handOver = func(context.Context, bool) map[string]error { handed++; return nil }
	added, failed := AddPlugins(context.Background(), withKeys)
	if len(failed) > 0 || !slices.Equal(added, []string{"@magpie-community/opencode-cursor-auth@0.3.0"}) ||
		!slices.Equal(asked, added) {
		t.Fatalf("added %v (asked %v, failed %v), want only the cursor plugin", added, asked, failed)
	}
	if handed != 1 {
		t.Fatalf("handed over %d times, want once: the plugin serves a built-in", handed)
	}
	got := plugin.Load().Plugins
	if len(got) != 2 || got[0].Options["map"].(map[string]any)["x"] != "y" {
		t.Fatalf("plugins here: %+v, want the middleware as the user set it", got)
	}

	// one turned off there comes off, with its options
	write(`{"plugins":[]}`)
	asked = nil
	if _, failed := AddPlugins(context.Background(), keyless[1:]); len(failed) > 0 {
		t.Fatal(failed)
	}
	if got := plugin.Load().Plugins; len(got) != 1 || !got[0].Off || got[0].Options["map"] == nil {
		t.Fatalf("plugins here: %+v, want the middleware off with its options", got)
	}

	// nothing to add: nothing installed, nothing handed over
	asked, handed = nil, 0
	AddPlugins(context.Background(), keyless[1:])
	if len(asked) != 0 || handed != 0 {
		t.Fatalf("asked %v, handed over %d times, for plugins already here", asked, handed)
	}
}

// The subscriptions a backup or a sync brings the settings of, with no
// account here, are named: they show once signed in, and nothing else says
// so (yapechan on Discord).
func TestNotSignedIn(t *testing.T) {
	isolate(t)
	h := t.TempDir()
	t.Setenv("HOME", h)
	t.Setenv("USERPROFILE", h)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(h, ".config"))
	t.Setenv("XDG_CACHE_HOME", filepath.Join(h, ".cache"))
	t.Setenv("PATH", h)
	t.Setenv("CLAUDE_CONFIG_DIR", filepath.Join(h, ".claude"))
	t.Setenv("CODEX_HOME", filepath.Join(h, ".codex"))
	ps := []Provider{
		{ID: "claude", Models: []string{"claude-opus-4-8"}},
		{ID: "cursor", Models: []string{"auto"}},
		{ID: "codex", Hidden: true},
		{ID: "relay", Name: "Relay", Chat: "https://relay.example.invalid/v1", Key: "sk"},
		{ID: "claude", Models: []string{"claude-opus-4-8"}},
	}
	if got := NotSignedIn(ps); !slices.Equal(got, []string{"claude", "cursor"}) {
		t.Fatalf("not signed in: %v, want claude and cursor", got)
	}
}
