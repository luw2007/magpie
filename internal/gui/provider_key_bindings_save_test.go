package gui

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/yetone/magpie/internal/provider"
	"github.com/yetone/magpie/internal/testenv"
)

// The editor's one Save must commit both keys' independent pool picks, without
// rewriting credentials or the other per-key settings. A bad second pick must
// reject the entire Save, including the first pick and the provider's name.
func TestProviderSaveKeyBindingsAreIndependentAndAtomic(t *testing.T) {
	testenv.SetHome(t, t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	if err := provider.SaveUsageSource(provider.UsageSource{ID: "source", Name: "Source", Type: "sub2api", BaseURL: "https://usage.example.invalid", Credential: "usage-secret"}); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"google", "google-2"} {
		if err := provider.SaveQuotaPool(provider.QuotaPool{ID: id, Name: id, SourceRef: "source", AccountIDs: []string{"account"}}); err != nil {
			t.Fatal(err)
		}
	}
	original := provider.Provider{
		ID: "relay", Name: "Relay", Chat: "http://127.0.0.1:1/v1", BalanceToken: "balance-secret",
		Keys: []provider.KeyAccount{
			{ID: "first", Name: "Personal", Key: "secret-one", Protocol: provider.Chat, Weight: 3, Models: []string{"old-one"}},
			{ID: "second", Name: "Shared", Key: "secret-two", Protocol: provider.Responses, Off: true, Weight: 7, Models: []string{"old-two"}},
		},
	}
	if err := provider.Save(original); err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	providerRoutes(mux, nil)
	post := func(body string) *httptest.ResponseRecorder {
		t.Helper()
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/api/provider/save", strings.NewReader(body)))
		return w
	}
	shown := func() map[string]provider.KeyInfo {
		t.Helper()
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/providers", nil))
		if w.Code != http.StatusOK {
			t.Fatalf("listing providers: %d %s", w.Code, w.Body)
		}
		var result struct {
			Providers []struct {
				ID      string             `json:"id"`
				KeyList []provider.KeyInfo `json:"keyList"`
			} `json:"providers"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
			t.Fatal(err)
		}
		for _, p := range result.Providers {
			if p.ID == original.ID {
				keys := make(map[string]provider.KeyInfo, len(p.KeyList))
				for _, k := range p.KeyList {
					keys[k.ID] = k
				}
				return keys
			}
		}
		t.Fatalf("relay absent from provider listing: %s", w.Body)
		return nil
	}
	const good = `{"id":"relay","from":"relay","name":"Relay","chat":"http://127.0.0.1:1/v1","keyBindings":{"first":{"poolRefs":["google"],"models":["model-one"]},"second":{"poolRefs":["google-2"],"models":["model-two"]}}}`
	if w := post(good); w.Code != http.StatusOK {
		t.Fatalf("saving independent bindings: %d %s", w.Code, w.Body)
	}
	keys := shown()
	for id, want := range map[string]provider.KeyBinding{
		"first":  {PoolRefs: []string{"google"}, Models: []string{"model-one"}},
		"second": {PoolRefs: []string{"google-2"}, Models: []string{"model-two"}},
	} {
		k, ok := keys[id]
		if !ok || !reflect.DeepEqual(k.PoolRefs, want.PoolRefs) || !reflect.DeepEqual(k.Models, want.Models) {
			t.Fatalf("editor reopened: key %s = %+v, want %+v", id, k, want)
		}
	}
	stored, err := provider.Find(original.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.BalanceToken != original.BalanceToken || len(stored.Keys) != 2 {
		t.Fatalf("save changed credentials or key count: %+v", stored)
	}
	for _, want := range original.Keys {
		var got *provider.KeyAccount
		for i := range stored.Keys {
			if stored.Keys[i].ID == want.ID {
				got = &stored.Keys[i]
				break
			}
		}
		if got == nil || got.Name != want.Name || got.Key != want.Key || got.Protocol != want.Protocol || got.Off != want.Off || got.Weight != want.Weight {
			t.Fatalf("binding save altered key %s metadata: %+v, want %+v", want.ID, got, want)
		}
	}
	before := *stored
	beforeShown := shown()
	const bad = `{"id":"relay","from":"relay","name":"Should Not Save","chat":"http://127.0.0.1:1/v1","keyBindings":{"first":{"poolRefs":["google-2"],"models":["changed"]},"second":{"poolRefs":["unknown-pool"],"models":["changed"]}}}`
	if w := post(bad); w.Code == http.StatusOK || !strings.Contains(w.Body.String(), "unknown quota pool") {
		t.Fatalf("unknown pool should reject whole Save: %d %s", w.Code, w.Body)
	}
	after, err := provider.Find(original.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(*after, before) || !reflect.DeepEqual(shown(), beforeShown) {
		t.Fatalf("rejected Save changed stored or visible provider: before %+v, after %+v", before, *after)
	}
}
