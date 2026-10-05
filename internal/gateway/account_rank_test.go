package gateway

import (
	"testing"

	"github.com/yetone/magpie/internal/provider"
	"github.com/yetone/magpie/internal/testenv"
)

// The trace tells each key's place in its provider's own list, as dragged,
// whatever order routing weighed them in, so the live routing view can
// seat them in it (#217).
func TestWeighedRankIsDraggedOrder(t *testing.T) {
	testenv.SetHome(t, t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	for _, mode := range []string{"", provider.Ordered, provider.Rotate, provider.LeastUsed} {
		p := provider.Provider{ID: "rank" + mode, Name: "Rank", Chat: "https://example.invalid/v1", Keys: []provider.KeyAccount{{ID: "k-first", Key: "first"}, {ID: "k-second", Key: "second"}, {ID: "k-third", Key: "third"}}, Routing: mode}
		if err := provider.Save(p); err != nil {
			t.Fatal(err)
		}
		if err := provider.SetAccountOrder(p.ID, []string{"k-third", "k-first", "k-second"}); err != nil {
			t.Fatal(err)
		}
		got, err := provider.Find(p.ID)
		if err != nil {
			t.Fatal(err)
		}
		_, pl := (&Server{}).plan(*got, "model", provider.Chat)
		want := map[string]int{"k-third": 0, "k-first": 1, "k-second": 2}
		if len(pl.order) != 3 {
			t.Fatalf("mode=%q order=%+v", mode, pl.order)
		}
		for _, w := range pl.order {
			id := w.ID[len(p.ID)+1:]
			if w.Rank != want[id] {
				t.Errorf("mode=%q %s rank=%d, want %d", mode, w.Who, w.Rank, want[id])
			}
		}
	}
}
