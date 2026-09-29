package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/yetone/magpie/internal/provider"
)

// The Usage page lists the accounts' windows and balances as the app's
// cards do, a window reading as used or left, and the vendor's own count
// beside the percentage, and when each window starts again.
func TestQuotaLines(t *testing.T) {
	now := time.Date(2026, 9, 26, 12, 0, 0, 0, time.UTC)
	reset, soon := now.Add(3*time.Hour), now.Add(40*time.Minute)
	qs := []provider.SubscriptionQuota{
		{Name: "Codex", Plan: "Plus", User: "me@example.com", Windows: []provider.QuotaWindow{
			{Name: "5 hours", Used: 64, ResetsAt: &soon},
			{Name: "7 days", Used: 99, ResetsAt: &reset},
		}},
		{Name: "ZCode", Windows: []provider.QuotaWindow{{Name: "Month", Used: 1.2, Display: "345 / 28000"}}},
		{Name: "DeepSeek", Balance: "¥12.30"},
		{Name: "Gemini", Error: "sign-in has expired"},
	}
	plain := func(ls []string) string { return strings.Join(ls, "\n") } // no terminal here, so no colour

	used := plain(quotaLines(qs, true, false, 200, now))
	for _, want := range []string{"Codex · Plus · me@example.com", "64% used ↻ " + provider.ResetClock(soon, now) + " (40m)", "99% used ↻ " + provider.ResetClock(reset, now) + " (3h)", "345 / 28000 · 1% used", "¥12.30 left", "sign-in has expired"} {
		if !strings.Contains(used, want) {
			t.Errorf("used: missing %q in\n%s", want, used)
		}
	}
	left := plain(quotaLines(qs, true, true, 200, now))
	for _, want := range []string{"36% left", "1% left", "345 / 28000 · 99% left", "% is how much is left"} {
		if !strings.Contains(left, want) {
			t.Errorf("left: missing %q in\n%s", want, left)
		}
	}
	// a narrow terminal puts the windows that don't fit on lines of their own
	if n := len(quotaLines(qs[:1], true, false, 60, now)); n != 3 {
		t.Errorf("narrow: %d lines, want 3 (head, and a window a line)", n)
	}
	// a Codex account's rate-limit resets follow its windows, until when
	// only when they run out
	expires := now.Add(7 * 24 * time.Hour)
	held := []provider.SubscriptionQuota{qs[0], qs[0]}
	held[0].Resets = &provider.ResetCredits{Count: 2, Until: &expires}
	held[1].Resets = &provider.ResetCredits{Count: 1}
	got := quotaLines(held, true, false, 200, now)
	if !strings.HasSuffix(got[1], "↺ 2 resets until "+provider.ResetClock(expires, now)) || !strings.HasSuffix(got[2], "↺ 1 reset") {
		t.Errorf("resets:\n%s", plain(got))
	}
	if quotaLines(nil, false, false, 80, now) != nil {
		t.Error("not asked yet: want nothing")
	}
	if got := plain(quotaLines(nil, true, false, 80, now)); !strings.Contains(got, "asking the vendors") {
		t.Errorf("asking: %q", got)
	}
}
