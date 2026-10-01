package provider

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

func TestUsageSourcesPreserveAccountsAndSharedPools(t *testing.T) {
	lastQuotas.Lock()
	lastQuotas.m, lastQuotas.loaded = nil, false
	lastQuotas.Unlock()
	t.Cleanup(func() { lastQuotas.Lock(); lastQuotas.m, lastQuotas.loaded = nil, false; lastQuotas.Unlock() })
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	var usageCalls atomic.Int32
	reset := time.Now().Add(24 * time.Hour)
	server := func(used float64) *httptest.Server {
		return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			var data any
			switch r.URL.Path {
			case "/api/v1/admin/accounts":
				if r.URL.Query().Get("page") == "1" {
					data = sub2APIAccounts{Total: 3, Items: []sub2APIAccount{{ID: 1, Name: "same name", Platform: "openai", Type: "oauth"}}}
				} else {
					data = sub2APIAccounts{Total: 3, Items: []sub2APIAccount{{ID: 2, Name: "same name", Platform: "anthropic", Type: "oauth"}, {ID: 3, Type: "apikey"}}}
				}
			case "/api/v1/admin/accounts/1/usage", "/api/v1/admin/accounts/2/usage":
				usageCalls.Add(1)
				data = sub2APIUsage{FiveHour: &sub2APIWindow{Utilization: used, ResetsAt: reset}, SevenDay: &sub2APIWindow{Utilization: used + 1, ResetsAt: reset}}
			default:
				http.NotFound(w, r)
				return
			}
			json.NewEncoder(w).Encode(map[string]any{"code": 0, "data": data})
		}))
	}
	a, b := server(20), server(70)
	defer a.Close()
	defer b.Close()
	sources := []UsageSource{{ID: "a", Name: "A", Type: "sub2api", BaseURL: a.URL, Credential: "test"}, {ID: "b", Name: "B", Type: "sub2api", BaseURL: b.URL, Credential: "test"}}
	accounts, err := discoverSourceAccounts(context.Background(), sources[0], "test")
	if err != nil || len(accounts) != 2 || accounts[0].ID != "1" || accounts[1].ID != "2" || accounts[1].Platform != "anthropic" {
		t.Fatalf("accounts %+v: %v", accounts, err)
	}
	pools := []QuotaPool{{ID: "shared", Name: "Shared", SourceRef: "a", AccountIDs: []string{"1", "2", "1"}}, {ID: "also", Name: "Also", SourceRef: "a", AccountIDs: []string{"1"}}, {ID: "other", Name: "Other", SourceRef: "b", AccountIDs: []string{"1"}}}
	providers := []Provider{{ID: "relay", Keys: []KeyAccount{{ID: "one", PoolRefs: []string{"shared", "also", "other"}}, {ID: "two", PoolRefs: []string{"shared", "also"}}}}}
	got := collectUsageSources(context.Background(), sources, pools, providers)
	if usageCalls.Load() != 3 {
		t.Fatalf("shared accounts fetched %d times", usageCalls.Load())
	}
	if len(got) != 4 {
		t.Fatalf("rows %+v", got)
	}
	for _, q := range got {
		if q.Status != "measured" || q.AsOf == nil || len(q.Windows) != 2 || q.Windows[0].Span != 5*time.Hour || q.Windows[1].Span != 7*24*time.Hour {
			t.Fatalf("windows %+v", q)
		}
		want := float64(20)
		if q.SourceRef == "b" {
			want = 70
		}
		if q.Windows[0].Used != want {
			t.Fatalf("source identity mixed: %+v", q)
		}
		if q.PoolRef == "shared" && (len(q.KeyRefs) != 2 || q.KeyRefs[0] != "relay/one" || q.KeyRefs[1] != "relay/two") {
			t.Fatalf("key references %+v", q)
		}
		if q.PoolRef == "also" && (len(q.KeyRefs) != 2 || q.KeyRefs[0] != "relay/one" || q.KeyRefs[1] != "relay/two") {
			t.Fatalf("multi-pool key references %+v", q)
		}
		if q.PoolRef == "other" && (len(q.KeyRefs) != 1 || q.KeyRefs[0] != "relay/one") {
			t.Fatalf("cross-source key references %+v", q)
		}
	}
}

func TestUsageSourceFailuresNeverBecomeZero(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	lastQuotas.Lock()
	lastQuotas.m, lastQuotas.loaded = nil, false
	lastQuotas.Unlock()
	t.Cleanup(func() { lastQuotas.Lock(); lastQuotas.m, lastQuotas.loaded = nil, false; lastQuotas.Unlock() })
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "unavailable", http.StatusServiceUnavailable)
	}))
	defer server.Close()
	got := collectUsageSources(context.Background(), []UsageSource{{ID: "failed", Type: "sub2api", BaseURL: server.URL, Credential: "test"}}, []QuotaPool{{ID: "failed-pool", SourceRef: "failed", AccountIDs: []string{"1", "2"}}}, nil)
	if len(got) != 2 {
		t.Fatalf("failure rows %+v", got)
	}
	for _, q := range got {
		if q.Status != "error" || q.Error == "" || len(q.Windows) != 0 {
			t.Fatalf("failure became known: %+v", q)
		}
	}
}

func TestSub2APIMissingMeasurementIsUnknown(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"code":0,"data":{"five_hour":{"resets_at":"2099-01-01T00:00:00Z"}}}`))
	}))
	defer server.Close()
	q := sourceAccountQuota(context.Background(), UsageSource{ID: "missing", Type: "sub2api", BaseURL: server.URL}, "test", SourceAccount{ID: "1"})
	if q.Status != "unknown" || len(q.Windows) != 0 {
		t.Fatalf("omitted usage became zero: %+v", q)
	}
}

func TestSub2APIRejectsInvalidAccountBeforeRequest(t *testing.T) {
	requested := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requested = true
		w.Write([]byte(`{"code":0,"data":{}}`))
	}))
	defer server.Close()
	for _, id := range []string{"account-1", "0", "-1", "../other", "1?query=1", "18446744073709551616"} {
		q := sourceAccountQuota(context.Background(), UsageSource{ID: "invalid", Type: "sub2api", BaseURL: server.URL}, "test", SourceAccount{ID: id})
		if q.Status != "error" || q.Error == "" || len(q.Windows) != 0 {
			t.Fatalf("invalid account %q became measurable: %+v", id, q)
		}
	}
	if requested {
		t.Fatal("invalid account ID reached upstream")
	}
}

func TestMalformedUsageConfigurationReportsError(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	if err := os.MkdirAll(filepath.Dir(Path()), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(Path(), []byte(`{"sources":`), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := DiscoverUsageSource(context.Background(), "missing"); err == nil {
		t.Fatal("malformed configuration silently became empty discovery")
	}
	qs := collectUsageSources(context.Background(), nil, nil, nil)
	if len(qs) != 1 || qs[0].Status != "error" || qs[0].Error == "" || len(qs[0].Windows) != 0 {
		t.Fatalf("malformed config quotas %+v", qs)
	}
	if _, ok := PoolAllowance("missing"); ok {
		t.Fatal("malformed configuration became known allowance")
	}
}

func TestKeyPoolAllowanceCombinesPoolsConservatively(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("HOME", t.TempDir())
	t.Setenv("USERPROFILE", t.TempDir())
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "unavailable", http.StatusServiceUnavailable)
	}))
	defer server.Close()
	if err := SaveUsageSource(UsageSource{ID: "combined-source", Name: "Combined source", Type: "sub2api", BaseURL: server.URL, Credential: "test"}); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"first", "second"} {
		if err := SaveQuotaPool(QuotaPool{ID: id, Name: id, SourceRef: "combined-source", AccountIDs: []string{"1"}}); err != nil {
			t.Fatal(err)
		}
	}
	defer InvalidateUsageConfiguration()
	reset := time.Now().Add(time.Hour)
	row := func(pool string, used float64) SubscriptionQuota {
		return SubscriptionQuota{PoolRef: pool, SourceRef: "combined-source", AccountID: "1", Status: "measured", Windows: []QuotaWindow{{Name: "5 hours", Used: used, ResetsAt: &reset, Span: 5 * time.Hour}}}
	}
	set := func(rows ...SubscriptionQuota) {
		subscriptionUsageCache.Lock()
		subscriptionUsageCache.at, subscriptionUsageCache.data = time.Now(), rows
		subscriptionUsageCache.Unlock()
	}
	set(row("first", 25), row("second", 85))
	for _, refs := range [][]string{{"first", "second"}, {"second", "first"}} {
		a, ok := KeyPoolAllowance(refs)
		used, _ := a.For("any-model", time.Now())
		if !ok || used != 85 {
			t.Fatalf("combined usage %v known=%v for %v", used, ok, refs)
		}
	}
	set(row("first", 25))
	if _, ok := KeyPoolAllowance([]string{"first", "second"}); ok {
		t.Fatal("missing pool treated as zero")
	}
	bad := row("second", 85)
	bad.Status, bad.Error = "error", "unavailable"
	set(row("first", 25), bad)
	if _, ok := KeyPoolAllowance([]string{"first", "second"}); ok {
		t.Fatal("failed pool treated as zero")
	}
	if _, ok := KeyPoolAllowance([]string{"first", "unconfigured"}); ok {
		t.Fatal("unconfigured pool treated as zero")
	}
	if _, ok := KeyPoolAllowance(nil); ok {
		t.Fatal("unbound key treated as known")
	}
}
