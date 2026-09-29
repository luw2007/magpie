package gateway

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/yetone/magpie/internal/provider"
)

// xaiUndecryptable is what xAI answers — a Grok API key or a SuperGrok
// account alike — for reasoning another account or vendor sealed.
const xaiUndecryptable = "Could not decrypt the provided encrypted_content. Ensure the value is the unmodified encrypted_content from a previous response."

// sealer is a Grok-like Responses upstream: it seals its reasoning as
// "sealed-by-<name>" and can read only its own.
type sealer struct {
	name   string
	down   bool // answering 429, for the conversation to move on
	stream bool // refusing in the stream (response.failed), not as a 400
	mu     sync.Mutex
	got    [][]string // the sealed reasoning each request carried
}

func (s *sealer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	b, _ := io.ReadAll(r.Body)
	var q struct {
		Input []sealedItem `json:"input"`
	}
	json.Unmarshal(b, &q)
	var got []string
	foreign := false
	for _, it := range q.Input {
		if it.Type == "reasoning" {
			got = append(got, it.Enc)
			foreign = foreign || it.Enc != "sealed-by-"+s.name
		}
	}
	s.mu.Lock()
	s.got = append(s.got, got)
	s.mu.Unlock()
	switch {
	case s.down:
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(429)
		io.WriteString(w, `{"error":{"message":"slow down"}}`)
		return
	case foreign && !s.stream:
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(400)
		io.WriteString(w, `{"code":"Client specified an invalid argument","error":"`+xaiUndecryptable+`"}`)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	if foreign {
		io.WriteString(w, sse(
			`data: {"type":"response.created","response":{"id":"r0","model":"grok-4"}}`,
			`data: {"type":"response.failed","response":{"id":"r0","error":{"code":"invalid_request_error","message":"`+xaiUndecryptable+`"}}}`))
		return
	}
	io.WriteString(w, sse(
		`data: {"type":"response.created","response":{"id":"r1","model":"grok-4"}}`,
		`data: {"type":"response.output_item.done","output_index":0,"item":{"type":"reasoning","id":"rs_1","summary":[],"encrypted_content":"sealed-by-`+s.name+`"}}`,
		`data: {"type":"response.output_text.delta","delta":"from `+s.name+`"}`,
		`data: {"type":"response.completed","response":{"id":"r1","usage":{"input_tokens":7,"output_tokens":1}}}`))
}

func (s *sealer) sent() [][]string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := s.got
	s.got = nil
	return out
}

// Switching a Codex conversation between two Grok accounts of a routing
// group mid-way (waroy): the second can't decrypt the first's sealed
// reasoning. It's asked again without it, and later turns leave out what
// it refused while its own sealed reasoning still goes along.
func TestForeignSealAcrossGroupMembers(t *testing.T) {
	for _, inStream := range []bool{false, true} {
		fresh(t)
		refusedSeals.Lock()
		refusedSeals.m = map[string]sealsRefused{}
		refusedSeals.Unlock()
		a, b := &sealer{name: "a"}, &sealer{name: "b", stream: inStream}
		for _, u := range []struct {
			id string
			h  http.Handler
		}{{"grok1", a}, {"grok2", b}} {
			up := httptest.NewServer(u.h)
			t.Cleanup(up.Close)
			if err := provider.Save(provider.Provider{ID: u.id, Name: "Grok (SuperGrok)", Key: "k-" + u.id, Models: []string{"grok-4"}, Responses: up.URL + "/v1"}); err != nil {
				t.Fatal(err)
			}
		}
		if err := provider.SaveGroup(provider.Group{Name: "Grok", Members: []string{"grok1/grok-4", "grok2/grok-4"}, Routing: provider.Ordered}); err != nil {
			t.Fatal(err)
		}
		s := New()
		turn := func(items ...string) (int, string) {
			t.Helper()
			in := `{"type":"message","role":"user","content":[{"type":"input_text","text":"count the lines"}]}`
			for _, it := range items {
				in += "," + it
			}
			rec := httptest.NewRecorder()
			req := httptest.NewRequest("POST", "/v1/responses", strings.NewReader(`{"model":"group/grok","stream":true,"input":[`+in+`]}`))
			req.Header.Set("session_id", "thread-1")
			s.Handler().ServeHTTP(rec, req)
			return rec.Code, rec.Body.String()
		}
		reasoning := func(by string) string {
			return `{"type":"reasoning","id":"rs_` + by + `","summary":[],"encrypted_content":"sealed-by-` + by + `"}`
		}
		call := func(id string) string {
			return `{"type":"function_call","call_id":"` + id + `","name":"shell","arguments":"{}"},{"type":"function_call_output","call_id":"` + id + `","output":"ok"}`
		}

		// a answers, and reads back what it sealed
		if code, body := turn(); code != 200 || !strings.Contains(body, "from a") {
			t.Fatalf("stream=%v first: %d %s", inStream, code, body)
		}
		if code, body := turn(reasoning("a"), call("c1")); code != 200 || !strings.Contains(body, "from a") {
			t.Fatalf("stream=%v second: %d %s", inStream, code, body)
		}
		if got := a.sent(); len(got) != 2 || strings.Join(got[1], ",") != "sealed-by-a" {
			t.Fatalf("stream=%v a was sent %q", inStream, got)
		}

		// a goes down: b takes the conversation over, without a's seal
		a.down = true
		code, body := turn(reasoning("a"), call("c1"), reasoning("a"), call("c2"))
		if code != 200 || !strings.Contains(body, "from b") {
			t.Fatalf("stream=%v after the switch: %d %s", inStream, code, body)
		}
		if got := b.sent(); len(got) != 2 || len(got[0]) != 2 || len(got[1]) != 0 {
			t.Fatalf("stream=%v b was sent %q", inStream, got)
		}

		// the next turn doesn't hit it again: a's seal is left out up front,
		// b's own goes along
		code, body = turn(reasoning("a"), call("c1"), reasoning("a"), call("c2"), reasoning("b"), call("c3"))
		if code != 200 || !strings.Contains(body, "from b") {
			t.Fatalf("stream=%v next turn: %d %s", inStream, code, body)
		}
		if got := b.sent(); len(got) != 1 || strings.Join(got[0], ",") != "sealed-by-b" {
			t.Fatalf("stream=%v b was then sent %q", inStream, got)
		}
		a.sent()
	}
}

// Each vendor's words for reasoning it can't read are known; a request
// at fault some other way is not taken for one.
func TestForeignReasoningWords(t *testing.T) {
	for _, m := range []string{
		xaiUndecryptable,
		`{"error":{"message":"The encrypted content for item rs_1 could not be verified.","code":"invalid_encrypted_content"}}`,
		"Grok (SuperGrok): Could not decrypt the provided encrypted_content.",
	} {
		if !foreignReasoning.MatchString(m) {
			t.Errorf("not matched: %s", m)
		}
	}
	for _, m := range []string{"Invalid value for reasoning.effort", "model not found", "could not parse the JSON body"} {
		if foreignReasoning.MatchString(m) {
			t.Errorf("matched: %s", m)
		}
	}
}
