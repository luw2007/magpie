package provider

// A provider with a decision API (Provider.Decide) — TypeSafe's System One,
// which Jev answers, or Jev as Vercel's AI Gateway or Cloudflare's Workers
// AI serve it — serves no conversation. Given a message and typed
// questions it answers with a choice and how likely each option was, so a
// routing group can ask it, as a user's turn begins, which of its rules'
// intents the message is and how hard the turn is to think about (see
// gateway/decide.go). Its models are never in the list agents pick from,
// nor members of a group: they are only a group's classifier.

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/yetone/magpie/internal/catalog"
)

// JevLatest is the Jev a decision provider is asked with when the user
// picked none: TypeSafe's latest stable one.
const JevLatest = "jev-latest"

// Decides reports whether the provider is a decision API.
func (p Provider) Decides() bool { return p.Decide != "" }

// The ways a decision API is asked, by where it is (DecideVia): TypeSafe's
// own System One, Vercel's AI Gateway (its evaluation models, with a
// boolean for a noul and probabilities for a confidence), or Workers AI
// (System One's questions and answers, in Cloudflare's envelope and at an
// account's address).
const (
	ViaSystemOne  = "systemone"
	ViaVercel     = "vercel"
	ViaCloudflare = "cloudflare"
)

// DecideVia is the way p's decision API is asked, known by its host or by
// the gateway's own path (Vercel's /v4/ai, Cloudflare's /client/v4).
func (p Provider) DecideVia() string {
	base := strings.TrimRight(p.Decide, "/")
	switch h := HostOf(base); {
	case h == "ai-gateway.vercel.sh" || strings.HasSuffix(base, "/v4/ai"):
		return ViaVercel
	case h == "api.cloudflare.com" || strings.HasSuffix(base, "/client/v4"):
		return ViaCloudflare
	}
	return ViaSystemOne
}

// Jev is the default model a decision provider is asked with: an explicit
// provider model, TypeSafe's latest stable Jev, or the Jev a gateway serves.
func (p Provider) Jev() string {
	if p.DecideModel != "" {
		return p.DecideModel
	}
	switch p.DecideVia() {
	case ViaVercel:
		return "typesafe-ai/jev"
	case ViaCloudflare:
		return "typesafe/jev"
	}
	return JevLatest
}

// decideModels are the models a decision provider offers: the vendor's
// list when fetched, else its explicit model, Jev's aliases, or a gateway's
// one Jev.
func (p Provider) decideModels() []catalog.Model {
	if live, _, ok := catalog.Live(p.ID); ok && len(live) > 0 {
		return live
	}
	if p.DecideModel != "" || p.DecideVia() != ViaSystemOne {
		return []catalog.Model{{ID: p.Jev(), Name: p.Jev()}}
	}
	return []catalog.Model{{ID: JevLatest, Name: "Jev"}, {ID: "jev-preview", Name: "Jev (preview)"}}
}

// DecideURL is where a question for p's decision API is posted. Workers
// AI's is under the account the token belongs to, looked up once.
func (p Provider) DecideURL(ctx context.Context) (string, error) {
	switch p.DecideVia() {
	case ViaVercel:
		return strings.TrimRight(p.Decide, "/") + "/evaluation-model", nil
	case ViaCloudflare:
		acct, err := p.cloudflareAccount(ctx)
		if err != nil {
			return "", err
		}
		return strings.TrimRight(p.Decide, "/") + "/accounts/" + acct + "/ai/run", nil
	}
	return p.Decide + "/systemone", nil
}

// cfAccounts are the Cloudflare accounts found for each token.
var cfAccounts = struct {
	sync.Mutex
	m map[string]string
}{m: map[string]string{}}

// cloudflareAccount is the account p's API token works in: the first it
// can see, which for a token made for one account is that one.
func (p Provider) cloudflareAccount(ctx context.Context) (string, error) {
	cfAccounts.Lock()
	id, ok := cfAccounts.m[p.Decide+"\x00"+p.Key]
	cfAccounts.Unlock()
	if ok {
		return id, nil
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(p.Decide, "/")+"/accounts", nil)
	if err != nil {
		return "", err
	}
	if err := p.Sign(ctx, req, Chat, nil); err != nil {
		return "", err
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("%s: %v", p.Name, err)
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	if res.StatusCode != http.StatusOK {
		return "", fmt.Errorf("%s: %s", p.Name, APIError(b, res.Status))
	}
	var out struct {
		Result []struct {
			ID string `json:"id"`
		} `json:"result"`
	}
	if json.Unmarshal(b, &out) != nil || len(out.Result) == 0 || out.Result[0].ID == "" {
		return "", fmt.Errorf("%s: the API token reaches no account; make one with Workers AI access", p.Name)
	}
	id = out.Result[0].ID
	cfAccounts.Lock()
	cfAccounts.m[p.Decide+"\x00"+p.Key] = id
	cfAccounts.Unlock()
	return id, nil
}

// Deciders are the models of the decision providers ready now, as a
// group's classifier names them.
func Deciders() []Entry {
	var out []Entry
	for _, p := range All() {
		if !p.Decides() || !p.Ready() {
			continue
		}
		for _, m := range p.Exposed() {
			out = append(out, Entry{ID: p.ID + "/" + m.ID, Model: m.ID, Name: m.Name, Provider: p})
		}
	}
	return out
}

// IsDecider reports whether a classifier ("provider/model") is a decision
// provider's model.
func IsDecider(id string) bool {
	p, _, ok := Resolve(id)
	return ok && p.Decides()
}

// fetchDecide lists the models a decision provider's key can use. System
// One servers may return TypeSafe's {"models":[{"name":…}]} or an
// OpenAI-compatible {"data":[{"id":…}]}; gateways are checked by their
// own free call and expose their one Jev.
func (p Provider) fetchDecide(ctx context.Context) ([]catalog.Model, error) {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	switch p.DecideVia() {
	case ViaVercel:
		if err := p.checkKey(ctx, strings.TrimSuffix(strings.TrimRight(p.Decide, "/"), "/v4/ai")+"/v1/credits"); err != nil {
			return nil, err
		}
		return p.decideModels(), nil
	case ViaCloudflare:
		if _, err := p.cloudflareAccount(ctx); err != nil {
			return nil, err
		}
		return p.decideModels(), nil
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.Decide+"/models", nil)
	if err != nil {
		return nil, err
	}
	if err := p.Sign(ctx, req, Chat, nil); err != nil {
		return nil, err
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s: %s", p.Name, APIError(b, res.Status))
	}
	var out struct {
		Models []struct {
			Name string `json:"name"`
		} `json:"models"`
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(b, &out); err != nil {
		return nil, fmt.Errorf("%s: not a model list", p.Name)
	}
	var ms []catalog.Model
	for _, m := range out.Models {
		if m.Name != "" {
			ms = append(ms, catalog.Model{ID: m.Name, Name: m.Name})
		}
	}
	for _, m := range out.Data {
		if m.ID != "" {
			ms = append(ms, catalog.Model{ID: m.ID, Name: m.ID})
		}
	}
	if len(ms) == 0 {
		return nil, fmt.Errorf("%s lists no models", p.Name)
	}
	return ms, catalog.SaveLive(p.ID, p.Decide, ms)
}

// checkKey gets u with p's key, which answers only a key that works.
func (p Provider) checkKey(ctx context.Context, u string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return err
	}
	if err := p.Sign(ctx, req, Chat, nil); err != nil {
		return err
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("%s: %v", p.Name, err)
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	if res.StatusCode != http.StatusOK {
		return fmt.Errorf("%s: %s", p.Name, APIError(b, res.Status))
	}
	return nil
}

// testDecide asks the decision API for its models (or checks its key),
// the one call it answers that costs nothing.
func (p Provider) testDecide(ctx context.Context) []Result {
	t0 := time.Now()
	r := Result{Protocol: "decide", Model: p.Jev()}
	ms, err := p.fetchDecide(ctx)
	r.Millis = time.Since(t0).Milliseconds()
	if err != nil {
		r.Error = err.Error()
		return []Result{r}
	}
	r.OK, r.Status = true, http.StatusOK
	if len(ms) > 0 {
		r.Model = ms[0].ID
	}
	return []Result{r}
}
