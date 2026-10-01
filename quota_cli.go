package main

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/yetone/magpie/internal/provider"
)

const quotaUsage = `usage: magpie quota [<provider>…] [--json]
       magpie quota reset [<codex account>] [--yes]
       magpie quota sources <list|add|edit|remove|discover|import-env> [flags]
       magpie quota pools <list|add|edit|remove> [flags]
  what is left of every subscription, plan and key magpie has: each window's use and
  when it starts again, and each key's balance, asked of the vendors now (or less than
  a minute ago). --json is for scripts and agents; the gateway answers the same at
  GET http://127.0.0.1:3425/v1/magpie/quotas
  A Codex account that holds rate-limit resets says how many; quota reset spends one,
  starting the account's current windows again (the one Codex is signed in to unless
  named). It can't be undone, so it asks first; --yes doesn't.`

// quotaCmd: magpie quota [<provider>…] [--json]
func quotaCmd(args []string) error {
	if len(args) > 1 && (args[1] == "sources" || args[1] == "pools") {
		return quotaBindingsCmd(args[1:])
	}
	if len(args) > 1 && args[1] == "reset" {
		return quotaResetCmd(args[2:])
	}
	asJSON := false
	var only []string
	for _, a := range args[1:] {
		switch a {
		case "--json", "-j":
			asJSON = true
		case "help", "-h", "--help":
			fmt.Println(quotaUsage)
			return nil
		default:
			only = append(only, strings.ToLower(a))
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	qs := []provider.Quota{}
	for _, q := range provider.QuotaReport(ctx, time.Now()) {
		if len(only) == 0 || quotaMatches(q, only) {
			qs = append(qs, q)
		}
	}
	qs, err := withUntold(qs, only)
	if err != nil {
		return err
	}
	if asJSON {
		b, _ := json.MarshalIndent(qs, "", "  ")
		fmt.Println(string(b))
		return nil
	}
	if len(qs) == 0 {
		if len(only) > 0 {
			return fmt.Errorf("no subscription, plan or key balance of %s", strings.Join(only, ", "))
		}
		fmt.Println(muted.Render("nothing to tell ·"), "a signed-in subscription, a coding plan or a key whose vendor tells its balance shows up here")
		return nil
	}
	width := 0
	for _, q := range qs {
		width = max(width, len([]rune(quotaTitle(q))))
	}
	for _, q := range qs {
		line := fmt.Sprintf("%-*s  %s", width, quotaTitle(q), muted.Render(fmt.Sprintf("%-12s", q.Kind)))
		for _, w := range q.Windows {
			line += "  " + quotaCell(w)
		}
		if q.Balance != "" {
			line += "  " + bold.Render(q.Balance) + muted.Render(" left")
		}
		if q.Resets != nil {
			line += "  " + resetsCell(q.Resets)
		}
		if q.Error != "" {
			line += "  " + muted.Render(q.Error)
		}
		fmt.Println(line)
	}
	fmt.Println(faint.Render("  % is how much of a window is used · ↻ when it starts again · --json for scripts, or GET /v1/magpie/quotas on the gateway"))
	return nil
}

// resetsCell is a Codex account's rate-limit resets in a line: "↺ 2
// resets until Oct 3 14:30", no date when they don't run out.
func resetsCell(r *provider.ResetCredits) string {
	cell := "↺ " + plural(r.Count, "reset")
	if r.Until != nil {
		cell += muted.Render(" until " + provider.ResetClock(*r.Until, time.Now()))
	}
	return cell
}

// quotaResetCmd: magpie quota reset [<codex account>] [--yes] — spends one
// of the account's rate-limit resets, once the user has said so.
func quotaResetCmd(args []string) error {
	yes, user := false, ""
	for _, a := range args {
		switch a {
		case "--yes", "-y":
			yes = true
		case "help", "-h", "--help":
			fmt.Println(quotaUsage)
			return nil
		default:
			if user != "" {
				return fmt.Errorf("one account at a time: %s or %s", user, a)
			}
			user = a
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	// what the account holds, and whose it is when none was named
	var held *provider.SubscriptionQuota
	for _, q := range provider.SubscriptionUsage(ctx) {
		if q.Provider == "codex" && (user == "" || strings.EqualFold(q.User, user)) {
			held = &q
			break
		}
	}
	if held == nil {
		if user != "" {
			return fmt.Errorf("no Codex account %s", user)
		}
		return fmt.Errorf("Codex isn't signed in")
	}
	who := cmp.Or(held.User, "the Codex account")
	if held.Resets == nil && held.Error == "" {
		return fmt.Errorf("%s holds no rate-limit reset", who)
	}
	if !yes {
		n := "one of its resets"
		if held.Resets != nil {
			n = "one of its " + plural(held.Resets.Count, "reset")
		}
		if held.Resets != nil && held.Resets.Until != nil {
			n += " (the one that runs out first, " + provider.ResetClock(*held.Resets.Until, time.Now()) + ")"
		}
		fmt.Printf("Spend %s on %s, starting its current windows again? It can't be undone. [y/N] ", n, who)
		line, _ := stdin.ReadString('\n')
		if a := strings.ToLower(strings.TrimSpace(line)); a != "y" && a != "yes" {
			return fmt.Errorf("nothing spent")
		}
	}
	out, err := provider.UseCodexReset(ctx, held.User)
	if err != nil {
		return err
	}
	if out.Code != "reset" {
		return fmt.Errorf("%s: %s", who, out.Text())
	}
	fmt.Println(green.Render("✓"), who+":", out.Text())
	return nil
}

// quotaTitle is a quota's line head: its provider, plan and account.
func quotaTitle(q provider.Quota) string {
	t := q.Provider
	if q.Plan != "" {
		t += " · " + q.Plan
	}
	if s := provider.PlanTerm(q.Until, q.Renew); s != "" {
		t += " · " + s
	}
	if q.User != "" {
		t += " · " + q.User
	}
	return t
}

func quotaMatches(q provider.Quota, only []string) bool {
	for _, o := range only {
		if strings.EqualFold(q.Provider, o) || strings.EqualFold(q.Name, o) || strings.EqualFold(q.Kind, o) {
			return true
		}
	}
	return false
}

// withUntold adds a line for each provider named that told nothing, saying
// why, rather than leaving it out as if it weren't there; a name that is no
// provider at all is an error.
func withUntold(qs []provider.Quota, only []string) ([]provider.Quota, error) {
	for _, o := range only {
		if o == "subscription" || o == "plan" || o == "balance" || slices.ContainsFunc(qs, func(q provider.Quota) bool { return quotaMatches(q, []string{o}) }) {
			continue
		}
		p, err := provider.Find(o)
		if err != nil {
			return nil, err
		}
		qs = append(qs, provider.Quota{Provider: p.ID, Name: p.Name, Kind: "balance", Windows: []provider.QuotaSpan{},
			Error: "not configured · no balance endpoint known for it; set Balance URL and Balance field in its editor"})
	}
	return qs, nil
}
