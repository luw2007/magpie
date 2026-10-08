package provider

import (
	"reflect"
	"strings"
	"testing"

	"github.com/yetone/magpie/internal/testenv"
)

func TestApplyKeyBindingsValidatesBeforeChangingKeys(t *testing.T) {
	testenv.SetHome(t, t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	if err := SaveUsageSource(UsageSource{ID: "source", Name: "Source", Type: "sub2api", BaseURL: "https://usage.example.invalid", Credential: "usage-secret"}); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"google", "google-2"} {
		if err := SaveQuotaPool(QuotaPool{ID: id, Name: id, SourceRef: "source", AccountIDs: []string{"account"}}); err != nil {
			t.Fatal(err)
		}
	}
	initial := Provider{ID: "relay", Name: "Relay", Keys: []KeyAccount{
		{ID: "first", Name: "Personal", Key: "secret-one", Off: true, Weight: 4, PoolRefs: []string{"google"}, Models: []string{"old-one"}},
		{ID: "second", Name: "Shared", Key: "secret-two", PoolRefs: []string{"google-2"}, Models: []string{"old-two"}},
		{ID: "untouched", Name: "Reserve", Key: "secret-three", Models: []string{"reserve"}},
	}}
	p := initial
	p.Keys = append([]KeyAccount(nil), initial.Keys...)
	if err := ApplyKeyBindings(&p, map[string]KeyBinding{
		"first":  {PoolRefs: []string{" google-2 "}, Models: []string{"new-one"}},
		"second": {PoolRefs: []string{"google"}, Models: []string{"new-two"}},
	}); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(p.Keys[0].PoolRefs, []string{"google-2"}) || !reflect.DeepEqual(p.Keys[0].Models, []string{"new-one"}) ||
		!reflect.DeepEqual(p.Keys[1].PoolRefs, []string{"google"}) || !reflect.DeepEqual(p.Keys[1].Models, []string{"new-two"}) ||
		!reflect.DeepEqual(p.Keys[2], initial.Keys[2]) || p.Keys[0].Key != initial.Keys[0].Key || !p.Keys[0].Off || p.Keys[0].Weight != 4 {
		t.Fatalf("independent bindings or other key fields changed: %+v", p.Keys)
	}
	for _, tc := range []struct {
		name, badRef string
		bad          KeyBinding
		message      string
	}{
		{"unknown pool", "second", KeyBinding{PoolRefs: []string{"unknown-pool"}}, "unknown quota pool"},
		{"duplicate pool", "second", KeyBinding{PoolRefs: []string{"google", " google "}}, "duplicate quota pool"},
		{"invalid model wildcard", "second", KeyBinding{PoolRefs: []string{"google"}, Models: []string{"model*"}}, "exact ID or *"},
		{"unknown key", "missing", KeyBinding{PoolRefs: []string{"google"}}, "no such key"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for trial := range 20 {
				candidate := initial
				candidate.Keys = append([]KeyAccount(nil), initial.Keys...)
				err := ApplyKeyBindings(&candidate, map[string]KeyBinding{
					"first":   {PoolRefs: []string{"google-2"}, Models: []string{"new-one"}},
					tc.badRef: tc.bad,
				})
				if err == nil || !strings.Contains(err.Error(), tc.message) {
					t.Fatalf("trial %d: rejected binding error = %v; want %s", trial, err, tc.message)
				}
				if !reflect.DeepEqual(candidate, initial) {
					t.Fatalf("trial %d: rejected binding partially changed provider: %+v", trial, candidate.Keys)
				}
			}
		})
	}
}
