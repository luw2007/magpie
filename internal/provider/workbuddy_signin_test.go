package provider

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"
)

// wbFakeSignIn serves WorkBuddy's sign-in for one account, uid u1 "Me".
func wbFakeSignIn(t *testing.T) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ok := func(data any) { json.NewEncoder(w).Encode(map[string]any{"code": 0, "data": data}) }
		switch r.URL.Path {
		case "/v2/plugin/auth/state":
			ok(map[string]any{"state": "s1", "authUrl": "https://login.codebuddy.cn/oauth?client_id=c"})
		case "/v2/plugin/auth/token":
			ok(map[string]any{"accessToken": "new-access", "refreshToken": "new-refresh",
				"expiresIn": 3600, "refreshExpiresIn": 7200, "domain": "www.codebuddy.cn", "tokenType": "Bearer"})
		case "/v2/plugin/login/account":
			ok(map[string]any{"uid": "u1", "nickname": "Me"})
		default:
			w.WriteHeader(404)
		}
	}))
	t.Cleanup(srv.Close)
	oldEnd, oldInt := wbEndpoint, wbPollInterval
	wbEndpoint, wbPollInterval = srv.URL, 5*time.Millisecond
	t.Cleanup(func() { wbEndpoint, wbPollInterval = oldEnd, oldInt })
}

func wbSignInNow(t *testing.T) SignInState {
	t.Helper()
	st, err := StartSignIn("workbuddy")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	st, _ = WaitSignIn(ctx, st.ID)
	return st
}

func wbWriteOwn(t *testing.T, home string, access any) {
	t.Helper()
	writeFile(t, wbAuthFile(home), map[string]any{
		"account": map[string]any{"uid": "u1", "nickname": "Me"},
		"auth": map[string]any{"accessToken": access, "refreshToken": "r",
			"expiresAt": time.Now().Add(24 * time.Hour).UnixMilli(), "domain": "www.codebuddy.cn"},
	})
}

// WorkBuddy's own account was seen once (so logins.json remembers it as
// WorkBuddy's own), then WorkBuddy can no longer be read — signed out, or its
// tokens encrypted at rest. Signing that account in from magpie must list
// it: it was taken for WorkBuddy's own and dropped, so the sign-in said done
// and the Providers page showed no WorkBuddy (#155).
func TestWorkBuddySignInOverUnreadableOwn(t *testing.T) {
	for _, gone := range []string{"signed out", "encrypted"} {
		t.Run(gone, func(t *testing.T) {
			home := signIn(t)
			wbTokens.Lock()
			wbTokens.m = map[string]wbCreds{}
			wbTokens.Unlock()
			wbFakeSignIn(t)
			wbWriteOwn(t, home, "own-access")
			if ls := Logins("workbuddy"); len(ls) != 1 || ls[0].User != "Me" {
				t.Fatalf("own not remembered: %+v", ls)
			}
			if gone == "signed out" {
				os.Remove(wbAuthFile(home))
			} else {
				wbWriteOwn(t, home, map[string]any{"$wbEncrypted": 1, "envelope": "x"})
			}
			if _, ok := find(All(), "workbuddy"); ok {
				t.Fatal("listed with no readable account")
			}

			if st := wbSignInNow(t); st.State != "done" || st.User != "Me" {
				t.Fatalf("sign-in: %+v", st)
			}
			p, ok := find(All(), "workbuddy")
			if !ok || p.Account.User != "Me" {
				t.Fatalf("signed in, not listed: %v %+v", ok, p.Account)
			}
			req, _ := http.NewRequest("POST", p.Chat+"/chat/completions", nil)
			if err := p.Sign(context.Background(), req, Chat, []byte(`{}`)); err != nil || req.Header.Get("Authorization") != "Bearer new-access" {
				t.Fatalf("signs with: %v %q", err, req.Header.Get("Authorization"))
			}

			// WorkBuddy readable again as that account: still one of it
			wbWriteOwn(t, home, "own-access")
			if ls := Logins("workbuddy"); len(ls) != 1 || ls[0].User != "Me" || !ls[0].Active {
				t.Fatalf("after WorkBuddy is back: %+v", ls)
			}
			if _, ok := find(All(), "workbuddy"); !ok {
				t.Fatal("not listed after WorkBuddy is back")
			}
		})
	}
}

// Signed in as the account WorkBuddy itself is signed in to and can be
// read: one account, in use, as before.
func TestWorkBuddySignInSameAsOwn(t *testing.T) {
	home := signIn(t)
	wbTokens.Lock()
	wbTokens.m = map[string]wbCreds{}
	wbTokens.Unlock()
	wbFakeSignIn(t)
	wbWriteOwn(t, home, "own-access")
	Logins("workbuddy")
	if st := wbSignInNow(t); st.State != "done" || !st.Using {
		t.Fatalf("sign-in: %+v", st)
	}
	if ls := Logins("workbuddy"); len(ls) != 1 || ls[0].User != "Me" {
		t.Fatalf("logins: %+v", ls)
	}
}
