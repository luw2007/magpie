package gateway

import (
	"reflect"
	"testing"

	"github.com/yetone/magpie/internal/provider"
	"github.com/yetone/magpie/internal/testenv"
)

func TestArrangedKeysAreGatewayCandidates(t *testing.T) {
	testenv.SetHome(t, t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	for _, mode := range []string{"", provider.Ordered, provider.Rotate, provider.LeastUsed} {
		p := provider.Provider{ID: "arrange-routing", Name: "Arrange", Chat: "https://example.invalid/v1", Keys: []provider.KeyAccount{{ID: "k-first", Key: "first"}, {ID: "k-second", Key: "second"}, {ID: "k-third", Key: "third"}, {ID: "k-off", Key: "off", Off: true}}, Routing: mode}
		if err := provider.Save(p); err != nil {
			t.Fatal(err)
		}
		order := []string{"k-third", "k-second", "k-first", "k-off"}
		if err := provider.SetAccountOrder(p.ID, order); err != nil {
			t.Fatal(err)
		}
		got, err := provider.Find(p.ID)
		if err != nil {
			t.Fatal(err)
		}
		var keys []string
		for _, c := range perKey(*got, "model", provider.Chat) {
			keys = append(keys, c.p.Key)
		}
		if !reflect.DeepEqual(keys, []string{"third", "second", "first"}) || got.KeyList()[0].ID != "k-"+keys[0] {
			t.Fatalf("mode=%q candidates=%v", mode, keys)
		}
	}
}
