package gateway

import (
	"fmt"
	"testing"

	"github.com/yetone/magpie/internal/catalog"
	"github.com/yetone/magpie/internal/provider"
)

// @NetRozee on X: a custom endpoint given a thousand keys froze magpie.
// Every request asked, for each key, whether it serves the model, and
// each asking read the provider's lists again: a thousand reads of the
// lists' file a request. The reads are counted, not timed: a request
// reads them as often with a thousand keys as with ten.
func TestPerKeyReadsListsOnce(t *testing.T) {
	fresh(t)
	reads := func(n int) int64 {
		p := provider.Provider{ID: fmt.Sprintf("relay%d", n), Name: "Relay", Chat: "https://relay.example/v1", Key: "sk-first"}
		for i := range n {
			k := fmt.Sprintf("sk-key-%d-%04d", n, i)
			p.Keys = append(p.Keys, provider.KeyAccount{Key: k})
		}
		if err := provider.Save(p); err != nil {
			t.Fatal(err)
		}
		persisted, err := provider.Find(p.ID)
		if err != nil {
			t.Fatal(err)
		}
		p = *persisted
		var ids, odd []string
		for i, k := range p.KeysOn() {
			if i == 0 || i%2 == 1 {
				ids = append(ids, k.ID)
			} else {
				odd = append(odd, k.ID)
			}
		}
		if err := catalog.SaveLive(p.ID, p.Chat, []catalog.Model{{ID: "m", Keys: ids}, {ID: "other", Keys: odd}}); err != nil {
			t.Fatal(err)
		}
		q, err := provider.Find(p.ID)
		if err != nil {
			t.Fatal(err)
		}
		perKey(*q, "m", provider.Chat) // the lists' first read
		before := provider.ListsRead()
		cs := perKey(*q, "m", provider.Chat)
		// the keys listing m; the others list another model only
		if want := 1 + (n+1)/2; len(cs) != want {
			t.Errorf("%d keys: %d candidates, want %d", n, len(cs), want)
		}
		return provider.ListsRead() - before
	}
	few, many := reads(10), reads(1000)
	t.Logf("lists read %d times for 10 keys, %d for 1000", few, many)
	if many > few+2 {
		t.Errorf("a request read the lists %d times with 1000 keys, %d with 10: once a key", many, few)
	}
}
