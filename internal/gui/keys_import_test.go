package gui

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/yetone/magpie/internal/gateway"
	"github.com/yetone/magpie/internal/provider"
)

// keys/import adds the keys pasted at once (361 on Discord), says how many
// were new, and a key the gateway rests says so in its row.
func TestKeysImportAndRest(t *testing.T) {
	h := t.TempDir()
	t.Setenv("HOME", h)
	t.Setenv("USERPROFILE", h)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(h, ".config"))
	t.Setenv("XDG_CACHE_HOME", filepath.Join(h, ".cache"))
	p := provider.Provider{ID: "import-test", Name: "Import", Chat: "https://example.invalid/v1", Key: "first"}
	if err := provider.Save(p); err != nil {
		t.Fatal(err)
	}
	until := time.Now().Add(10 * time.Minute)
	// keys/import API goes through provider.Save which calls normalizeKeys
	// to assign stable IDs — we need those real IDs, not placeholders.
	mux := http.NewServeMux()
	providerRoutes(mux, nil)
	post := func(body string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, httptest.NewRequest("POST", "/api/keys/import", strings.NewReader(body)))
		return w
	}
	w := post(`{"id":"import-test","key":"first\ntwo, three\n\n'four'"}`)
	if w.Code != 200 {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	var imported providersJSON
	if err := json.Unmarshal(w.Body.Bytes(), &imported); err != nil {
		t.Fatal(err)
	}
	if imported.Added != 3 || imported.Had != 1 {
		t.Fatalf("added %d had %d", imported.Added, imported.Had)
	}
	// Now read the actual persisted IDs to match against RestKey.
	fixture, err := provider.Find("import-test")
	if err != nil {
		t.Fatal(err)
	}
	if len(fixture.Keys) != 4 {
		t.Fatalf("fixture should have 4 keys after import, got %d: %v", len(fixture.Keys), fixture.Keys)
	}
	// The key whose secret is "two" — in normalizeKeys order after import,
	// fixture.Keys[i].Key == "two"; build its canonical RestKey = id + "#" + KeyID.
	var twoKeyID string
	for _, k := range fixture.Keys {
		if k.Key == "two" {
			twoKeyID = k.ID
			break
		}
	}
	if twoKeyID == "" {
		t.Fatalf("could not find 'two' key in fixture: %v", fixture.Keys)
	}

	// Patch keyRestOf AFTER the import so it uses exact IDs, not placeholder
	was := keyRestOf
	keyRestOf = func(key string) (gateway.Rest, bool) {
		if key == "import-test#"+twoKeyID {
			return gateway.Rest{Why: "rate", Status: 429, Until: until}, true
		}
		return gateway.Rest{}, false
	}
	defer func() { keyRestOf = was }()

	w = httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest("GET", "/api/providers", nil))

	var st providersJSON
	if err := json.Unmarshal(w.Body.Bytes(), &st); err != nil {
		t.Fatal(err)
	}
	var info *providerJSON
	for i := range st.Providers {
		if st.Providers[i].ID == "import-test" {
			info = &st.Providers[i]
		}
	}
	if info == nil || len(info.KeyList) != 4 {
		t.Fatalf("keys: %+v", info)
	}
	// The second key (index 1) in display order has Rest != nil because
	// RestKey on the imported key whose secret is "two" matches our mock.
	twoDisplayIdx := -1
	for i, k := range info.KeyList {
		if k.ID == twoKeyID {
			twoDisplayIdx = i
			break
		}
	}
	if twoDisplayIdx < 0 {
		t.Fatalf("could not find 'two' key in response KeyList: %+v", info.KeyList)
	}
	for i, k := range info.KeyList {
		if (k.Rest != nil) != (i == twoDisplayIdx) {
			t.Fatalf("key %d rest %+v", i, k.Rest)
		}
	}
	if r := info.KeyList[twoDisplayIdx].Rest; r.Status != 429 || r.Why != "rate" || !r.Until.Equal(until) {
		t.Fatalf("rest %+v", r)
	}
	for _, k := range []string{`"first"`, `"two"`, `"three"`, `"four"`} {
		if strings.Contains(w.Body.String(), `"key":`+k) {
			t.Fatal("leaked a key")
		}
	}
	// pasting only keys it has is refused, saying so
	w = post(`{"id":"import-test","key":"two three"}`)
	if w.Code == 200 || !strings.Contains(w.Body.String(), "already has") {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
}
