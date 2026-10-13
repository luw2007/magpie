package provider

import (
	"slices"
	"testing"
)

// A key's value replaced in place (#1869) keeps its stable ID, name, place,
// protocol, weight, on or off, and all account bindings; only the secret changes.
func TestReplaceKeyKeepsItsPlace(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	if err := Save(Provider{ID: "relay", Name: "Relay", Chat: "https://relay.test/v1", Anthropic: "https://relay.test",
		Keys: []KeyAccount{
			{ID: "personal", Name: "Personal", Key: "sk-first", Weight: 2},
			{ID: "team", Name: "Team", Key: "sk-team-old", Protocol: Anthropic, Weight: 3, PoolRefs: []string{"shared"}, Models: []string{"m1"}},
			{ID: "spare", Name: "Spare", Key: "sk-spare", Off: true},
		},
		AccountModels:      map[string][]string{"team": {"m1"}},
		AccountProxies:     map[string]string{"team": "direct"},
		AccountConcurrency: map[string]int{"team": 4, "personal": 1},
	}); err != nil {
		t.Fatal(err)
	}
	was, now, err := ReplaceKey("relay", "team", "  sk-team-new\n")
	if err != nil || was != "team" || now != "team" {
		t.Fatalf("%q %q %v", was, now, err)
	}
	p, _ := Find("relay")
	want := []KeyAccount{
		{ID: "personal", Name: "Personal", Key: "sk-first", Weight: 2},
		{ID: "team", Name: "Team", Key: "sk-team-new", Protocol: Anthropic, Weight: 3, PoolRefs: []string{"shared"}, Models: []string{"m1"}},
		{ID: "spare", Name: "Spare", Key: "sk-spare", Off: true},
	}
	if p.Key != "sk-first" || p.KeyID != "personal" || !sameKeyAccounts(p.Keys, want) {
		t.Fatalf("keys %q %+v", p.Key, p.Keys)
	}
	if !slices.Equal(p.AccountModels["team"], []string{"m1"}) || len(p.AccountModels) != 1 {
		t.Fatalf("models %v", p.AccountModels)
	}
	if p.AccountProxies[now] != "direct" || p.AccountConcurrency[now] != 4 || p.AccountConcurrency["personal"] != 1 || len(p.AccountConcurrency) != 2 {
		t.Fatalf("proxies %v concurrency %v", p.AccountProxies, p.AccountConcurrency)
	}

	// the first key: stays first, keeps its name and weight
	if was, now, err := ReplaceKey("relay", "personal", "sk-first-2"); err != nil || was != "personal" || now != "personal" {
		t.Fatalf("primary identity: %q %q %v", was, now, err)
	}
	p, _ = Find("relay")
	want[0].Key = "sk-first-2"
	if p.Key != "sk-first-2" || p.KeyID != "personal" || !sameKeyAccounts(p.Keys, want) || p.AccountConcurrency["personal"] != 1 {
		t.Fatalf("first %q %+v %v", p.Key, p.Keys, p.AccountConcurrency)
	}

	// another key's value, nothing, or a key it doesn't have: refused,
	// nothing changed; its own value again: nothing to do
	for _, c := range []struct{ ref, key string }{
		{"spare", "sk-team-new"},
		{"spare", "  "},
		{"gone", "sk-x"},
	} {
		if _, _, err := ReplaceKey("relay", c.ref, c.key); err == nil {
			t.Errorf("%s → %q: no error", c.ref, c.key)
		}
	}
	if was, now, err := ReplaceKey("relay", "spare", "sk-spare"); err != nil || was != "spare" || now != "spare" {
		t.Fatalf("same value: %q %q %v", was, now, err)
	}
	if q, _ := Find("relay"); q.Key != p.Key || q.KeyID != p.KeyID || !sameKeyAccounts(q.Keys, p.Keys) {
		t.Fatalf("changed: %q %+v", q.Key, q.Keys)
	}
}

func sameKeyAccounts(a, b []KeyAccount) bool {
	return slices.EqualFunc(a, b, func(x, y KeyAccount) bool {
		return x.ID == y.ID && x.Name == y.Name && x.Key == y.Key && x.Protocol == y.Protocol && x.Off == y.Off && x.Weight == y.Weight && slices.Equal(x.PoolRefs, y.PoolRefs) && slices.Equal(x.Models, y.Models)
	})
}
