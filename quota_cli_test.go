package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/yetone/magpie/internal/provider"
)

// TestQuotaDataAsksTheGateway: a subscription's quota can need an
// environment variable only the app's launchctl environment carries (a
// local proxy's admin key, say) — magpie quota, run from a shell, doesn't
// have it, and computing the report in its own process leaves that
// subscription out (see sub2api_usage.go's SUB2API_* and CPAMC_* reads).
// A gateway already running computed the same report in its own
// environment, so quotaData must prefer its answer over a local one.
func TestQuotaDataAsksTheGateway(t *testing.T) {
	want := []provider.Quota{{
		Provider: "arkbot-codex-gpt", Name: "Codex", Kind: "subscription",
		Windows: []provider.QuotaSpan{{Name: "5 hours", Used: 10, Remaining: 90}},
	}}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/magpie/quotas" {
			t.Errorf("path = %q, want /v1/magpie/quotas", r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"object": "list", "data": want})
	}))
	defer srv.Close()
	t.Setenv("MAGPIE_ADDR", strings.TrimPrefix(srv.URL, "http://"))

	got := quotaData(context.Background())
	if len(got) != 1 || got[0].Provider != "arkbot-codex-gpt" {
		t.Fatalf("quotaData() = %+v, want the gateway's arkbot-codex-gpt", got)
	}
}

// TestQuotaDataFallsBackWithNoGateway: with no gateway running, the CLI
// still answers from its own process, in bounded time.
func TestQuotaDataFallsBackWithNoGateway(t *testing.T) {
	t.Setenv("MAGPIE_ADDR", "127.0.0.1:1") // nothing listens here
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	done := make(chan []provider.Quota, 1)
	go func() { done <- quotaData(ctx) }()
	select {
	case got := <-done:
		if got == nil {
			t.Fatalf("quotaData() = nil, want the local (possibly empty) report")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("quotaData() did not return within 5s with no gateway running")
	}
}
