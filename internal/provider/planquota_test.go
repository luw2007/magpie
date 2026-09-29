package provider

import (
	"context"
	"encoding/json"
	"math"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestPlanQuotaSource(t *testing.T) {
	for _, c := range []struct {
		p        Provider
		url      string
		ok, sure bool
	}{
		{Provider{Chat: "https://open.bigmodel.cn/api/coding/paas/v4", Anthropic: "https://open.bigmodel.cn/api/anthropic"}, "https://open.bigmodel.cn/api/monitor/usage/quota/limit", true, true},
		{Provider{Chat: "https://open.bigmodel.cn/api/paas/v4", Anthropic: "https://open.bigmodel.cn/api/anthropic"}, "https://open.bigmodel.cn/api/monitor/usage/quota/limit", true, false},
		{Provider{Anthropic: "https://api.z.ai/api/anthropic"}, "https://api.z.ai/api/monitor/usage/quota/limit", true, false},
		{Provider{Chat: "https://opencode.ai/zen/go/v1", Anthropic: "https://opencode.ai/zen/go"}, "https://opencode.ai/zen/go/v1/usage", true, true},
		{Provider{Anthropic: "https://opencode.ai/zen/go"}, "https://opencode.ai/zen/go/v1/usage", true, true},
		{Provider{Chat: "https://opencode.ai/zen/v1"}, "", false, false}, // Zen is pay as you go
		{Provider{Chat: "https://api.kimi.com/coding/v1", Anthropic: "https://api.kimi.com/coding"}, "https://api.kimi.com/coding/v1/usages", true, true},
		{Provider{Anthropic: "https://api.kimi.ai/coding/"}, "https://api.kimi.ai/coding/v1/usages", true, true},
		{Provider{Chat: "https://api.commandcode.ai/provider/v1", Anthropic: "https://api.commandcode.ai/provider"}, "https://api.commandcode.ai/alpha/billing/credits", true, false},
		{Provider{Chat: "https://api.deepseek.com"}, "", false, false},
	} {
		src, ok := planQuotaSourceOf(c.p)
		if ok != c.ok || src.url != c.url || src.sure != c.sure {
			t.Errorf("%+v: got %q ok=%v sure=%v", c.p, src.url, ok, src.sure)
		}
	}
}

func TestReadZhipuPlan(t *testing.T) {
	plan, ws, err := readZhipuPlan([]byte(`{"code":200,"success":true,"data":{"level":"pro","limits":[
		{"type":"TOKENS_LIMIT","unit":3,"number":5,"percentage":12,"nextResetTime":1790000000000},
		{"type":"TOKENS_LIMIT","unit":6,"number":7,"percentage":40,"nextResetTime":1790500000000},
		{"type":"TIME_LIMIT","unit":5,"number":1,"percentage":3,"usageDetails":[{"modelCode":"search-prime","usage":2}]}]}}`))
	if err != nil || len(ws) != 3 || plan != "pro" {
		t.Fatalf("%v %q %+v", err, plan, ws)
	}
	if ws[0].Name != "5 hours" || ws[0].Used != 12 || ws[0].Span != 5*time.Hour || !ws[0].ResetsAt.Equal(time.UnixMilli(1790000000000)) {
		t.Errorf("5h: %+v", ws[0])
	}
	if ws[1].Name != "7 days" || ws[1].Used != 40 || ws[1].Span != 7*24*time.Hour {
		t.Errorf("week: %+v", ws[1])
	}
	if ws[2].Name != "MCP · Month" || !ws[2].Aside || ws[2].ResetsAt != nil {
		t.Errorf("mcp: %+v", ws[2])
	}
	if _, _, err := readZhipuPlan([]byte(`{"code":401,"msg":"令牌已过期或验证不正确","success":false}`)); err == nil || err.Error() != "令牌已过期或验证不正确" {
		t.Errorf("a refusal reads as %v", err)
	}
	// a pay-as-you-go key: no windows, which PlanQuotas leaves off the page
	if _, ws, err := readZhipuPlan([]byte(`{"success":true,"data":{"limits":[]}}`)); err != nil || len(ws) != 0 {
		t.Errorf("no plan: %v %+v", err, ws)
	}
}

// cmdCreditsReply is /alpha/billing/credits as command-code 1.66's /usage
// reads it: the credits, the windows beside them (resetAt in ms), and the
// sandbox's minutes, which magpie has no use for.
const cmdCreditsReply = `{"credits":{"planId":"individual-goat-monthly","monthlyCredits":41.2,"purchasedCredits":5,"freeCredits":0},
	"windowLimits":{"limited":true,"fiveHour":{"used":3,"cap":10,"resetAt":1790000000000},"weekly":{"used":12,"cap":40,"resetAt":1790400000000}},
	"sandboxMinutes":{"limitMinutes":0,"usedMinutes":0},"sandboxAccess":false}`

func TestReadCommandCodePlan(t *testing.T) {
	plan, ws, err := readCommandCodePlan([]byte(cmdCreditsReply))
	if err != nil || plan != "GOAT" || len(ws) != 2 {
		t.Fatalf("%v %q %+v", err, plan, ws)
	}
	if ws[0].Name != "5 hours" || ws[0].Used != 30 || ws[0].Span != 5*time.Hour || !ws[0].ResetsAt.Equal(time.UnixMilli(1790000000000)) {
		t.Errorf("5h: %+v", ws[0])
	}
	if ws[1].Name != "Weekly" || ws[1].Used != 30 || ws[1].Span != 7*24*time.Hour || !ws[1].ResetsAt.Equal(time.UnixMilli(1790400000000)) {
		t.Errorf("week: %+v", ws[1])
	}
	// a pay-as-you-go key: no windows, and so no card
	if _, ws, err := readCommandCodePlan([]byte(`{"credits":{"purchasedCredits":20},"windowLimits":null}`)); err != nil || len(ws) != 0 {
		t.Errorf("no plan: %v %+v", err, ws)
	}
	// the keyed preset's balance card is the dollars alone
	if got, err := readCommandCode([]byte(cmdCreditsReply)); err != nil || got != "$46.20" {
		t.Errorf("balance: %q %v", got, err)
	}
}

// TestCommandCodeQuotaShowsWindows: the signed-in plan's card carries its
// windows and not a Balance, which every view shows in their place.
func TestCommandCodeQuotaShowsWindows(t *testing.T) {
	var c cmdCredits
	if err := json.Unmarshal([]byte(cmdCreditsReply), &c); err != nil {
		t.Fatal(err)
	}
	q := cmdQuotaOf(SubscriptionQuota{}, c)
	if q.Balance != "" || q.Plan != "GOAT" || len(q.Windows) != 3 {
		t.Fatalf("%+v", q)
	}
	// the CLI's pool: $70 a month, $41.20 of it left, and $5 bought
	cr := q.Windows[2]
	if cr.Name != "Credits" || cr.Display != "$28.80 / $75.00" || math.Abs(cr.Used-38.4) > 1e-9 || cr.Span != 0 {
		t.Errorf("credits: %+v", cr)
	}
	// no plan and no windows: what is left is all there is to show
	var none cmdCredits
	_ = json.Unmarshal([]byte(`{"credits":{"purchasedCredits":"20","freeCredits":1}}`), &none)
	if q := cmdQuotaOf(SubscriptionQuota{}, none); q.Balance != "$21.00" || len(q.Windows) != 0 {
		t.Errorf("no plan: %+v", q)
	}
}

func TestReadOpenCodeGo(t *testing.T) {
	_, ws, err := readOpenCodeGo([]byte(`{"usage":{
		"rolling":{"status":"ok","percent":0,"resetsAt":"2026-09-25T20:00:00.000Z"},
		"weekly":{"status":"ok","percent":62,"resetsAt":"2026-09-28T00:00:00.000Z"},
		"monthly":{"status":"rate-limited","percent":100,"resetsAt":"2026-10-11T00:00:00.000Z"}}}`))
	if err != nil || len(ws) != 3 {
		t.Fatalf("%v %+v", err, ws)
	}
	if ws[0].Name != "5 hours" || ws[0].Used != 0 || ws[0].ResetsAt != nil {
		t.Errorf("an idle window keeps its placeholder reset: %+v", ws[0])
	}
	if ws[1].Name != "7 days" || ws[1].Used != 62 || ws[1].ResetsAt == nil || ws[1].ResetsAt.Day() != 28 {
		t.Errorf("week: %+v", ws[1])
	}
	if ws[2].Name != "Month" || ws[2].Used != 100 {
		t.Errorf("month: %+v", ws[2])
	}
	if _, _, err := readOpenCodeGo([]byte(`{"rollingUsage":{}}`)); err == nil {
		t.Error("a reply of another shape reads as no windows, silently")
	}
}

func TestPlanWindowsAuth(t *testing.T) {
	var auth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth = r.Header.Get("Authorization")
		if r.URL.Path == "/go" {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		w.Write([]byte(`{"success":true,"data":{"limits":[{"type":"TOKENS_LIMIT","unit":3,"number":5,"percentage":1}]}}`))
	}))
	defer srv.Close()
	_, ws, err := planWindows(context.Background(), planQuotaSource{url: srv.URL + "/z", read: readZhipuPlan}, "k1")
	if err != nil || len(ws) != 1 || auth != "k1" {
		t.Errorf("zhipu: %v %+v auth %q", err, ws, auth)
	}
	_, _, err = planWindows(context.Background(), planQuotaSource{url: srv.URL + "/go", bearer: true, read: readOpenCodeGo}, "k2")
	if err == nil || auth != "Bearer k2" {
		t.Errorf("go: %v auth %q", err, auth)
	}
}

// rewrite sends every request to srv, keeping its path, as if srv were
// the vendor's host.
type rewrite struct{ srv *httptest.Server }

func (r rewrite) RoundTrip(req *http.Request) (*http.Response, error) {
	u := *req.URL
	u.Scheme, u.Host = "http", strings.TrimPrefix(r.srv.URL, "http://")
	req = req.Clone(req.Context())
	req.Header.Set("X-Host", req.URL.Host)
	req.URL, req.Host = &u, ""
	return http.DefaultTransport.RoundTrip(req)
}

// #68: the Usage page shows a GLM Coding Plan's windows and OpenCode Go's,
// a card for each key when a provider has several, and nothing for a
// pay-as-you-go GLM key.
func TestPlanQuotas(t *testing.T) {
	isolate(t)
	h := t.TempDir()
	t.Setenv("HOME", h)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(h, ".config"))
	t.Setenv("XDG_CACHE_HOME", filepath.Join(h, ".cache"))
	t.Setenv("PATH", h)
	for _, v := range []string{"CLAUDE_CONFIG_DIR", "CODEX_HOME"} {
		t.Setenv(v, "")
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key := r.Header.Get("Authorization")
		switch r.Header.Get("X-Host") + r.URL.Path + " " + key {
		case "open.bigmodel.cn/api/monitor/usage/quota/limit glm-a":
			w.Write([]byte(`{"success":true,"data":{"level":"pro","limits":[{"type":"TOKENS_LIMIT","unit":3,"number":5,"percentage":20}]}}`))
		case "open.bigmodel.cn/api/monitor/usage/quota/limit glm-b":
			w.Write([]byte(`{"success":true,"data":{"level":"lite","limits":[{"type":"TOKENS_LIMIT","unit":3,"number":5,"percentage":90}]}}`))
		case "open.bigmodel.cn/api/monitor/usage/quota/limit glm-payg":
			w.Write([]byte(`{"success":true,"data":{"limits":[]}}`))
		case "open.bigmodel.cn/api/biz/subscription/list glm-a":
			w.Write([]byte(`{"code":200,"success":true,"data":[{"productName":"GLM Coding Pro","status":"VALID","autoRenew":1,"nextRenewTime":"2026-10-18 10:00:00"}]}`))
		case "open.bigmodel.cn/api/biz/subscription/list glm-b":
			w.WriteHeader(http.StatusInternalServerError)
		case "opencode.ai/zen/go/v1/usage Bearer go-k":
			w.Write([]byte(`{"usage":{"rolling":{"percent":5,"resetsAt":"2026-09-25T20:00:00Z"}}}`))
		default:
			t.Errorf("asked %s%s with %q", r.Header.Get("X-Host"), r.URL.Path, key)
			w.WriteHeader(http.StatusUnauthorized)
		}
	}))
	defer srv.Close()
	old := http.DefaultClient.Transport
	http.DefaultClient.Transport = rewrite{srv}
	t.Cleanup(func() { http.DefaultClient.Transport = old })

	for _, p := range []Provider{
		{ID: "glm", Name: "GLM", Chat: "https://open.bigmodel.cn/api/coding/paas/v4", Key: "glm-a", KeyName: "work",
			Keys: []KeyAccount{{Key: "glm-a"}, {Key: "glm-b", Name: "home"}, {Key: "glm-off", Off: true}}},
		{ID: "glm-api", Name: "GLM API", Chat: "https://open.bigmodel.cn/api/paas/v4", Key: "glm-payg"},
		{ID: "go", Name: "OpenCode Go", Chat: "https://opencode.ai/zen/go/v1", Key: "go-k"},
		{ID: "zen", Name: "Zen", Chat: "https://opencode.ai/zen/v1", Key: "zen-k"},
	} {
		if err := Save(p); err != nil {
			t.Fatal(err)
		}
	}
	got := map[string]SubscriptionQuota{}
	for _, q := range PlanQuotas(context.Background()) {
		got[q.Provider+"/"+q.User] = q
	}
	if len(got) != 3 {
		t.Fatalf("cards: %+v", got)
	}
	if q := got["glm/work"]; q.Plan != "pro" || len(q.Windows) != 1 || q.Windows[0].Used != 20 ||
		q.Until == nil || q.Until.UTC().Hour() != 2 || q.Renew != "auto" {
		t.Errorf("first key: %+v", q)
	}
	if q := got["glm/home"]; q.Plan != "lite" || q.Windows[0].Used != 90 || q.Until != nil || q.Error != "" {
		t.Errorf("second key: %+v", q)
	}
	if q := got["go/"]; q.Name != "OpenCode Go" || len(q.Windows) != 1 || q.Windows[0].Name != "5 hours" {
		t.Errorf("go: %+v", q)
	}
}

func TestReadKimiCode(t *testing.T) {
	_, ws, err := readKimiCode([]byte(`{"usage":{"limit":"100","used":"12","resetTime":"2026-09-30T05:24:18.443553353Z"},
		"limits":[{"window":{"duration":300,"timeUnit":"TIME_UNIT_MINUTE"},"detail":{"limit":"50","remaining":"40","resetTime":"2026-09-27T09:00:00Z"}},
		          {"window":{"duration":1,"timeUnit":"TIME_UNIT_DAY"},"detail":{"limit":200,"used":200}}]}`))
	if err != nil {
		t.Fatal(err)
	}
	if len(ws) != 3 || ws[0].Name != "5 hours" || ws[0].Used != 20 || ws[0].Span != 5*time.Hour || ws[0].ResetsAt == nil ||
		ws[1].Name != "24 hours" || ws[1].Used != 100 || ws[1].ResetsAt != nil ||
		ws[2].Name != "7 days" || ws[2].Used != 12 || ws[2].ResetsAt == nil || ws[2].ResetsAt.Day() != 30 {
		t.Fatalf("%+v", ws)
	}
	if _, _, err := readKimiCode([]byte(`{"error":{"message":"bad key"}}`)); err == nil {
		t.Fatal("nothing read")
	}
}
