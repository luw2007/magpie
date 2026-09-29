package gateway

import (
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/yetone/magpie/internal/provider"
)

// WorkBuddy's stream goes on sending an empty reasoning_content with the
// text once thinking is over; it reaches the agent without it, the
// thinking and the rest as they came.
func TestChatRelayDropsEmptyReasoning(t *testing.T) {
	fresh(t)
	up := sse(`data: {"id":"c1","object":"chat.completion.chunk","choices":[{"index":0,"delta":{"role":"assistant","reasoning_content":"The user said hi."}}]}`,
		`data: {"id":"c1","object":"chat.completion.chunk","choices":[{"index":0,"delta":{"content":"Hi","reasoning_content":""}}]}`,
		`data: {"id":"c1","object":"chat.completion.chunk","choices":[{"index":0,"delta":{"content":"!","reasoning_content":""}}]}`,
		`data: {"id":"c1","object":"chat.completion.chunk","choices":[{"index":0,"delta":{"reasoning_content":""},"finish_reason":"stop"}],"usage":{"prompt_tokens":3,"completion_tokens":2,"total_tokens":5}}`,
		`data: [DONE]`)
	a := &scripted{replies: []reply{{200, "text/event-stream", up}}}
	scriptedOn(t, "a", provider.Chat, a)
	rec := httptest.NewRecorder()
	New().Handler().ServeHTTP(rec, httptest.NewRequest("POST", "/v1/chat/completions",
		strings.NewReader(`{"model":"a/m","stream":true,"messages":[{"role":"user","content":"hi"}]}`)))
	body := rec.Body.String()
	if rec.Code != 200 || strings.Contains(body, `"reasoning_content":""`) ||
		!strings.Contains(body, `"reasoning_content":"The user said hi."`) ||
		!strings.Contains(body, `"content":"Hi"`) || !strings.Contains(body, `"content":"!"`) ||
		!strings.Contains(body, `"finish_reason":"stop"`) || !strings.Contains(body, `"total_tokens":5`) ||
		!strings.HasSuffix(strings.TrimSpace(body), "data: [DONE]") {
		t.Fatalf("%d %s", rec.Code, body)
	}
}

// Lines split across reads come out whole; one without an empty
// reasoning_content comes out byte for byte.
func TestChatTidySplitLines(t *testing.T) {
	in := "data: {\"choices\":[{\"delta\":{\"content\":\"a\",\"reasoning_content\":\"\"}}]}\n\n" +
		"data: {\"choices\":[{\"delta\":{\"content\":\"b\"}}],  \"x\":1}\r\n\r\ndata: [DONE]"
	var tidy chatTidy
	var out []byte
	for i := 0; i < len(in); i += 7 {
		out = append(out, tidy.write([]byte(in[i:min(i+7, len(in))]))...)
	}
	out = append(out, tidy.flush()...)
	want := "data: {\"choices\":[{\"delta\":{\"content\":\"a\"}}]}\n\n" +
		"data: {\"choices\":[{\"delta\":{\"content\":\"b\"}}],  \"x\":1}\r\n\r\ndata: [DONE]"
	if string(out) != want {
		t.Fatalf("got %q", out)
	}
}
