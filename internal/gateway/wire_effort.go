package gateway

import (
	"context"

	"github.com/tidwall/gjson"

	"github.com/yetone/magpie/internal/provider"
)

// thinkingBackOn is req with the thinking off it asked for taken back, for
// a model that refused it: at the least the model has, as a request asking
// none was sent before thinking could be turned off (#1636).
func thinkingBackOn(p provider.Provider, model string, req *Request) *Request {
	r := *req
	r.ThinkOff = false
	if r.Effort == "" || offEffort(r.Effort) {
		r.Effort = onEffort(p, model)
	}
	return &r
}

// wireAt is where a request leaves the reasoning it went out at, for the
// log (wireEffortOf) and the reply's X-Magpie-Model; set says it was.
type wireAt struct {
	effort string
	set    bool
	// own: the request is built in the words of its API's own levels and
	// budgets (Gemini's), which the log leaves as asked
	own bool
	// model: the latest Auto pick, retained when a request is built again
	model string
	// tier: the service tier the try's request went out at, whichever
	// path sent it (noteWireTier)
	tier string
	// onEffort, when set, is told each level a request goes out at
	// (noteWireBody), for the reply's X-Magpie-Model (noteMember)
	onEffort func(effort string)
}

type wireAtKey struct{}

// withWireAt is ctx carrying w, which forwardOnce fills.
func withWireAt(ctx context.Context, w *wireAt) context.Context {
	return context.WithValue(ctx, wireAtKey{}, w)
}

// noteWireBody leaves, where the request's context has a place for it, the
// reasoning the body goes out at: the body forwardOnce is about to send,
// after every shaping of it for the provider and the user's own fields.
func noteWireBody(ctx context.Context, to provider.Protocol, body []byte) {
	w, _ := ctx.Value(wireAtKey{}).(*wireAt)
	if w == nil || w.own {
		return
	}
	if e, ok := wireEffort(to, body); ok {
		w.effort, w.set = e, true
		if w.onEffort != nil {
			w.onEffort(e)
		}
	}
}

// wireEffort is the reasoning a body asks its model for, in the words the
// log uses: what the client asked can go as another level once built —
// effort none and minimal read as low where the model's levels don't say
// it can stop (effortOf), thinking turned off goes as off to Anthropic's
// API, a budget as the level it is nearest — and the log said the one
// asked while another was sent (#1636). Anthropic's output_config effort
// alone, with no thinking, asks for none. false for an API the log leaves
// as asked (Gemini's levels and budgets).
func wireEffort(to provider.Protocol, body []byte) (string, bool) {
	switch to {
	case provider.Anthropic:
		switch gjson.GetBytes(body, "thinking.type").String() {
		case "disabled", "between_tools":
			return "none", true
		}
		return requestEffort(to, body), true
	case provider.Chat, provider.Responses:
		return bodyEffort(to, body), true
	}
	return "", false
}
