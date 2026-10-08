package provider

import (
	"path/filepath"
	"slices"
	"testing"
)

// Removing many keys at once (361 on Discord: hundreds of keys, some dead):
// those named by persistent ID go in one save, and removing every key in
// use is refused, leaving them all.
func TestRemoveKeys(t *testing.T) {
	h := t.TempDir()
	t.Setenv("HOME", h)
	t.Setenv("USERPROFILE", h)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(h, ".config"))
	t.Setenv("XDG_CACHE_HOME", filepath.Join(h, ".cache"))
	if err := Save(Provider{ID: "many", Name: "Many", Chat: "https://example.invalid/v1",
		Keys: []KeyAccount{{ID: "id1", Key: "k1", Name: "first"}, {ID: "id2", Key: "k2", Off: true}, {ID: "id3", Key: "k3"}, {ID: "id4", Key: "k4", Name: "four"}, {ID: "id5", Key: "k5", Off: true}}}); err != nil {
		t.Fatal(err)
	}
	keys := func() (out []string) {
		p, _ := Find("many")
		for _, k := range p.KeyList() {
			out = append(out, k.ID)
		}
		return out
	}
	// Remove an active and an off key, preserving the remaining list.
	n, err := RemoveKeys("many", []string{"id1", "id2", "nosuchkey"})
	if err != nil || n != 2 {
		t.Fatalf("removed %d: %v", n, err)
	}
	p, _ := Find("many")
	if len(p.Keys) != 3 || p.Keys[0].ID != "id3" || p.Keys[0].Key != "k3" || p.Keys[1].ID != "id4" || p.Keys[1].Name != "four" || p.Keys[2].ID != "id5" || !p.Keys[2].Off {
		t.Fatalf("left %+v", p.Keys)
	}
	// every key in use: refused, nothing removed
	before := keys()
	if _, err := RemoveKeys("many", []string{"id3", "id4"}); err == nil {
		t.Fatal("removed every key in use")
	}
	if after := keys(); !slices.Equal(after, before) {
		t.Fatalf("a refused removal removed: %v → %v", before, after)
	}
	// none it has
	if _, err := RemoveKeys("many", []string{"nosuchkey"}); err == nil {
		t.Fatal("no key removed: no error")
	}
	// Removing an off key leaves both active keys unchanged.
	if n, err := RemoveKeys("many", []string{"id5"}); err != nil || n != 1 {
		t.Fatalf("removed %d: %v", n, err)
	}
	if p, _ := Find("many"); len(p.Keys) != 2 || p.Keys[0].ID != "id3" || p.Keys[1].ID != "id4" {
		t.Fatalf("left %+v", p.Keys)
	}
}
