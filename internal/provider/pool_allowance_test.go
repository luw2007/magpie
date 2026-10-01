package provider

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestPoolAllowanceRequiresEverySelectedAccount(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("HOME", t.TempDir())
	t.Setenv("USERPROFILE", t.TempDir())
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "unavailable", http.StatusServiceUnavailable)
	}))
	defer server.Close()
	if err := SaveUsageSource(UsageSource{ID: "pool-source", Name: "Source", Type: "sub2api", BaseURL: server.URL, Credential: "test"}); err != nil {
		t.Fatal(err)
	}
	if err := SaveQuotaPool(QuotaPool{ID: "pool", Name: "Pool", SourceRef: "pool-source", AccountIDs: []string{"1", "2"}}); err != nil {
		t.Fatal(err)
	}
	defer InvalidateUsageConfiguration()
	reset := time.Now().Add(time.Hour)
	asOf := time.Now().Add(-5 * time.Second)
	row := func(id string, used float64) SubscriptionQuota {
		return SubscriptionQuota{PoolRef: "pool", SourceRef: "pool-source", AccountID: id, Status: "measured", AsOf: &asOf, Windows: []QuotaWindow{{Name: "5 hours", Used: used, ResetsAt: &reset, Span: 5 * time.Hour}}}
	}
	set := func(rows ...SubscriptionQuota) {
		subscriptionUsageCache.Lock()
		subscriptionUsageCache.at, subscriptionUsageCache.data = time.Now(), rows
		subscriptionUsageCache.Unlock()
	}
	set(row("1", 10))
	if _, ok := PoolAllowance("pool"); ok {
		t.Fatal("missing selected account treated as known")
	}
	set(row("1", 10), row("2", 80))
	a, ok := PoolAllowance("pool")
	used, _ := a.For("any-model", time.Now())
	if !ok || used != 80 {
		t.Fatalf("pool usage %v known=%v", used, ok)
	}
	reportStatus := func(want string) {
		t.Helper()
		for _, q := range QuotaReport(context.Background(), time.Now()) {
			if q.PoolRef == "pool" && q.AccountID == "2" {
				if q.Status != want || q.AsOf == nil || q.Windows[0].Used != 80 {
					t.Fatalf("quota report status want=%s: %+v", want, q)
				}
				return
			}
		}
		t.Fatal("pool account absent from quota report")
	}
	reportStatus("measured")
	bad := row("2", 80)
	bad.Status, bad.Error = "error", "unavailable"
	set(row("1", 10), bad)
	if _, ok := PoolAllowance("pool"); ok {
		t.Fatal("failed account treated as zero")
	}
	bad = row("2", 80)
	bad.Status = "stale"
	set(row("1", 10), bad)
	if _, ok := PoolAllowance("pool"); ok {
		t.Fatal("stale account treated as known")
	}
	old := time.Now().Add(-2 * time.Minute)
	bad = row("2", 80)
	bad.AsOf = &old
	set(row("1", 10), bad)
	if _, ok := PoolAllowance("pool"); ok {
		t.Fatal("old measurement with future reset treated as fresh")
	}
	reportStatus("stale")
	lastQuotas.Lock()
	lastQuotas.m, lastQuotas.loaded = nil, false
	lastQuotas.Unlock()
	t.Cleanup(func() { lastQuotas.Lock(); lastQuotas.m, lastQuotas.loaded = nil, false; lastQuotas.Unlock() })
	keepLast(row("2", 80), "")
	bad = keepLast(SubscriptionQuota{PoolRef: "pool", SourceRef: "pool-source", AccountID: "2", Error: "service unavailable"}, "")
	set(row("1", 10), bad)
	if _, ok := PoolAllowance("pool"); ok {
		t.Fatal("error fallback with recent timestamp treated as fresh")
	}
	reportStatus("stale")
	past := time.Now().Add(-time.Minute)
	bad = row("2", 80)
	bad.Windows[0].ResetsAt = &past
	set(row("1", 10), bad)
	if _, ok := PoolAllowance("pool"); ok {
		t.Fatal("expired window inferred as zero")
	}
	reportStatus("stale")
	set(row("1", 10), row("2", 80))
	if err := SaveQuotaPool(QuotaPool{ID: "pool", Name: "Changed", SourceRef: "pool-source", AccountIDs: []string{"1"}}); err != nil {
		t.Fatal(err)
	}
	if _, ok := PoolAllowance("pool"); ok {
		t.Fatal("configuration mutation did not invalidate reading")
	}
}

func TestPoolAllowanceRefreshesWithoutUsageConsumer(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("HOME", t.TempDir())
	t.Setenv("USERPROFILE", t.TempDir())
	reset := time.Now().Add(time.Hour)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var data any
		if r.URL.Path == "/api/v1/admin/accounts" {
			data = sub2APIAccounts{Total: 1, Items: []sub2APIAccount{{ID: 1, Name: "account", Platform: "openai", Type: "oauth"}}}
		} else {
			data = sub2APIUsage{FiveHour: &sub2APIWindow{Utilization: 63, ResetsAt: reset}}
		}
		json.NewEncoder(w).Encode(map[string]any{"code": 0, "data": data})
	}))
	defer server.Close()
	if err := SaveUsageSource(UsageSource{ID: "lazy-source", Name: "Source", Type: "sub2api", BaseURL: server.URL, Credential: "test"}); err != nil {
		t.Fatal(err)
	}
	if err := SaveQuotaPool(QuotaPool{ID: "lazy-pool", Name: "Pool", SourceRef: "lazy-source", AccountIDs: []string{"1"}}); err != nil {
		t.Fatal(err)
	}
	defer InvalidateUsageConfiguration()
	a, ok := PoolAllowance("lazy-pool")
	used, _ := a.For("any", time.Now())
	if !ok || used != 63 {
		t.Fatalf("routing did not fetch cold quota: used=%v known=%v", used, ok)
	}
	// A stale response starts a background refresh but is not usable until it
	// lands. No request needs a separate UI/CLI call to trigger that refresh.
	subscriptionUsageCache.Lock()
	subscriptionUsageCache.at = time.Now().Add(-2 * time.Minute)
	subscriptionUsageCache.Unlock()
	PoolAllowance("lazy-pool")
	subscriptionUsageCache.Lock()
	pending := subscriptionUsageCache.pending
	subscriptionUsageCache.Unlock()
	if pending != nil {
		<-pending
	}
	a, ok = PoolAllowance("lazy-pool")
	used, _ = a.For("any", time.Now())
	if !ok || used != 63 {
		t.Fatalf("routing did not refresh stale quota: used=%v known=%v", used, ok)
	}
}

func TestPoolAllowancePendingRefreshTimeoutIsUnknown(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	if err := SaveUsageSource(UsageSource{ID: "timeout-source", Name: "Source", Type: "sub2api", BaseURL: "http://localhost", Credential: "test"}); err != nil {
		t.Fatal(err)
	}
	if err := SaveQuotaPool(QuotaPool{ID: "timeout-pool", Name: "Pool", SourceRef: "timeout-source", AccountIDs: []string{"1"}}); err != nil {
		t.Fatal(err)
	}
	oldTimeout := subscriptionTimeout
	subscriptionTimeout = time.Millisecond
	defer func() { subscriptionTimeout = oldTimeout; InvalidateUsageConfiguration() }()
	subscriptionUsageCache.Lock()
	subscriptionUsageCache.pending = make(chan struct{})
	subscriptionUsageCache.Unlock()
	if _, ok := PoolAllowance("timeout-pool"); ok {
		t.Fatal("pending refresh timeout became known zero")
	}
}

func TestKeyPoolAllowanceRequiresEveryBoundPoolAndUsesMaximum(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("HOME", t.TempDir())
	t.Setenv("USERPROFILE", t.TempDir())
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "unavailable", http.StatusServiceUnavailable)
	}))
	defer server.Close()
	if err := SaveUsageSource(UsageSource{ID: "source", Name: "Source", Type: "sub2api", BaseURL: server.URL, Credential: "test"}); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"personal", "team"} {
		if err := SaveQuotaPool(QuotaPool{ID: id, Name: id, SourceRef: "source", AccountIDs: []string{"1"}}); err != nil {
			t.Fatal(err)
		}
	}
	defer InvalidateUsageConfiguration()
	reset := time.Now().Add(time.Hour)
	row := func(pool string, used float64) SubscriptionQuota {
		return SubscriptionQuota{PoolRef: pool, SourceRef: "source", AccountID: "1", Status: "measured", Windows: []QuotaWindow{{Name: "5 hours", Used: used, ResetsAt: &reset, Span: 5 * time.Hour}}}
	}
	set := func(rows ...SubscriptionQuota) {
		subscriptionUsageCache.Lock()
		subscriptionUsageCache.at, subscriptionUsageCache.data = time.Now(), rows
		subscriptionUsageCache.Unlock()
	}
	set(row("personal", 12), row("team", 99))
	for _, refs := range [][]string{{"personal", "team"}, {"team", "personal"}} {
		a, ok := KeyPoolAllowance(refs)
		used, _ := a.For("actual", time.Now())
		if !ok || used != 99 || !a.Full("actual", 98, time.Now()).Equal(reset) {
			t.Fatalf("combined allowance refs=%v used=%v known=%v full=%v", refs, used, ok, a.Full("actual", 98, time.Now()))
		}
	}
	if _, ok := KeyPoolAllowance(nil); ok {
		t.Fatal("unbound key treated as known")
	}
	if _, ok := KeyPoolAllowance([]string{"personal", "missing"}); ok {
		t.Fatal("missing bound pool treated as zero")
	}
	set(row("personal", 12))
	if _, ok := KeyPoolAllowance([]string{"personal", "team"}); ok {
		t.Fatal("missing pool reading treated as zero")
	}
	bad := row("team", 99)
	bad.Status = "stale"
	set(row("personal", 12), bad)
	if _, ok := KeyPoolAllowance([]string{"personal", "team"}); ok {
		t.Fatal("stale bound pool treated as known")
	}
	bad.Status, bad.Error = "error", "unavailable"
	set(row("personal", 12), bad)
	if _, ok := KeyPoolAllowance([]string{"personal", "team"}); ok {
		t.Fatal("failed bound pool treated as zero")
	}
}
