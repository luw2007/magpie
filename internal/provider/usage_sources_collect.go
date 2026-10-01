package provider

import (
	"context"
	"fmt"
	"math"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// SourceAccount identifies an upstream account within one usage source.
type SourceAccount struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Platform string `json:"platform"`
	Type     string `json:"type"`
}

func DiscoverUsageSource(ctx context.Context, id string) ([]SourceAccount, error) {
	if err := CheckUsageConfiguration(); err != nil {
		return nil, err
	}
	for _, source := range UsageSources() {
		if source.ID == id {
			if source.Off {
				return nil, fmt.Errorf("usage source %q is off", id)
			}
			credential, err := SourceCredential(source)
			if source.Type == "traex" && source.Credential == "" && source.CredentialEnv == "" {
				err = nil
			}
			if err != nil {
				return nil, err
			}
			return discoverSourceAccounts(ctx, source, credential)
		}
	}
	return nil, fmt.Errorf("usage source %q not found", id)
}

func discoverSourceAccounts(ctx context.Context, source UsageSource, credential string) ([]SourceAccount, error) {
	switch source.Type {
	case "sub2api":
		var out []SourceAccount
		seen := map[int]bool{}
		count := 0
		for page := 1; ; page++ {
			var listed sub2APIAccounts
			endpoint := strings.TrimRight(source.BaseURL, "/") + "/api/v1/admin/accounts?page=" + strconv.Itoa(page) + "&page_size=200&sort_by=name&sort_order=asc&lite=1"
			if err := sub2APIGet(ctx, endpoint, credential, &listed); err != nil {
				return nil, err
			}
			newIDs := 0
			for _, account := range listed.Items {
				if seen[account.ID] {
					continue
				}
				seen[account.ID] = true
				newIDs++
				if account.Type != "oauth" {
					continue
				}
				out = append(out, SourceAccount{ID: strconv.Itoa(account.ID), Name: account.Name, Platform: account.Platform, Type: account.Type})
			}
			count += len(listed.Items)
			if len(listed.Items) == 0 || (listed.Total > 0 && count >= listed.Total) {
				break
			}
			if newIDs == 0 {
				return nil, fmt.Errorf("account pagination made no progress")
			}
		}
		return out, nil
	case "google-proxy", "glm", "deepseek", "traex":
		id := "default"
		if source.Type == "google-proxy" && source.AuthIndex != "" {
			id = source.AuthIndex
		}
		return []SourceAccount{{ID: id, Name: source.Name, Platform: source.Type, Type: source.Type}}, nil
	default:
		return nil, fmt.Errorf("unsupported usage source type %q", source.Type)
	}
}

func sourceAccountQuota(ctx context.Context, source UsageSource, credential string, account SourceAccount) SubscriptionQuota {
	var usage sub2APIUsage
	var q SubscriptionQuota
	var err error
	switch source.Type {
	case "sub2api":
		id, parseErr := strconv.ParseUint(account.ID, 10, 64)
		if parseErr != nil || id == 0 {
			err = fmt.Errorf("sub2api account ID must be a positive upstream integer ID")
		} else {
			err = sub2APIGet(ctx, strings.TrimRight(source.BaseURL, "/")+"/api/v1/admin/accounts/"+account.ID+"/usage", credential, &usage)
		}
	case "google-proxy":
		usage, err = fetchGoogleQuota(ctx, source, credential)
	case "glm":
		usage, err = fetchGLMQuota(ctx, source, credential)
	case "deepseek":
		q, err = fetchDeepSeekBalance(ctx, source, credential)
	case "traex":
		q, err = fetchTraexQuota(ctx, source)
	default:
		err = fmt.Errorf("unsupported usage source type %q", source.Type)
	}
	q.SourceRef, q.AccountID, q.DisplayName = source.ID, account.ID, account.Name
	q.Name, q.User = source.Name, account.Name
	if q.Windows == nil {
		q.Windows = []QuotaWindow{}
	}
	if err != nil {
		q.Status, q.Error = "error", err.Error()
		return q
	}
	windows := usage.Windows
	if usage.FiveHour != nil {
		w := *usage.FiveHour
		w.Name, w.Span = "5 hours", 5*time.Hour
		windows = append(windows, w)
	}
	if usage.SevenDay != nil {
		w := *usage.SevenDay
		w.Name, w.Span = "7 days", 7*24*time.Hour
		windows = append(windows, w)
	}
	seen := map[string]bool{}
	for _, w := range windows {
		if w.Name == "" || math.IsNaN(w.Utilization) || math.IsInf(w.Utilization, 0) || w.Utilization < 0 || w.Utilization > 100 || w.ResetsAt.IsZero() {
			continue
		}
		if !w.reported {
			continue
		}
		identity := w.Name + "/" + w.ResetsAt.Format(time.RFC3339Nano)
		if seen[identity] {
			continue
		}
		seen[identity] = true
		reset := w.ResetsAt
		q.Windows = append(q.Windows, QuotaWindow{Name: w.Name, Used: w.Utilization, ResetsAt: &reset, Span: w.Span})
	}
	now := time.Now()
	q.AsOf = &now
	q.Status = quotaStatus(q, now)
	return q
}

func poolKeyRefs(poolID string, providers []Provider) []string {
	var refs []string
	for _, p := range providers {
		for _, key := range p.Keys {
			if slices.Contains(key.PoolRefs, poolID) {
				refs = append(refs, p.ID+"/"+key.ID)
			}
		}
	}
	sort.Strings(refs)
	return refs
}

// collectUsageSources fetches each source-account once, then associates readings
// with pools, not keys. The same upstream ID in two sources is never merged.
func collectUsageSources(ctx context.Context, sources []UsageSource, pools []QuotaPool, providers []Provider) []SubscriptionQuota {
	if err := CheckUsageConfiguration(); err != nil {
		return []SubscriptionQuota{{Name: "Usage configuration", DisplayName: "Usage configuration", Status: "error", Error: "usage configuration is unreadable or malformed", Windows: []QuotaWindow{}}}
	}
	results := make([][]SubscriptionQuota, len(sources))
	var wg sync.WaitGroup
	for i, source := range sources {
		wg.Add(1)
		go func() {
			defer wg.Done()
			credential, err := SourceCredential(source)
			if source.Type == "traex" && source.Credential == "" && source.CredentialEnv == "" {
				err = nil
			}
			if source.Off {
				err = fmt.Errorf("usage source is off")
			}
			var accounts []SourceAccount
			if err == nil {
				accounts, err = discoverSourceAccounts(ctx, source, credential)
			}
			readings := map[string]SubscriptionQuota{}
			byID := map[string]SourceAccount{}
			for _, account := range accounts {
				byID[account.ID] = account
			}
			for _, pool := range pools {
				if pool.SourceRef != source.ID {
					continue
				}
				ids := pool.AccountIDs
				if len(ids) == 0 && source.Type != "sub2api" && len(accounts) == 1 {
					ids = []string{accounts[0].ID}
				}
				if len(ids) == 0 {
					ids = []string{""}
				}
				seen := map[string]bool{}
				for _, id := range ids {
					if seen[id] {
						continue
					}
					seen[id] = true
					q, ok := readings[id]
					if !ok {
						account, found := byID[id]
						switch {
						case source.Off:
							q = SubscriptionQuota{Status: "unknown", Error: "usage source is off"}
						case err != nil:
							q = SubscriptionQuota{Status: "error", Error: err.Error()}
						case !found:
							q = SubscriptionQuota{Status: "unknown", Error: "selected upstream account not found"}
						default:
							q = sourceAccountQuota(ctx, source, credential, account)
						}
						readings[id] = q
					}
					q.PoolRef, q.SourceRef, q.AccountID = pool.ID, source.ID, id
					q.Name = pool.Name
					if q.DisplayName == "" {
						q.DisplayName = pool.Name
					}
					q.KeyRefs = poolKeyRefs(pool.ID, providers)
					results[i] = append(results[i], keepLast(q, ""))
				}
			}
		}()
	}
	wg.Wait()
	var out []SubscriptionQuota
	for _, rows := range results {
		out = append(out, rows...)
	}
	configured := map[string]bool{}
	for _, source := range sources {
		configured[source.ID] = true
	}
	for _, pool := range pools {
		if configured[pool.SourceRef] {
			continue
		}
		ids := pool.AccountIDs
		if len(ids) == 0 {
			ids = []string{""}
		}
		for _, id := range ids {
			out = append(out, SubscriptionQuota{PoolRef: pool.ID, SourceRef: pool.SourceRef, AccountID: id, KeyRefs: poolKeyRefs(pool.ID, providers), Name: pool.Name, DisplayName: pool.Name, Status: "error", Error: "configured usage source not found", Windows: []QuotaWindow{}})
		}
	}
	return out
}

// PoolAllowance reads only the cache; a missing or stale member cannot be
// interpreted as zero usage. For() takes the maximum of all applicable windows.
func PoolAllowance(poolRef string) (Allowance, bool) {
	var pool QuotaPool
	if err := CheckUsageConfiguration(); err != nil {
		return nil, false
	}
	found := false
	for _, p := range QuotaPools() {
		if p.ID == poolRef {
			pool, found = p, true
			break
		}
	}
	if !found {
		return nil, false
	}
	active := false
	for _, source := range UsageSources() {
		if source.ID == pool.SourceRef && !source.Off {
			active = true
			break
		}
	}
	if !active {
		return nil, false
	}
	selected := map[string]bool{}
	for _, id := range pool.AccountIDs {
		selected[id] = true
	}
	// Routing is a quota consumer too: start the shared bounded refresh even
	// when no Usage UI or quota command has ever been opened. Never hold the
	// cache lock while SubscriptionUsage waits for its first refresh.
	ctx, cancel := context.WithTimeout(context.Background(), subscriptionTimeout)
	SubscriptionUsage(ctx)
	cancel()
	c := &subscriptionUsageCache
	c.Lock()
	defer c.Unlock()
	if c.at.IsZero() || time.Since(c.at) >= time.Minute {
		return nil, false
	}
	seen := map[string]bool{}
	var allowance Allowance
	for _, q := range c.data {
		if q.PoolRef != poolRef || q.SourceRef != pool.SourceRef {
			continue
		}
		if len(selected) > 0 && !selected[q.AccountID] {
			continue
		}
		if quotaStatus(q, time.Now()) != "measured" || len(q.Windows) == 0 {
			return nil, false
		}
		for _, w := range q.Windows {
			if w.ResetsAt != nil && !w.ResetsAt.After(time.Now()) {
				return nil, false
			}
		}
		seen[q.AccountID] = true
		allowance = append(allowance, allowanceOf(q.Windows, time.Now())...)
	}
	for _, id := range pool.AccountIDs {
		if !seen[id] {
			return nil, false
		}
	}
	return allowance, len(allowance) > 0
}

// KeyPoolAllowance combines all bound pools without treating an unknown pool
// as zero usage. For() selects the maximum across their applicable windows.
func KeyPoolAllowance(poolRefs []string) (Allowance, bool) {
	if len(poolRefs) == 0 {
		return nil, false
	}
	var allowance Allowance
	for _, poolRef := range poolRefs {
		poolAllowance, ok := PoolAllowance(poolRef)
		if !ok {
			return nil, false
		}
		allowance = append(allowance, poolAllowance...)
	}
	return allowance, true
}
