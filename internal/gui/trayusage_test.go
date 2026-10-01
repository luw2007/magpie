package gui

import (
	"testing"
	"time"

	"github.com/yetone/magpie/internal/provider"
)

func TestTrayUsageText(t *testing.T) {
	now := time.Date(2026, 9, 27, 12, 0, 0, 0, time.UTC)
	in := func(d time.Duration) *time.Time { at := now.Add(d); return &at }

	// the shortest window first, whatever order the vendor gives them in;
	// on-demand spending and a single model's window are left out
	q := provider.SubscriptionQuota{Provider: "claude", Name: "Claude", User: "a@b.c", Windows: []provider.QuotaWindow{
		{Name: "Weekly", Used: 17.6, ResetsAt: in(3*24*time.Hour + 4*time.Hour), Span: 7 * 24 * time.Hour},
		{Name: "5-hour", Used: 42.2, ResetsAt: in(2*time.Hour + 10*time.Minute), Span: 5 * time.Hour},
		{Name: "Opus weekly", Used: 90, Model: "opus", Span: 7 * 24 * time.Hour},
		{Name: "Extra usage", Used: 5, Aside: true},
	}}
	label, tip := trayUsageText(q, now, false)
	if label != "42% · 18%" {
		t.Errorf("label %q", label)
	}
	if want := "Claude\n5-hour 42% used · resets in 2h 10m\nWeekly 18% used · resets in 3d 4h"; tip != want {
		t.Errorf("tip %q, want %q", tip, want)
	}
	// or what is left of each, as Settings or the Usage page says (#122)
	label, tip = trayUsageText(q, now, true)
	if label != "58% · 82%" {
		t.Errorf("left label %q", label)
	}
	if want := "Claude\n5-hour 58% left · resets in 2h 10m\nWeekly 82% left · resets in 3d 4h"; tip != want {
		t.Errorf("left tip %q, want %q", tip, want)
	}
	if id := trayCardID(q); id != "claude|a@b.c" {
		t.Errorf("id %q", id)
	}

	// no lengths known: the vendor's order, two at most, clamped, its own count
	q = provider.SubscriptionQuota{Provider: "zcode", Name: "ZCode", Windows: []provider.QuotaWindow{
		{Name: "5 小时", Used: 120, Display: "1.2k / 1k"},
		{Name: "Weekly", Used: -3, ResetSecs: 90},
		{Name: "Monthly", Used: 1},
	}}
	label, tip = trayUsageText(q, now, false)
	if label != "100% · 0%" {
		t.Errorf("label %q", label)
	}
	if want := "ZCode\n5 小时 1.2k / 1k · 100% used\nWeekly 0% used · resets in 2m\nMonthly 1% used"; tip != want {
		t.Errorf("tip %q, want %q", tip, want)
	}
	if id := trayCardID(q); id != "zcode" {
		t.Errorf("id %q", id)
	}

	// a balance, an error, nothing
	if label, _ = trayUsageText(provider.SubscriptionQuota{Name: "DeepSeek", Balance: "¥12.30"}, now, false); label != "¥12.30" {
		t.Errorf("balance label %q", label)
	}
	if label, tip = trayUsageText(provider.SubscriptionQuota{Name: "Codex", Error: "signed out"}, now, false); label != "" || tip != "Codex: signed out" {
		t.Errorf("error: %q %q", label, tip)
	}
	if label, tip = trayUsageText(provider.SubscriptionQuota{Name: "Empty"}, now, false); label != "" || tip != "" {
		t.Errorf("empty: %q %q", label, tip)
	}
}

func TestTrayPoolSelectionIdentity(t *testing.T) {
	shared := provider.SubscriptionQuota{PoolRef: "shared", Name: "Shared allowance"}
	other := provider.SubscriptionQuota{PoolRef: "other", Name: "Shared allowance"}
	if got := trayCardID(shared); got != "pool|shared" {
		t.Fatalf("shared pool selection = %q, want pool|shared", got)
	}
	if trayCardID(shared) == trayCardID(other) {
		t.Fatal("distinct pools with the same display name must remain independently selectable")
	}
	shared.Provider, shared.User = "vendor-a", "account-a"
	if got := trayCardID(shared); got != "pool|shared" {
		t.Fatalf("binding a provider changed pool selection to %q", got)
	}
	shared.Provider, shared.User = "vendor-b", "account-b"
	if got := trayCardID(shared); got != "pool|shared" {
		t.Fatalf("changing provider binding changed pool selection to %q", got)
	}
}
