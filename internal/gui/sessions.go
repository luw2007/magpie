package gui

import (
	"cmp"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"runtime"
	"strconv"
	"time"

	"github.com/yetone/magpie/internal/agent"
	"github.com/yetone/magpie/internal/proc"
	"github.com/yetone/magpie/internal/sessions"
	"github.com/yetone/magpie/internal/usage"
)

// sessionJSON is a session with what the UI needs to draw its agent.
type sessionJSON struct {
	sessions.Session
	Name string `json:"name"`
	Icon string `json:"icon"`
	// Via: the models the gateway sent its calls to, at the reasoning
	// each was asked for, when it went through magpie
	Via []usage.Via `json:"via,omitempty"`
}

type sessionsJSON struct {
	Sessions []sessionJSON `json:"sessions"`
	// Terminal is set where magpie can open Terminal on the session: the
	// Mac app, not a browser tab that may be on another computer.
	Terminal bool     `json:"terminal"`
	Dirs     []string `json:"dirs"` // where they were read from
}

func sessionRoutes(mux *http.ServeMux, w Windows) {
	mux.HandleFunc("GET /api/sessions", func(rw http.ResponseWriter, r *http.Request) {
		n, _ := strconv.Atoi(r.URL.Query().Get("limit"))
		agents := map[string]*agent.Agent{}
		for _, a := range agent.Clients() {
			agents[a.ID] = a
		}
		out := sessionsJSON{Sessions: []sessionJSON{}, Terminal: runtime.GOOS == "darwin" && !isWeb(w),
			Dirs: []string{}}
		for _, d := range sessions.Dirs() {
			out.Dirs = append(out.Dirs, tilde(d))
		}
		list := sessions.List(n)
		since := time.Now()
		for _, s := range list {
			if !s.Start.IsZero() && s.Start.Before(since) {
				since = s.Start
			}
		}
		vias := usage.Vias(since.Add(-time.Minute))
		for _, s := range list {
			j := sessionJSON{Session: s, Name: s.Agent, Icon: "generic", Via: vias[s.Agent+"|"+s.ID]}
			if a := agents[s.Agent]; a != nil {
				j.Name, j.Icon = a.Name, a.Icon
			}
			j.Path = tilde(j.Path)
			out.Sessions = append(out.Sessions, j)
		}
		writeJSON(rw, out)
	})
	// stats is every session's usage by day over the last ?days=N days
	// (every day when 0 or none), cut by agent, folder and model for the
	// page to filter; names are the agents' as the page shows them.
	mux.HandleFunc("GET /api/sessions/stats", func(rw http.ResponseWriter, r *http.Request) {
		n, _ := strconv.Atoi(r.URL.Query().Get("days"))
		st := sessions.StatsFor(max(0, n))
		names := map[string]string{}
		for _, a := range agent.Clients() {
			names[a.ID] = a.Name
		}
		agents := map[string]string{}
		for _, d := range st.Days {
			for _, u := range d.Usage {
				agents[u.Agent] = cmp.Or(names[u.Agent], u.Agent)
			}
			for _, a := range d.Active {
				agents[a.Agent] = cmp.Or(names[a.Agent], a.Agent)
			}
		}
		writeJSON(rw, struct {
			sessions.Stats
			Agents map[string]string `json:"agents"`
		}{st, agents})
	})
	// terminal opens Terminal on a session's resume command. The command is
	// made here from the session as listed, never taken from the page.
	mux.HandleFunc("POST /api/sessions/terminal", func(rw http.ResponseWriter, r *http.Request) {
		var in struct{ Agent, ID string }
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
			fail(rw, err)
			return
		}
		if runtime.GOOS != "darwin" || isWeb(w) {
			fail(rw, errors.New("opening Terminal is only for the Mac app"))
			return
		}
		s, ok := sessions.Find(in.Agent, in.ID)
		if !ok || s.Resume == "" {
			fail(rw, errors.New("no such session"))
			return
		}
		if err := openTerminal(s.Resume); err != nil {
			fail(rw, err)
			return
		}
		rw.WriteHeader(http.StatusNoContent)
	})
}

// openTerminal runs a command in a new Terminal window, through a .command
// file Terminal opens as it would a double-click: no Automation consent.
// The shell is left open when the agent quits.
func openTerminal(command string) error {
	f, err := os.CreateTemp("", "magpie-resume-*.command")
	if err != nil {
		return err
	}
	script := "#!/bin/sh\n" + command + "\nexec \"${SHELL:-/bin/zsh}\" -l\n"
	_, err = f.WriteString(script)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Chmod(f.Name(), 0o700)
	}
	if err != nil {
		os.Remove(f.Name())
		return err
	}
	if err := proc.Command("open", "-a", "Terminal", f.Name()).Run(); err != nil {
		os.Remove(f.Name())
		return err
	}
	// Terminal has read it long before
	time.AfterFunc(time.Minute, func() { os.Remove(f.Name()) })
	return nil
}
