package gateway

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/yetone/magpie/internal/provider"
)

// TestMemberEffortIsWrittenForEveryProtocol exercises the actual gateway
// attempt path rather than the protocol conversion helpers: each member's
// override must reach its vendor in that protocol's native request shape.
func TestMemberEffortIsWrittenForEveryProtocol(t *testing.T) {
	for _, tc := range []struct {
		name, path string
		request    string
		check      func(*testing.T, map[string]any)
	}{
		{"chat", "/v1/chat/completions", `{"model":"group/effort","reasoning_effort":"low","messages":[{"role":"user","content":"hi"}]}`, func(t *testing.T, got map[string]any) {
			if got["reasoning_effort"] != "high" {
				t.Fatalf("chat effort: %#v", got)
			}
		}},
		{"responses", "/v1/responses", `{"model":"group/effort","reasoning":{"effort":"low"},"input":"hi"}`, func(t *testing.T, got map[string]any) {
			r, _ := got["reasoning"].(map[string]any)
			if r["effort"] != "high" {
				t.Fatalf("responses effort: %#v", got)
			}
		}},
		{"anthropic", "/v1/messages", `{"model":"group/effort","max_tokens":32000,"thinking":{"type":"adaptive"},"messages":[{"role":"user","content":"hi"}]}`, func(t *testing.T, got map[string]any) {
			oc, _ := got["output_config"].(map[string]any)
			if oc["effort"] != "high" {
				t.Fatalf("anthropic effort: %#v", got)
			}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fresh(t)
			var got map[string]any
			up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, _ := io.ReadAll(r.Body)
				if err := json.Unmarshal(body, &got); err != nil {
					t.Errorf("upstream body: %v", err)
				}
				w.Header().Set("Content-Type", "application/json")
				switch tc.name {
				case "chat":
					io.WriteString(w, `{"id":"ok","choices":[{"message":{"role":"assistant","content":"ok"}}]}`)
				case "responses":
					io.WriteString(w, `{"id":"ok","output":[]}`)
				default:
					io.WriteString(w, `{"id":"ok","type":"message","role":"assistant","content":[]}`)
				}
			}))
			defer up.Close()
			p := provider.Provider{ID: "effort", Name: "Effort", Key: "k", Models: []string{"m"}}
			switch tc.name {
			case "chat":
				p.Chat = up.URL + "/v1"
			case "responses":
				p.Responses = up.URL + "/v1"
			default:
				p.Anthropic = up.URL
			}
			if err := provider.Save(p); err != nil {
				t.Fatal(err)
			}
			if err := provider.SaveGroup(provider.Group{ID: "effort", Members: []string{"effort/m"}, MemberEfforts: map[string]string{"effort/m": "high"}}); err != nil {
				t.Fatal(err)
			}
			s := New()
			rec := httptest.NewRecorder()
			s.Handler().ServeHTTP(rec, httptest.NewRequest("POST", tc.path, strings.NewReader(tc.request)))
			if rec.Code != http.StatusOK {
				t.Fatalf("status %d: %s", rec.Code, rec.Body)
			}
			tc.check(t, got)
		})
	}
}

func TestMemberEffortChangesOnFallback(t *testing.T) {
	fresh(t)
	var bodies []map[string]any
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var got map[string]any
		_ = json.Unmarshal(body, &got)
		bodies = append(bodies, got)
		w.Header().Set("Content-Type", "application/json")
		if len(bodies) == 1 {
			w.WriteHeader(http.StatusTooManyRequests)
			io.WriteString(w, `{"error":{"message":"limited"}}`)
			return
		}
		io.WriteString(w, `{"id":"ok","choices":[{"message":{"role":"assistant","content":"ok"}}]}`)
	}))
	defer up.Close()
	for _, id := range []string{"first", "second"} {
		if err := provider.Save(provider.Provider{ID: id, Name: id, Key: "k", Chat: up.URL + "/v1", Models: []string{"m"}}); err != nil {
			t.Fatal(err)
		}
	}
	if err := provider.SaveGroup(provider.Group{ID: "fallback-effort", Members: []string{"first/m", "second/m"}, MemberEfforts: map[string]string{"first/m": "high", "second/m": "xhigh"}}); err != nil {
		t.Fatal(err)
	}
	s := New()
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(`{"model":"group/fallback-effort","reasoning_effort":"low","messages":[{"role":"user","content":"hi"}]}`)))
	if rec.Code != http.StatusOK || len(bodies) != 2 {
		t.Fatalf("status %d, attempts %d: %s", rec.Code, len(bodies), rec.Body)
	}
	if bodies[0]["reasoning_effort"] != "high" || bodies[1]["reasoning_effort"] != "xhigh" {
		t.Fatalf("fallback efforts: %#v", bodies)
	}
}
