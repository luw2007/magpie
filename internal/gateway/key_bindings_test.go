package gateway

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/yetone/magpie/internal/provider"
)

func TestKeyBindingFiltersResolvedModelBeforeRouting(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	p := provider.Provider{ID: "relay", Name: "Relay", Chat: "https://relay.test/v1", Models: []string{"actual"}, Keys: []provider.KeyAccount{{ID: "wrong", Key: "wrong-secret", Models: []string{"relay/actual"}}, {ID: "right", Key: "right-secret", Models: []string{"actual"}}, {ID: "disabled", Key: "disabled-secret", Off: true, Models: []string{"*"}}}}
	if err := provider.Save(p); err != nil {
		t.Fatal(err)
	}
	resolved, model, ok := provider.Resolve("relay/actual")
	if !ok || model != "actual" {
		t.Fatalf("resolve: %s %v", model, ok)
	}
	got := perKey(resolved, model, provider.Chat)
	if len(got) != 1 || got[0].p.KeyID != "right" {
		t.Fatalf("bound candidates: %+v", got)
	}
	if got := perKey(resolved, "not-allowed", provider.Chat); len(got) != 0 {
		t.Fatalf("disallowed model entered fallback: %+v", got)
	}
}

func TestBoundKeysUsePoolQuotaAndKeepMissingUnknown(t *testing.T) {
	old := keyPoolAllowance
	t.Cleanup(func() { keyPoolAllowance = old })
	now := time.Now()
	keyPoolAllowance = func(refs []string) (provider.Allowance, bool) {
		var all provider.Allowance
		for _, ref := range refs {
			switch ref {
			case "spent":
				all = append(all, provider.Limit{Used: 99, Resets: now.Add(time.Hour), Span: time.Hour})
			case "low":
				// Smart routing reserves low-quota keys at 90%, not 85%;
				// healthy keys with equal resets keep their cache-warm order.
				all = append(all, provider.Limit{Used: 95, Resets: now.Add(time.Hour), Span: time.Hour})
			case "fresh":
				all = append(all, provider.Limit{Used: 10, Resets: now.Add(time.Hour), Span: time.Hour})
			default:
				return nil, false
			}
		}
		return all, len(all) > 0
	}
	p := provider.Provider{ID: "relay", Chat: "https://relay.test/v1", Keys: []provider.KeyAccount{{ID: "unknown", Key: "a", PoolRefs: []string{"fresh", "missing"}}, {ID: "spent", Key: "b", PoolRefs: []string{"fresh", "spent"}}, {ID: "low", Key: "c", PoolRefs: []string{"fresh", "low"}}, {ID: "fresh", Key: "d", PoolRefs: []string{"fresh"}}}}
	for _, routing := range []string{"", provider.LeastUsed} {
		p.Routing = routing
		got, wg := weigh(p, perKey(p, "actual", provider.Chat), "actual", provider.Chat)
		want := []string{"fresh", "low", "spent", "unknown"}
		for i, id := range want {
			if got[i].p.KeyID != id {
				t.Fatalf("routing %s position %d: %s, want %s", routing, i, got[i].p.KeyID, id)
			}
		}
		if _, ok := wg.lefts[got[3].allowanceKey()]; ok {
			t.Fatal("missing quota marked as zero usage")
		}
		if wg.lefts[got[1].allowanceKey()].used != 95 || wg.lefts[got[2].allowanceKey()].used != 99 {
			t.Fatalf("routing ignored more-used pool: %+v", wg.lefts)
		}
		if full := got[2].full(now); !full.Equal(now.Add(time.Hour)) {
			t.Fatalf("spent secondary pool did not delay key: %v", full)
		}
		if full := got[3].full(now); !full.IsZero() {
			t.Fatalf("partially unknown key invented renewal: %v", full)
		}
	}
}

func TestHealthyBoundKeyPrecedesPartiallyUnknownKey(t *testing.T) {
	old := keyPoolAllowance
	t.Cleanup(func() { keyPoolAllowance = old })
	now := time.Now()
	keyPoolAllowance = func(refs []string) (provider.Allowance, bool) {
		var all provider.Allowance
		for _, ref := range refs {
			var used float64
			switch ref {
			case "fresh-a":
				used = 10
			case "fresh-b":
				used = 20
			default:
				return nil, false
			}
			all = append(all, provider.Limit{Used: used, Resets: now.Add(time.Hour), Span: time.Hour})
		}
		return all, len(all) > 0
	}
	// Put B first so preserving input order or treating missing usage as 0%
	// cannot accidentally satisfy the ranking assertion.
	p := provider.Provider{ID: "relay", Chat: "https://relay.test/v1", Keys: []provider.KeyAccount{
		{ID: "b", Key: "b-secret", PoolRefs: []string{"fresh-b", "unknown"}},
		{ID: "a", Key: "a-secret", PoolRefs: []string{"fresh-a"}},
	}}
	for _, routing := range []string{"", provider.LeastUsed} {
		p.Routing = routing
		got, wg := weigh(p, perKey(p, "actual", provider.Chat), "actual", provider.Chat)
		if len(got) != 2 || got[0].p.KeyID != "a" || got[1].p.KeyID != "b" {
			t.Fatalf("routing %s: healthy A must precede partially unknown B: %+v", routing, got)
		}
		if used := wg.lefts[got[0].allowanceKey()].used; used != 10 {
			t.Fatalf("routing %s: healthy A usage = %v, want 10", routing, used)
		}
		if _, known := wg.lefts[got[1].allowanceKey()]; known {
			t.Fatalf("routing %s: B's unknown pool became known aggregate usage", routing)
		}
		if full := got[1].full(now); !full.IsZero() {
			t.Fatalf("routing %s: unknown B was marked spent until %v", routing, full)
		}
	}
}

func TestImageBindingUsesEligibleKeyAndRejectsNoEligibleKey(t *testing.T) {
	fresh(t)
	var auth []string
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth = append(auth, r.Header.Get("Authorization"))
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"data":[{"b64_json":"aW1hZ2U="}]}`))
	}))
	t.Cleanup(up.Close)
	p := provider.Provider{ID: "art", Name: "Art", Chat: up.URL + "/v1", Keys: []provider.KeyAccount{{ID: "wrong", Key: "wrong-secret", Models: []string{"other"}}, {ID: "right", Key: "right-secret", Models: []string{"gpt-image-1"}}}}
	if err := provider.Save(p); err != nil {
		t.Fatal(err)
	}
	s := New()
	code, _, raw := postImages(t, s, "/v1/images/generations", "application/json", `{"model":"art/gpt-image-1","prompt":"bird"}`)
	if code != 200 || len(auth) != 1 || auth[0] != "Bearer right-secret" {
		t.Fatalf("image selection %d %s auth=%v", code, raw, auth)
	}
	code, _, raw = postImages(t, s, "/v1/images/generations", "application/json", `{"model":"art/blocked-image","prompt":"bird"}`)
	if code != 404 || len(auth) != 1 {
		t.Fatalf("ineligible key sent outbound %d %s auth=%v", code, raw, auth)
	}
}
