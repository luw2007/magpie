package provider

// Local sub2api usage is an optional source of the quotas behind custom
// providers. Magpie asks sub2api's admin API directly so every upstream window
// is preserved; the older local summary script omitted five-hour windows.

import (
	"context"
	"encoding/json"
	"fmt"
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
	Total int              `json:"total"`
}

type sub2APIWindow struct {
	Name        string        `json:"-"`
	Span        time.Duration `json:"-"`
	Utilization float64       `json:"utilization"`
	ResetsAt    time.Time     `json:"resets_at"`
	reported    bool
}

func (w *sub2APIWindow) UnmarshalJSON(body []byte) error {
	var wire struct {
		Used  *float64  `json:"utilization"`
		Reset time.Time `json:"resets_at"`
	}
	if err := json.Unmarshal(body, &wire); err != nil {
		return err
	}
	w.ResetsAt, w.reported = wire.Reset, wire.Used != nil
	if wire.Used != nil {
		w.Utilization = *wire.Used
	}
	return nil
}

type sub2APIUsage struct {
	AccountID   int            `json:"-"`
	AccountName string         `json:"-"`
	FiveHour    *sub2APIWindow `json:"five_hour"`
	SevenDay    *sub2APIWindow `json:"seven_day"`
	Windows     []sub2APIWindow
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
	if envelope.Code != 0 {
		return fmt.Errorf("usage API reported error code %d", envelope.Code)
	}
	return json.Unmarshal(envelope.Data, dst)
}

func fetchGoogleQuota(ctx context.Context, source UsageSource, credential string) (sub2APIUsage, error) {
	data := map[string]string{}
	if source.Project != "" {
		data["project"] = source.Project
	}
	encoded, err := json.Marshal(data)
	if err != nil {
		return sub2APIUsage{}, err
	}
	body := map[string]any{
		"authIndex": source.AuthIndex, "method": "POST",
		"url":    "https://daily-cloudcode-pa.googleapis.com/v1internal:retrieveUserQuotaSummary",
		"header": map[string]string{"Authorization": "Bearer $TOKEN$", "Content-Type": "application/json", "User-Agent": "antigravity/cli/1.0.13 (aidev_client; os_type=darwin; arch=arm64)"},
		"data":   string(encoded),
	}
	var envelope struct {
		Body json.RawMessage `json:"body"`
	}
	if err := usageJSON(ctx, http.MethodPost, strings.TrimRight(source.BaseURL, "/")+"/v0/management/api-call", "Bearer "+credential, body, &envelope); err != nil {
		return sub2APIUsage{}, err
	}
	var quota struct {
		Groups []struct {
			DisplayName string `json:"displayName"`
			Buckets     []struct {
				Window    string    `json:"window"`
				Remaining *float64  `json:"remainingFraction"`
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
				if bucket.Remaining != nil {
					out.Windows = append(out.Windows, sub2APIWindow{Name: name, Span: span, Utilization: (1 - *bucket.Remaining) * 100, ResetsAt: bucket.Reset, reported: true})
				}
			}
		}
	}
	if len(out.Windows) == 0 {
		return sub2APIUsage{}, os.ErrNotExist
	}
	return out, nil
}

func fetchGLMQuota(ctx context.Context, source UsageSource, credential string) (sub2APIUsage, error) {
	var quota struct {
		Data struct {
			Limits []struct {
				Type        string   `json:"type"`
				Unit        int      `json:"unit"`
				Used        *float64 `json:"percentage"`
				ResetMillis int64    `json:"nextResetTime"`
			} `json:"limits"`
		} `json:"data"`
	}
	endpoint := "https://open.bigmodel.cn/api/monitor/usage/quota/limit"
	if source.BaseURL != "" {
		endpoint = strings.TrimRight(source.BaseURL, "/") + "/api/monitor/usage/quota/limit"
	}
	if err := usageJSON(ctx, http.MethodGet, endpoint, credential, nil, &quota); err != nil {
		return sub2APIUsage{}, err
	}
	var out sub2APIUsage
	for _, limit := range quota.Data.Limits {
		if limit.Type != "TOKENS_LIMIT" || limit.Used == nil || limit.ResetMillis <= 0 {
			continue
		}
		w := &sub2APIWindow{Utilization: *limit.Used, ResetsAt: time.UnixMilli(limit.ResetMillis), reported: true}
		switch limit.Unit {
		case 3:
			out.FiveHour = w
		case 6:
			out.SevenDay = w
		}
	}
	return out, nil
}
func fetchDeepSeekBalance(ctx context.Context, source UsageSource, credential string) (SubscriptionQuota, error) {
	var payload struct {
		BalanceInfos []struct {
			Currency string `json:"currency"`
			Total    string `json:"total_balance"`
		} `json:"balance_infos"`
	}
	endpoint := "https://api.deepseek.com/user/balance"
	if source.BaseURL != "" {
		endpoint = strings.TrimRight(source.BaseURL, "/") + "/user/balance"
	}
	if err := usageJSON(ctx, http.MethodGet, endpoint, "Bearer "+credential, nil, &payload); err != nil {
		return SubscriptionQuota{}, err
	}
	if len(payload.BalanceInfos) == 0 {
		return SubscriptionQuota{}, fmt.Errorf("source reported no balance")
	}
	amount, err := strconv.ParseFloat(payload.BalanceInfos[0].Total, 64)
	if err != nil {
		return SubscriptionQuota{}, err
	}
	return SubscriptionQuota{Balance: money(currencySign(payload.BalanceInfos[0].Currency), amount)}, nil
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
