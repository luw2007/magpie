package gui

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/yetone/magpie/internal/provider"
)

func TestProviderSaveActionEnvelope(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	mux := http.NewServeMux()
	providerRoutes(mux, nil)
	post := func(body map[string]any) {
		t.Helper()
		b, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, httptest.NewRequest("POST", "/api/provider/save", strings.NewReader(string(b))))
		if w.Code != http.StatusOK {
			t.Fatalf("save: %d %s", w.Code, w.Body.String())
		}
	}
	post(map[string]any{"id": "envelope", "name": "Original", "chat": "http://127.0.0.1:1/v1/chat/completions", "key": "original-key", "balanceToken": "saved-token", "new": true})
	post(map[string]any{"id": "envelope", "name": "Second", "chat": "http://127.0.0.1:1/v1/chat/completions", "key": "second-key", "new": true})
	original, err := provider.Find("envelope")
	if err != nil {
		t.Fatal(err)
	}
	if original.Name != "Original" || original.Key != "original-key" {
		t.Fatalf("adding replaced original: %+v", original)
	}
	var second *provider.Provider
	for _, p := range provider.All() {
		if p.Name == "Second" {
			copy := p
			second = &copy
			break
		}
	}
	if second == nil || second.ID == original.ID || second.Key != "second-key" {
		t.Fatalf("second provider was not independently added: %+v", second)
	}
	post(map[string]any{"id": original.ID, "name": original.Name, "chat": original.Chat})
	retained, err := provider.Find(original.ID)
	if err != nil {
		t.Fatal(err)
	}
	if retained.BalanceToken != "saved-token" {
		t.Fatalf("blank token lost saved credential: %q", retained.BalanceToken)
	}
	post(map[string]any{"id": original.ID, "name": original.Name, "chat": original.Chat, "clearBalanceToken": true})
	cleared, err := provider.Find(original.ID)
	if err != nil {
		t.Fatal(err)
	}
	if cleared.BalanceToken != "" {
		t.Fatalf("explicit clearing ignored: %q", cleared.BalanceToken)
	}
	if cleared.Key != "original-key" {
		t.Fatalf("clearing balance credential changed API key: %q", cleared.Key)
	}
}
