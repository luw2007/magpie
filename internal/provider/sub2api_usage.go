package provider

// Local sub2api usage is an optional source of the quotas behind custom
// providers. Magpie asks sub2api's admin API directly so every upstream window
// is preserved; the older local summary script omitted five-hour windows.

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"
)

type sub2APIAccount struct {
	ID       int            `json:"id"`
	Name     string         `json:"name"`
	Platform string         `json:"platform"`
	Type     string         `json:"type"`
	Extra    map[string]any `json:"extra"`
}

type sub2APIAccounts struct {
	Items []sub2APIAccount `json:"items"`
}

type sub2APIWindow struct {
	Name        string        `json:"-"`
	Span        time.Duration `json:"-"`
	Utilization float64       `json:"utilization"`
	ResetsAt    time.Time     `json:"resets_at"`
}

type sub2APIUsage struct {
	FiveHour *sub2APIWindow `json:"five_hour"`
	SevenDay *sub2APIWindow `json:"seven_day"`
	Windows  []sub2APIWindow
}

var (
	sub2APIBaseURL     = func() string { return strings.TrimRight(os.Getenv("SUB2API_BASE_URL"), "/") }
	sub2APIKey         = func() string { return os.Getenv("SUB2API_ADMIN_API_KEY") }
	googleProxyURL     = func() string { return strings.TrimRight(os.Getenv("CPAMC_BASE_URL"), "/") }
	googleProxyToken   = func() string { return os.Getenv("CPAMC_TOKEN") }
	googleAuthIndex    = func() string { return os.Getenv("CPAMC_GOOGLE_AUTH_INDEX") }
	glmAPIKey          = func() string { return os.Getenv("ZHIPU_API_KEY") }
	deepseekAPIKey     = func() string { return os.Getenv("DEEPSEEK_API_KEY") }
	glmQuotaURL        = "https://open.bigmodel.cn/api/monitor/usage/quota/limit"
	deepseekBalanceURL = "https://api.deepseek.com/user/balance"
	sub2APIGPTAccount  = func() string {
		if name := strings.TrimSpace(os.Getenv("SUB2API_GPT_ACCOUNT")); name != "" {
			return name
		}
		return "pro more"
	}
)

// sub2APIAccountOf maps the first simple set of local provider lanes to the
// account kind that supplies their models. Provider ids are used deliberately:
// matching every GPT or Claude model would attach these quotas to independent
// relays such as TraeX or Coco.
func sub2APIAccountOf(p Provider) string {
	id := strings.ToLower(p.ID)
	switch {
	case strings.Contains(id, "claude-sub2api"):
		return "claude"
	case strings.Contains(id, "codex-gpt"):
		return "gpt"
	case strings.Contains(id, "gcloud"):
		return "google"
	case strings.Contains(id, "glm"):
		return "glm"
	case strings.Contains(id, "deepseek"):
		return "deepseek"
	}
	return ""
}

func fetchSub2APIUsage(ctx context.Context, providers []Provider) []SubscriptionQuota {
	type result struct {
		usage   map[string]sub2APIUsage
		balance *SubscriptionQuota
	}
	results := make(chan result, 3)
	go func() { results <- result{usage: fetchSub2APIAccounts(ctx)} }()
	go func() {
		u, err := fetchGoogleQuota(ctx)
		if err != nil {
			results <- result{}
			return
		}
		results <- result{usage: map[string]sub2APIUsage{"google": u}}
	}()
	go func() {
		u, err := fetchGLMQuota(ctx)
		if err != nil {
			results <- result{}
			return
		}
		results <- result{usage: map[string]sub2APIUsage{"glm": u}}
	}()
	usage := map[string]sub2APIUsage{}
	for range 3 {
		r := <-results
		for kind, u := range r.usage {
			if u.FiveHour != nil || u.SevenDay != nil || len(u.Windows) > 0 {
				usage[kind] = u
			}
		}
	}
	return sub2APIQuotas(providers, usage)
}
func fetchSub2APIAccounts(ctx context.Context) map[string]sub2APIUsage {
	base, key := sub2APIBaseURL(), sub2APIKey()
	usage := map[string]sub2APIUsage{}
	if base == "" || key == "" {
		return usage
	}
	var listed sub2APIAccounts
	if err := sub2APIGet(ctx, base+"/api/v1/admin/accounts?page=1&page_size=200&sort_by=name&sort_order=asc&lite=1", key, &listed); err != nil {
		return usage
	}
	ids := map[string]int{}
	gptName := sub2APIGPTAccount()
	for _, account := range listed.Items {
		if account.Platform == "openai" && account.Type == "oauth" && strings.EqualFold(strings.TrimSpace(account.Name), gptName) {
			ids["gpt"] = account.ID
		}
		if account.Platform == "anthropic" && ids["claude"] == 0 {
			ids["claude"] = account.ID
		}
	}
	for kind, id := range ids {
		var u sub2APIUsage
		if sub2APIGet(ctx, base+"/api/v1/admin/accounts/"+strconv.Itoa(id)+"/usage", key, &u) == nil {
			usage[kind] = u
		}
	}
	return usage
}

func sub2APIGet(ctx context.Context, url, key string, dst any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("x-api-key", key)
	req.Header.Set("Accept", "application/json")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	body, err := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	if err != nil {
		return err
	}
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return &accountStatusError{status: res.StatusCode}
	}
	var envelope struct {
		Code int             `json:"code"`
		Data json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		return err
	}
	return json.Unmarshal(envelope.Data, dst)
}

func fetchGoogleQuota(ctx context.Context) (sub2APIUsage, error) {
	body := map[string]any{
		"authIndex": googleAuthIndex(), "method": "POST",
		"url":    "https://daily-cloudcode-pa.googleapis.com/v1internal:retrieveUserQuotaSummary",
		"header": map[string]string{"Authorization": "Bearer $TOKEN$", "Content-Type": "application/json", "User-Agent": "antigravity/cli/1.0.13 (aidev_client; os_type=darwin; arch=arm64)"},
		"data":   `{"project":"aicode-consumers"}`,
	}
	var envelope struct {
		Body json.RawMessage `json:"body"`
	}
	if googleProxyURL() == "" || googleProxyToken() == "" || googleAuthIndex() == "" {
		return sub2APIUsage{}, os.ErrNotExist
	}
	if err := usageJSON(ctx, http.MethodPost, googleProxyURL()+"/v0/management/api-call", "Bearer "+googleProxyToken(), body, &envelope); err != nil {
		return sub2APIUsage{}, err
	}
	var quota struct {
		Groups []struct {
			DisplayName string `json:"displayName"`
			Buckets     []struct {
				Window    string    `json:"window"`
				Remaining float64   `json:"remainingFraction"`
				Reset     time.Time `json:"resetTime"`
			} `json:"buckets"`
		} `json:"groups"`
	}
	if err := json.Unmarshal(envelope.Body, &quota); err != nil {
		var encoded string
		if json.Unmarshal(envelope.Body, &encoded) != nil || json.Unmarshal([]byte(encoded), &quota) != nil {
			return sub2APIUsage{}, err
		}
	}
	var out sub2APIUsage
	for _, group := range quota.Groups {
		for _, bucket := range group.Buckets {
			name, span := "", time.Duration(0)
			switch bucket.Window {
			case "5h":
				name, span = group.DisplayName+" · 5 hours", 5*time.Hour
			case "weekly":
				name, span = group.DisplayName+" · 7 days", 7*24*time.Hour
			}
			if name != "" {
				out.Windows = append(out.Windows, sub2APIWindow{Name: name, Span: span, Utilization: (1 - bucket.Remaining) * 100, ResetsAt: bucket.Reset})
			}
		}
	}
	if len(out.Windows) == 0 {
		return sub2APIUsage{}, os.ErrNotExist
	}
	return out, nil
}

func fetchGLMQuota(ctx context.Context) (sub2APIUsage, error) {
	var quota struct {
		Data struct {
			Limits []struct {
				Type        string  `json:"type"`
				Unit        int     `json:"unit"`
				Used        float64 `json:"percentage"`
				ResetMillis int64   `json:"nextResetTime"`
			} `json:"limits"`
		} `json:"data"`
	}
	if glmAPIKey() == "" {
		return sub2APIUsage{}, os.ErrNotExist
	}
	if err := usageJSON(ctx, http.MethodGet, glmQuotaURL, glmAPIKey(), nil, &quota); err != nil {
		return sub2APIUsage{}, err
	}
	var out sub2APIUsage
	for _, limit := range quota.Data.Limits {
		if limit.Type != "TOKENS_LIMIT" {
			continue
		}
		w := &sub2APIWindow{Utilization: limit.Used, ResetsAt: time.UnixMilli(limit.ResetMillis)}
		switch limit.Unit {
		case 3:
			out.FiveHour = w
		case 6:
			out.SevenDay = w
		}
	}
	return out, nil
}
func fetchDeepSeekBalance(ctx context.Context, providers []Provider) (SubscriptionQuota, bool) {
	var payload struct {
		BalanceInfos []struct {
			Currency string `json:"currency"`
			Total    string `json:"total_balance"`
		} `json:"balance_infos"`
	}
	if deepseekAPIKey() == "" || usageJSON(ctx, http.MethodGet, deepseekBalanceURL, "Bearer "+deepseekAPIKey(), nil, &payload) != nil || len(payload.BalanceInfos) == 0 {
		return SubscriptionQuota{}, false
	}
	for _, p := range providers {
		if sub2APIAccountOf(p) == "deepseek" {
			amount, err := strconv.ParseFloat(payload.BalanceInfos[0].Total, 64)
			if err != nil {
				return SubscriptionQuota{}, false
			}
			return SubscriptionQuota{Provider: p.ID, Name: p.Name, Icon: p.Icon, Windows: []QuotaWindow{}, Balance: money(currencySign(payload.BalanceInfos[0].Currency), amount)}, true
		}
	}
	return SubscriptionQuota{}, false
}

func usageJSON(ctx context.Context, method, url, authorization string, input, output any) error {
	var body io.Reader
	if input != nil {
		b, err := json.Marshal(input)
		if err != nil {
			return err
		}
		body = strings.NewReader(string(b))
	}
	req, err := http.NewRequestWithContext(ctx, method, url, body)
	if err != nil {
		return err
	}
	if authorization != "" {
		req.Header.Set("Authorization", authorization)
	}
	if input != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	b, err := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	if err != nil {
		return err
	}
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return &accountStatusError{status: res.StatusCode}
	}
	return json.Unmarshal(b, output)
}

func sub2APIQuotas(providers []Provider, usage map[string]sub2APIUsage) []SubscriptionQuota {
	var quotas []SubscriptionQuota
	for _, p := range providers {
		kind := sub2APIAccountOf(p)
		u, ok := usage[kind]
		if !ok {
			continue
		}
		q := SubscriptionQuota{Provider: p.ID, Name: p.Name, Icon: p.Icon, Windows: []QuotaWindow{}}
		for _, w := range []struct {
			name string
			span time.Duration
			data *sub2APIWindow
		}{{"5 hours", 5 * time.Hour, u.FiveHour}, {"7 days", 7 * 24 * time.Hour, u.SevenDay}} {
			if w.data == nil || w.data.Utilization < 0 || w.data.Utilization > 100 || w.data.ResetsAt.IsZero() {
				continue
			}
			reset := w.data.ResetsAt
			q.Windows = append(q.Windows, QuotaWindow{Name: w.name, Used: w.data.Utilization, ResetsAt: &reset, Span: w.span})
		}
		for _, w := range u.Windows {
			if w.Name == "" || w.Utilization < 0 || w.Utilization > 100 || w.ResetsAt.IsZero() {
				continue
			}
			reset := w.ResetsAt
			q.Windows = append(q.Windows, QuotaWindow{Name: w.Name, Used: w.Utilization, ResetsAt: &reset, Span: w.Span})
		}
		if len(q.Windows) > 0 {
			quotas = append(quotas, q)
		}
	}
	return quotas
}
