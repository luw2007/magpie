package provider

import (
	"encoding/json"
	"slices"
	"testing"
)

func TestLegacyKeysMigrateStableIdentity(t *testing.T) {
	raw := []byte(`{"id":"relay","name":"Relay","chat":"https://relay.test/v1","key":"first-secret","keyName":"Personal","keyProtocol":"chat","keys":[{"key":"second-secret","off":true}]}`)
	var a, b Provider
	if err := json.Unmarshal(raw, &a); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, &b); err != nil {
		t.Fatal(err)
	}
	if len(a.Keys) != 2 || a.Key != "first-secret" || a.KeyID != a.Keys[0].ID || a.Keys[0].ID != b.Keys[0].ID || a.Keys[1].ID != b.Keys[1].ID {
		t.Fatalf("migration identities: %+v %+v", a, b)
	}
	if a.SelectedKey().Name != "Personal" || a.SelectedKey().Protocol != Chat || !a.Keys[1].Off {
		t.Fatalf("lost legacy metadata: %+v", a.Keys)
	}
	encoded, err := json.Marshal(a)
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(encoded, &fields); err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"key", "keyName", "keyProtocol", "keyID"} {
		if _, ok := fields[field]; ok {
			t.Fatalf("legacy mirror %s persisted: %s", field, encoded)
		}
	}
	var c Provider
	if err := json.Unmarshal(encoded, &c); err != nil {
		t.Fatal(err)
	}
	if c.KeyID != a.KeyID || c.Keys[1].ID != a.Keys[1].ID {
		t.Fatal("identities changed after persistence")
	}
}

func TestKeyReplacementPreservesBindingAndIdentity(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	if err := SaveUsageSource(UsageSource{ID: "source", Name: "Source", Type: "sub2api", BaseURL: "https://usage.test", Credential: "test"}); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"personal-pool", "shared-pool"} {
		if err := SaveQuotaPool(QuotaPool{ID: id, Name: id, SourceRef: "source", AccountIDs: []string{"1"}}); err != nil {
			t.Fatal(err)
		}
	}
	if err := Save(Provider{ID: "relay", Name: "Relay", Chat: "https://relay.test/v1", Keys: []KeyAccount{{ID: "personal", Key: "old-secret", Name: "Personal", Protocol: Chat, Models: []string{"actual"}}, {ID: "team", Key: "team-secret"}}}); err != nil {
		t.Fatal(err)
	}
	if err := SetKeyBinding("relay", "personal", []string{"personal-pool", "shared-pool"}, []string{"actual"}); err != nil {
		t.Fatal(err)
	}
	if err := SetKeyBinding("relay", "team", []string{"shared-pool"}, nil); err != nil {
		t.Fatal(err)
	}
	if err := ReplaceKey("relay", "personal", "new-secret"); err != nil {
		t.Fatal(err)
	}
	if err := UseKey("relay", "team"); err != nil {
		t.Fatal(err)
	}
	if err := SetKeyOn("relay", "personal", false); err != nil {
		t.Fatal(err)
	}
	if err := SetKeyProtocol("relay", "personal", Responses); err != nil {
		t.Fatal(err)
	}
	p, err := Find("relay")
	if err != nil {
		t.Fatal(err)
	}
	k := p.Keys[1]
	if k.ID != "personal" || k.Key != "new-secret" || k.Name != "Personal" || !k.Off || k.Protocol != Responses || !slices.Equal(k.Models, []string{"actual"}) || !slices.Equal(k.PoolRefs, []string{"personal-pool", "shared-pool"}) {
		t.Fatalf("replacement lost metadata: %+v", k)
	}
	if p.KeyID != "team" {
		t.Fatalf("wrong selection: %s", p.KeyID)
	}
	for _, refs := range [][]string{{"missing"}, {"personal-pool", "missing"}, {""}, {" "}, {"personal-pool", "personal-pool"}, {"personal-pool", " personal-pool "}} {
		if err := SetKeyBinding("relay", "personal", refs, nil); err == nil {
			t.Fatalf("accepted invalid pools %q", refs)
		}
	}
	if err := SetKeyBinding("relay", "personal", nil, []string{"actual*"}); err == nil {
		t.Fatal("accepted wildcard pattern")
	}
	p, err = Find("relay")
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(p.Keys[1].PoolRefs, []string{"personal-pool", "shared-pool"}) || !slices.Equal(p.Keys[1].Models, []string{"actual"}) {
		t.Fatalf("failed validation mutated binding: %+v", p.Keys[1])
	}
	for _, pool := range []string{"personal-pool", "shared-pool"} {
		if err := DeleteQuotaPool(pool); err == nil {
			t.Fatalf("deleted referenced pool %s", pool)
		}
	}
	if err := SetKeyBinding("relay", "personal", nil, []string{"*"}); err != nil {
		t.Fatal(err)
	}
	p, err = Find("relay")
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Keys[1].PoolRefs) != 0 || !p.Keys[1].AllowsModel("other") || !slices.Equal(p.Keys[0].PoolRefs, []string{"shared-pool"}) {
		t.Fatalf("clearing affected other bindings: %+v", p.Keys)
	}
	if err := DeleteQuotaPool("personal-pool"); err != nil {
		t.Fatal(err)
	}
	if err := DeleteQuotaPool("shared-pool"); err == nil {
		t.Fatal("deleted pool still referenced by another key")
	}
	if err := SetKeyBinding("relay", "personal", []string{" shared-pool "}, []string{"actual"}); err != nil {
		t.Fatal(err)
	}
	p, err = Find("relay")
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(p.Keys[1].PoolRefs, []string{"shared-pool"}) || p.Keys[1].Key != "new-secret" || p.KeyID != "team" {
		t.Fatalf("rebinding changed identity or order: %+v", p)
	}
}
