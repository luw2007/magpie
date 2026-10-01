package provider

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestFetchGLMQuota(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "glm-key" {
			t.Errorf("auth %q", r.Header.Get("Authorization"))
		}
		w.Write([]byte(`{"code":200,"data":{"limits":[{"type":"TOKENS_LIMIT","unit":3,"percentage":4,"nextResetTime":1790593287436},{"type":"TOKENS_LIMIT","unit":6,"percentage":11,"nextResetTime":1790994851984}]},"success":true}`))
	}))
	defer srv.Close()
	u, err := fetchGLMQuota(context.Background(), UsageSource{BaseURL: srv.URL}, "glm-key")
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
	q, err := fetchDeepSeekBalance(context.Background(), UsageSource{BaseURL: srv.URL}, "deep-key")
	if err != nil || q.Balance != "¥16.45" {
		t.Fatalf("DeepSeek %+v %v", q, err)
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
	u, err := fetchGoogleQuota(context.Background(), UsageSource{BaseURL: srv.URL, AuthIndex: "auth-index"}, "proxy-key")
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
