package davsync

import (
	"context"
	"fmt"
	"reflect"
	"slices"
	"testing"

	"github.com/yetone/magpie/internal/backup"
	"github.com/yetone/magpie/internal/provider"
)

// A computer sending no keys leaves the server's balance token in place,
// just as it leaves its API keys. Sending keys replaces the token too. A
// Volcengine access key (#1427) goes the same way, ID and Secret together.
func TestTakeBalanceToken(t *testing.T) {
	for _, c := range []struct {
		name                     string
		serverKeys, incomingKeys bool
		token, want              string
	}{
		{"keyless update", true, false, "", "balance-server"},
		{"explicit token", true, false, "balance-new", "balance-new"},
		{"with keys", true, true, "balance-new", "balance-new"},
		{"with keys without token", true, true, "", ""},
		{"keyless server", false, false, "", ""},
		{"adding keys", false, true, "balance-new", "balance-new"},
	} {
		t.Run(c.name, func(t *testing.T) {
			// take() operates on raw Bundle providers: it only backfills
			// empty KeyAccount.Key entries by matching IDs, never
			// synthesizing Provider.Key from Keys. Each side must carry a
			// populated Keys[{Key, ID}] when it has keys, or the merge
			// yields empty secrets and fails the assertions.
			server := provider.Provider{ID: "relay", Name: "Old", Chat: "https://old.example.com/v1"}
			if c.serverKeys {
				server.Key, server.BalanceToken = "sk-server", "balance-server"
				server.Keys = []provider.KeyAccount{{ID: "stable-server", Key: "sk-server"}}
				server.KeyID = "stable-server"
				server.AccessKeyID, server.SecretAccessKey = "AK-server", "SK-server"
			}
			incoming := provider.Provider{ID: "relay", Name: "New", Chat: "https://new.example.com/v1", BalanceToken: c.token}
			if c.incomingKeys {
				incoming.Key = "sk-new"
				incoming.Keys = []provider.KeyAccount{{ID: "stable-new", Key: "sk-new"}}
				incoming.KeyID = "stable-new"
				incoming.AccessKeyID, incoming.SecretAccessKey = "AK-new", "SK-new"
			}
			to := backup.Bundle{Keys: c.serverKeys, Providers: []provider.Provider{server}}
			from := backup.Bundle{Keys: c.incomingKeys, Providers: []provider.Provider{incoming}}
			take(&to, from, "providers")
			if len(to.Providers) != 1 {
				t.Fatalf("providers: %+v", to.Providers)
			}
			got := to.Providers[0]
			if got.BalanceToken != c.want || got.Name != incoming.Name || got.Chat != incoming.Chat {
				t.Fatalf("merged: %+v, want balance token %q and the incoming name and URL", got, c.want)
			}
			// The merge preserves whichever side's credential set the
			// scoped-keys flag selected.
			if c.serverKeys || c.incomingKeys {
				if len(got.Keys) != 1 || got.Keys[0].Key == "" {
					t.Fatalf("expected one key with secret to survive merge, got Keys=%v", got.Keys)
				}
			} else if len(got.Keys) != 0 {
				t.Fatalf("keyless merge should carry no keys, got Keys=%v", got.Keys)
			}
			wantAK, wantSK := incoming.AccessKeyID, incoming.SecretAccessKey
			if c.serverKeys && !c.incomingKeys {
				wantAK, wantSK = server.AccessKeyID, server.SecretAccessKey
			}
			if got.AccessKeyID != wantAK || got.SecretAccessKey != wantSK {
				t.Fatalf("access key: %q %q, want %q %q", got.AccessKeyID, got.SecretAccessKey, wantAK, wantSK)
			}
			if to.Keys != (c.serverKeys || c.incomingKeys) || (to.Keys || (to.ProvidersKeys != nil && *to.ProvidersKeys)) != (c.serverKeys || c.incomingKeys) {
				t.Fatalf("keys flag after merge: %+v", to)
			}
			if !reflect.DeepEqual(from.Providers, []provider.Provider{incoming}) {
				t.Fatalf("merge changed the incoming bundle: %+v", from.Providers)
			}
		})
	}
}

// Exercise the encrypted remote and two separate computers. Keyless
// uploads keep each computer's token, and any token already on the server.
func TestSyncBalanceToken(t *testing.T) {
	for _, serverKeys := range []bool{false, true} {
		t.Run(fmt.Sprintf("serverKeys=%v", serverKeys), func(t *testing.T) {
			f, srv := newFakeS3(t)
			cfg := f.config(srv)
			cfg.Keys, cfg.Agents = serverKeys, false
			a, b := newComputer(t), newComputer(t)
			use := func(c computer) {
				c.use(t)
				t.Setenv("USERPROFILE", string(c))
			}
			now := func() {
				t.Helper()
				if err := Now(context.Background()); err != nil {
					t.Fatal(err)
				}
			}
			remote := func() backup.Bundle {
				t.Helper()
				got, err := backup.Open(f.objects["team x+y/magpie/magpie.magpie-backup"], cfg.Passphrase)
				if err != nil {
					t.Fatal(err)
				}
				return got
			}

			// Computer a provisions relay -------------------------------
			use(a)
			if err := provider.Save(provider.Provider{ID: "relay", Name: "Relay", Chat: "https://relay.example.com/v1",
				Key: "sk-a", BalanceToken: "balance-a", AccessKeyID: "AK-a", SecretAccessKey: "SK-a"}); err != nil {
				t.Fatal(err)
			}
			if err := Configure(cfg); err != nil {
				t.Fatal(err)
			}
			now()

			// Provider.Key is runtime-only; Keys[i].Key carries inference secrets.
			wantToken, wantSecret := "", ""
			if serverKeys {
				wantToken, wantSecret = "balance-a", "SK-a"
			}
			if got := remote(); got.Keys != serverKeys || len(got.Providers) != 1 || got.Providers[0].BalanceToken != wantToken || got.Providers[0].SecretAccessKey != wantSecret {
				t.Fatalf("first upload: %+v", got)
			}
			r := remote().Providers[0]
			if serverKeys {
				if len(r.Keys) != 1 || r.Keys[0].Key != "sk-a" {
					t.Fatalf("first upload keys expected sk-a, got %+v", r)
				}
			} else {
				if len(r.Keys) != 1 || r.Keys[0].Key != "" {
					t.Fatalf("first upload should redact key, got %+v", r)
				}
			}

			// Computer b overwrites relay -------------------------------
			use(b)
			if err := provider.Save(provider.Provider{ID: "relay", Name: "Relay b", Chat: "https://relay.example.com/v1",
				Key: "sk-b", BalanceToken: "balance-b", AccessKeyID: "AK-b", SecretAccessKey: "SK-b"}); err != nil {
				t.Fatal(err)
			}
			keyless := cfg
			keyless.Keys = false
			if err := Configure(keyless); err != nil {
				t.Fatal(err)
			}
			now()

			localKey, localToken, localSecret := "sk-b", "balance-b", "SK-b"
			if serverKeys {
				localKey, localToken, localSecret = "sk-a", "balance-a", "SK-a"
			}
			ps, _ := provider.Stored()
			p, err := provider.Find("relay")
			if err != nil || len(ps) != 1 || p.Key != localKey || p.BalanceToken != localToken || p.SecretAccessKey != localSecret {
				t.Fatalf("b after download: raw=%+v normalized=%+v err=%v", ps, p, err)
			}

			p.Name, p.BalanceToken = "Renamed", "balance-b-new"
			if err := provider.Save(*p); err != nil {
				t.Fatal(err)
			}
			now()

			r = remote().Providers[0]
			if got := remote(); got.Keys != serverKeys || len(got.Providers) != 1 || got.Providers[0].Name != "Renamed" || got.Providers[0].BalanceToken != wantToken || got.Providers[0].SecretAccessKey != wantSecret {
				t.Fatalf("after b's keyless upload: %+v", got)
			}
			if serverKeys {
				if len(r.Keys) != 1 || r.Keys[0].Key != "sk-a" {
					t.Fatalf("after b's keyless upload should keep server sk-a, got %+v", r)
				}
			} else if r.Key != "" || slices.ContainsFunc(r.Keys, func(k provider.KeyAccount) bool { return k.Key != "" }) {
				t.Fatalf("after b's keyless upload should redact key, got %+v", r)
			}
			if p, err := provider.Find("relay"); err != nil || p.BalanceToken != "balance-b-new" {
				t.Fatalf("upload changed b's token: %+v %v", p, err)
			}

			// Computer a sees the rename -------------------------------
			use(a)
			now()
			if p, err := provider.Find("relay"); err != nil || p.Name != "Renamed" || p.Key != "sk-a" || p.BalanceToken != "balance-a" || p.AccessKeyID != "AK-a" || p.SecretAccessKey != "SK-a" {
				t.Fatalf("a after download: %+v %v", p, err)
			}
		})
	}
}
