package provider

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestSub2APIQuotasPreserveFiveHourAndSevenDay(t *testing.T) {
	five, _ := time.Parse(time.RFC3339, "2026-09-28T18:20:00+08:00")
	seven, _ := time.Parse(time.RFC3339, "2026-10-04T00:58:40+08:00")
	providers := []Provider{
		{ID: "arkbot-codex-gpt", Name: "GPT"},
		{ID: "arkbot-claude-sub2api", Name: "Claude"},
		{ID: "devbox-traex", Name: "TraeX"},
	}
	got := sub2APIQuotas(providers, map[string]sub2APIUsage{
		"gpt":    {FiveHour: &sub2APIWindow{Utilization: 2, ResetsAt: five}, SevenDay: &sub2APIWindow{Utilization: 16, ResetsAt: seven}},
		"claude": {FiveHour: &sub2APIWindow{Utilization: 3, ResetsAt: five}},
	})
	if len(got) != 2 {
		t.Fatalf("quotas %+v", got)
	}
	if ws := got[0].Windows; len(ws) != 2 || ws[0].Name != "5 hours" || ws[0].Used != 2 || ws[0].Span != 5*time.Hour || ws[1].Name != "7 days" || ws[1].Used != 16 || ws[1].Span != 7*24*time.Hour {
		t.Fatalf("GPT windows %+v", ws)
	}
	if ws := got[1].Windows; len(ws) != 1 || ws[0].Name != "5 hours" || ws[0].Used != 3 {
		t.Fatalf("Claude windows %+v", ws)
	}
}

func TestFetchSub2APIUsageUsesAdminAPI(t *testing.T) {
	var paths []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		if r.Header.Get("x-api-key") != "secret" {
			t.Errorf("key %q", r.Header.Get("x-api-key"))
		}
		w.Header().Set("Content-Type", "application/json")
		var data any
		switch r.URL.Path {
		case "/api/v1/admin/accounts":
			data = sub2APIAccounts{Items: []sub2APIAccount{{ID: 2, Platform: "openai", Type: "oauth"}, {ID: 6, Platform: "anthropic", Type: "setup-token"}}}
		case "/api/v1/admin/accounts/2/usage":
			data = map[string]any{"five_hour": map[string]any{"utilization": 4, "resets_at": "2026-09-28T18:20:00+08:00"}, "seven_day": map[string]any{"utilization": 16, "resets_at": "2026-10-04T00:58:40+08:00"}}
		case "/api/v1/admin/accounts/6/usage":
			data = map[string]any{"five_hour": map[string]any{"utilization": 2, "resets_at": "2026-09-28T18:20:00+08:00"}}
		default:
			http.NotFound(w, r)
			return
		}
		b, _ := json.Marshal(map[string]any{"code": 0, "data": data})
		w.Write(b)
	}))
	defer srv.Close()
	oldBase, oldKey := sub2APIBaseURL, sub2APIKey
	sub2APIBaseURL, sub2APIKey = func() string { return srv.URL }, func() string { return "secret" }
	t.Cleanup(func() { sub2APIBaseURL, sub2APIKey = oldBase, oldKey })
	got := fetchSub2APIUsage(context.Background(), []Provider{{ID: "local-codex-gpt"}, {ID: "local-claude-sub2api"}})
	if len(got) != 2 || len(got[0].Windows) != 2 || len(got[1].Windows) != 1 {
		t.Fatalf("fetched %+v", got)
	}
	if strings.Join(paths, ",") != "/api/v1/admin/accounts,/api/v1/admin/accounts/2/usage,/api/v1/admin/accounts/6/usage" {
		t.Fatalf("paths %v", paths)
	}
}

func TestSub2APIQuotasIgnoreInvalidAndUnrelated(t *testing.T) {
	reset := time.Now().Add(time.Hour)
	got := sub2APIQuotas([]Provider{{ID: "relay"}, {ID: "my-codex-gpt"}}, map[string]sub2APIUsage{"gpt": {FiveHour: &sub2APIWindow{Utilization: 101, ResetsAt: reset}}})
	if len(got) != 0 {
		t.Fatalf("invalid %+v", got)
	}
}

func TestFetchGLMQuota(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "glm-key" {
			t.Errorf("auth %q", r.Header.Get("Authorization"))
		}
		w.Write([]byte(`{"code":200,"data":{"limits":[{"type":"TOKENS_LIMIT","unit":3,"percentage":4,"nextResetTime":1790593287436},{"type":"TOKENS_LIMIT","unit":6,"percentage":11,"nextResetTime":1790994851984}]},"success":true}`))
	}))
	defer srv.Close()
	oldKey, oldURL := glmAPIKey, glmQuotaURL
	glmAPIKey, glmQuotaURL = func() string { return "glm-key" }, srv.URL
	t.Cleanup(func() { glmAPIKey, glmQuotaURL = oldKey, oldURL })
	u, err := fetchGLMQuota(context.Background())
	if err != nil || u.FiveHour == nil || u.FiveHour.Utilization != 4 || u.SevenDay == nil || u.SevenDay.Utilization != 11 {
		t.Fatalf("GLM %+v %v", u, err)
	}
}

func TestFetchDeepSeekBalance(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer deep-key" {
			t.Errorf("auth %q", r.Header.Get("Authorization"))
		}
		w.Write([]byte(`{"is_available":true,"balance_infos":[{"currency":"CNY","total_balance":"16.45"}]}`))
	}))
	defer srv.Close()
	oldKey, oldURL := deepseekAPIKey, deepseekBalanceURL
	deepseekAPIKey, deepseekBalanceURL = func() string { return "deep-key" }, srv.URL
	t.Cleanup(func() { deepseekAPIKey, deepseekBalanceURL = oldKey, oldURL })
	q, ok := fetchDeepSeekBalance(context.Background(), []Provider{{ID: "arkbot-deepseek", Name: "DeepSeek"}})
	if !ok || q.Balance != "¥16.45" || q.Provider != "arkbot-deepseek" {
		t.Fatalf("DeepSeek %+v %v", q, ok)
	}
}

func TestFetchGoogleQuota(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer proxy-key" {
			t.Errorf("auth %q", r.Header.Get("Authorization"))
		}
		w.Write([]byte(`{"body":"{\"groups\":[{\"displayName\":\"Gemini Models\",\"buckets\":[{\"window\":\"weekly\",\"remainingFraction\":0.87,\"resetTime\":\"2026-09-30T04:09:39Z\"},{\"window\":\"5h\",\"remainingFraction\":1,\"resetTime\":\"2026-09-28T11:19:44Z\"}]},{\"displayName\":\"Claude and GPT models\",\"buckets\":[{\"window\":\"weekly\",\"remainingFraction\":0.99,\"resetTime\":\"2026-10-04T10:18:14Z\"},{\"window\":\"5h\",\"remainingFraction\":1,\"resetTime\":\"2026-09-28T11:19:44Z\"}]}]}"}`))
	}))
	defer srv.Close()
	oldURL, oldToken, oldIndex := googleProxyURL, googleProxyToken, googleAuthIndex
	googleProxyURL, googleProxyToken, googleAuthIndex = func() string { return srv.URL }, func() string { return "proxy-key" }, func() string { return "auth-index" }
	t.Cleanup(func() { googleProxyURL, googleProxyToken, googleAuthIndex = oldURL, oldToken, oldIndex })
	u, err := fetchGoogleQuota(context.Background())
	if err != nil || len(u.Windows) != 4 {
		t.Fatalf("Google %+v %v", u, err)
	}
	wants := []string{"Gemini Models · 7 days", "Gemini Models · 5 hours", "Claude and GPT models · 7 days", "Claude and GPT models · 5 hours"}
	for i, want := range wants {
		if u.Windows[i].Name != want {
			t.Fatalf("window %d: %+v", i, u.Windows[i])
		}
	}
}
