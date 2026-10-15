package gui

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yetone/magpie/internal/settings"
)

// The page width is saved on its own, offered only as the widths the Settings
// row has, left as it is by a save of the other settings (an older page never
// sends it), told to the page before it paints, and never replaced by sync:
// it is this computer's, a screen's.
func TestPageWidthSetting(t *testing.T) {
	h := t.TempDir()
	t.Setenv("HOME", h)
	t.Setenv("USERPROFILE", h)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(h, ".config"))
	t.Setenv("XDG_CACHE_HOME", filepath.Join(h, ".cache"))
	srv := Handler(nil, nil)
	post := func(path, body string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		srv.ServeHTTP(rec, httptest.NewRequest("POST", path, strings.NewReader(body)))
		return rec
	}
	if got := settings.Load().PageWidth; got != 0 {
		t.Fatalf("a page that never chose has %d, not 0 (the default 1200)", got)
	}
	rec := post("/api/settings/page-width", `{"width":1600}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	var got struct{ PageWidth int }
	if json.Unmarshal(rec.Body.Bytes(), &got); got.PageWidth != 1600 || settings.Load().PageWidth != 1600 {
		t.Fatalf("answered %d, saved %d", got.PageWidth, settings.Load().PageWidth)
	}
	for _, bad := range []string{`{"width":1000}`, `{"width":-2}`, `{"width":99999}`} {
		if rec := post("/api/settings/page-width", bad); rec.Code == http.StatusOK || settings.Load().PageWidth != 1600 {
			t.Fatalf("%s was taken: %d, saved %d", bad, rec.Code, settings.Load().PageWidth)
		}
	}
	if rec := post("/api/settings", `{"theme":"dark","lang":"en"}`); rec.Code != http.StatusOK || settings.Load().PageWidth != 1600 {
		t.Fatalf("the Settings page's save changed the page width: %d %d", rec.Code, settings.Load().PageWidth)
	}
	rec = httptest.NewRecorder()
	srv.ServeHTTP(rec, httptest.NewRequest("GET", "/boot.js", nil))
	if !strings.Contains(rec.Body.String(), `"pageWidth":1600`) {
		t.Fatalf("boot.js: %s", rec.Body)
	}
	// no limit is -1, and 0 is back to the default
	for _, w := range []int{-1, 0} {
		b, _ := json.Marshal(map[string]int{"width": w})
		if rec := post("/api/settings/page-width", string(b)); rec.Code != http.StatusOK || settings.Load().PageWidth != w {
			t.Fatalf("width %d: %d, saved %d", w, rec.Code, settings.Load().PageWidth)
		}
	}
	// sync brings in another computer's settings: this one's width stays
	if err := settings.Save(settings.Settings{PageWidth: 2000}); err != nil {
		t.Fatal(err)
	}
	in := settings.Settings{Theme: "dark", PageWidth: 1200}
	in.KeepOwn(settings.Load())
	if in.PageWidth != 2000 {
		t.Fatalf("sync replaced the page width: %d", in.PageWidth)
	}
}
