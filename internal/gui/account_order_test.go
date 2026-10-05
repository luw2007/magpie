package gui

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/yetone/magpie/internal/provider"
)

func TestAccountArrangeRouteAndEditorSave(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	// Canonical fixture: normalizeKeys prepends Key → Keys[0] if not already
	// present, so primary ends up as Keys[0] (the runtime-selected key),
	// second ends up as Keys[1].
	p := provider.Provider{ID: "arrange-test", Name: "Arrange", Chat: "https://example.invalid/v1",
		Key: "primary", Keys: []provider.KeyAccount{{Key: "second"}}}
	if err := provider.Save(p); err != nil {
		t.Fatal(err)
	}
	fixture, _ := provider.Find("arrange-test")
	// fixture.Keys after normalizeKeys: [primary, second] order
	primaryID := fixture.Keys[0].ID   // the Key runtime-selection
	secondID := fixture.Keys[1].ID
	mux := http.NewServeMux()
	providerRoutes(mux, nil)
	// Arrange orders second first, primary last
	order := []string{secondID, primaryID}
	post := func(action string, body any) *httptest.ResponseRecorder {
		b, _ := json.Marshal(body)
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, httptest.NewRequest("POST", "/api/provider/"+action, strings.NewReader(string(b))))
		return w
	}
	w := post("arrange", map[string]any{"id": p.ID, "accountOrder": order})
	if w.Code != 200 {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	var state providersJSON
	if err := json.Unmarshal(w.Body.Bytes(), &state); err != nil {
		t.Fatal(err)
	}
	if len(state.Providers) == 0 || state.Providers[0].KeyList[0].ID != order[0] || !state.Providers[0].KeyList[0].Active {
		t.Fatal("arrange response must include the new First key")
	}
	if strings.Contains(w.Body.String(), `"key":"primary"`) || strings.Contains(w.Body.String(), `"key":"second"`) {
		t.Fatal("leaked key")
	}
	// Saving an older editor form must not erase the native key arrangement.
	w = post("save", map[string]any{"id": p.ID, "name": p.Name, "chat": p.Chat})
	if w.Code != 200 {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	got, err := provider.Find(p.ID)
	if err != nil {
		t.Fatalf("find after save: %v", err)
	}
	// Keys now in arranged order: secondID first (the runtime KeyID),
	// primaryID second (also known as got.Keys[1].ID).
	if !reflect.DeepEqual([]string{got.KeyID, got.Keys[1].ID}, order) {
		t.Fatalf("lost order: got %v (KeyID=%s, Keys=%v)", []string{got.KeyID, got.Keys[1].ID}, got.KeyID, got.Keys)
	}
	info := providerInfo(*got, nil)
	if info.KeyList[0].ID != order[0] || !info.KeyList[0].Active {
		t.Fatal("displayed first key does not match routing")
	}
	if w = post("arrange", map[string]any{"id": p.ID, "accountOrder": []string{"missing"}}); w.Code == 200 {
		t.Fatal("accepted stale order")
	}
}
