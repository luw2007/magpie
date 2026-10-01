package provider

import "testing"

func TestSeveralKeys(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	if err := Save(Provider{ID: "relay", Name: "Relay", Chat: "https://relay.test/v1", Keys: []KeyAccount{{ID: "personal", Key: "sk-personal-1111"}}}); err != nil {
		t.Fatal(err)
	}
	if err := AddKey("relay", "Team", "sk-team-2222", ""); err != nil {
		t.Fatal(err)
	}
	if err := AddKey("relay", "", "sk-team-2222", ""); err == nil {
		t.Fatal("the same key was added twice")
	}
	p, _ := Find("relay")
	ks := p.KeyList()
	if p.Key != "sk-personal-1111" || len(ks) != 2 || !ks[0].Active || ks[1].Name != "Team" {
		t.Fatalf("keys %+v", ks)
	}
	if !ks[1].On || len(p.KeysOn()) != 2 {
		t.Fatalf("a new key is on: %+v", ks)
	}
	if err := RenameKey("relay", ks[0].ID, "Personal"); err != nil {
		t.Fatal(err)
	}
	// first in line, the other stays on behind it
	if err := UseKey("relay", ks[1].ID); err != nil {
		t.Fatal(err)
	}
	p, _ = Find("relay")
	if p.KeyID != ks[1].ID || len(p.Keys) != 2 || p.Keys[0].Name != "Team" || p.Keys[1].ID != "personal" || p.Keys[1].Name != "Personal" {
		t.Fatalf("after use: %+v", p)
	}
	// off: the next one on takes the lead; the last one on stays
	if err := SetKeyOn("relay", ks[1].ID, false); err != nil {
		t.Fatal(err)
	}
	p, _ = Find("relay")
	if p.Key != "sk-personal-1111" || len(p.KeysOn()) != 1 || !p.Keys[0].Off || p.Keys[0].Name != "Team" {
		t.Fatalf("after off: %+v", p)
	}
	if err := SetKeyOn("relay", "personal", false); err == nil {
		t.Fatal("turned off the last key in use")
	}
	if err := RemoveKey("relay", "personal"); err == nil {
		t.Fatal("removed the last key in use")
	}
	if err := SetKeyOn("relay", ks[1].ID, true); err != nil {
		t.Fatal(err)
	}
	if err := RemoveKey("relay", "personal"); err != nil {
		t.Fatal(err)
	}
	if p, _ = Find("relay"); len(p.Keys) != 1 || p.KeyID != ks[1].ID || p.SelectedKey().Name != "Team" {
		t.Fatalf("after remove: %+v", p)
	}
}

func TestKeyProtocol(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	if err := Save(Provider{ID: "relay", Name: "Relay", Chat: "https://relay.test/v1", Anthropic: "https://relay.test", Keys: []KeyAccount{{ID: "chat", Key: "sk-oai-1111"}}}); err != nil {
		t.Fatal(err)
	}
	if err := AddKey("relay", "Claude", "sk-ant-2222", Anthropic); err != nil {
		t.Fatal(err)
	}
	if err := AddKey("relay", "", "sk-x-3333", "carrier-pigeon"); err == nil {
		t.Fatal("took an unknown protocol")
	}
	if err := SetKeyProtocol("relay", "chat", Chat); err != nil {
		t.Fatal(err)
	}
	p, _ := Find("relay")
	ks := p.KeyList()
	if ks[0].Protocol != Chat || ks[1].Protocol != Anthropic {
		t.Fatalf("keys %+v", ks)
	}
	// it goes with the key when the order changes
	if err := UseKey("relay", ks[1].ID); err != nil {
		t.Fatal(err)
	}
	p, _ = Find("relay")
	if p.KeyID != ks[1].ID || len(p.Keys) != 2 || p.SelectedKey().Protocol != Anthropic || p.Keys[1].Protocol != Chat {
		t.Fatalf("after use: %+v", p)
	}
	q := p.WithKey(p.Keys[1])
	if q.Key != "sk-oai-1111" || q.Chat == "" || q.Anthropic != "" || q.Responses != "" {
		t.Fatalf("with the OpenAI key: %+v", q)
	}
	if q := p.WithKey(KeyAccount{Key: "k"}); q.Chat == "" || q.Anthropic == "" {
		t.Fatalf("a key for any protocol: %+v", q)
	}
}
