package agent

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
	"unicode/utf16"

	"github.com/yetone/magpie/internal/catalog"
	"github.com/yetone/magpie/internal/edit"
	"github.com/yetone/magpie/internal/gateway"
	"github.com/yetone/magpie/internal/proc"
	"github.com/yetone/magpie/internal/provider"
)

// Codex installed in a WSL distro reads its config there, not in Windows'
// home — so does the Codex app's WSL connection. On Windows magpie lists the
// distros (wsl.exe -l -q), asks each running one once for its $HOME and
// whether Codex is there (never starting one that is stopped), and edits
// the files through \\wsl.localhost\<distro>. Each is an agent of its own,
// codex@wsl:<distro>. The gateway it is pointed at is
// 127.0.0.1 when WSL shares Windows' network (networkingMode=mirrored in
// .wslconfig); under NAT it is Windows as WSL sees it, which reaches the
// gateway only while that listens beyond loopback.

// place is where an agent lives: its home as magpie opens it, how a path
// there is spelt in the agent's own config, and the gateway as it reaches
// it. here(home) is this machine's.
type place struct {
	home  string
	id    string              // the agent's id when not its own: its stash keys go under it
	spell func(string) string // a path under home as the agent names it; nil: as is
	base  func() string       // the gateway's URL from the agent; nil: gateway.URL
}

func here(home string) place { return place{home: home} }

func (p place) gw() string {
	if p.base != nil {
		return p.base()
	}
	return gateway.URL()
}

func (p place) v1() string       { return p.gw() + "/v1" }
func (p place) codexURL() string { return p.gw() + gateway.CodexPath }

// host is the gateway's host as the agent reaches it.
func (p place) host() string {
	if p.base == nil {
		return "127.0.0.1"
	}
	h, _, err := net.SplitHostPort(strings.TrimPrefix(p.base(), "http://"))
	if err != nil {
		return "127.0.0.1"
	}
	return h
}

// native is a path under home as the agent names it in its config.
func (p place) native(path string) string {
	if p.spell == nil {
		return path
	}
	return p.spell(path)
}

// key is a stash key of this machine's agent ("codex.model") as this
// place's agent's.
func (p place) key(k string) string {
	if p.id == "" {
		return k
	}
	_, rest, _ := strings.Cut(k, ".")
	return p.id + "." + rest
}

// distro is one WSL distro, as probed.
type distro struct {
	Name     string            `json:"name"`
	Home     string            `json:"home"`              // $HOME inside it, e.g. /home/me
	Root     string            `json:"root"`              // where magpie opens its / from, e.g. \\wsl.localhost\Ubuntu
	Has      map[string]bool   `json:"has"`               // "dir:.codex", "bin:codex": what the probe found
	Gateway  string            `json:"gateway,omitempty"` // the Windows host as the distro reaches it, when not mirrored
	Values   map[string]string `json:"values,omitempty"`  // Codex's fields as last read, shown while it is stopped
	Mirrored bool              `json:"-"`
	Running  bool              `json:"-"`
}

// local is a path inside the distro as magpie opens it.
func (d distro) local(linux string) string {
	return d.Root + strings.ReplaceAll(linux, "/", string(filepath.Separator))
}

// native is a path magpie opens as the distro spells it.
func (d distro) native(local string) string {
	rel := local
	if len(local) >= len(d.Root) && strings.EqualFold(local[:len(d.Root)], d.Root) {
		rel = local[len(d.Root):]
	}
	rel = strings.ReplaceAll(rel, `\`, "/")
	if !strings.HasPrefix(rel, "/") {
		rel = "/" + rel
	}
	return rel
}

// base is the gateway's URL from inside the distro.
func (d distro) base() string {
	if d.Mirrored || d.Gateway == "" {
		return gateway.URL()
	}
	return "http://" + net.JoinHostPort(d.Gateway, gateway.Port())
}

func (d distro) place(id string) place {
	return place{home: d.local(d.Home), id: id, spell: d.native, base: d.base}
}

// wslCodex is Codex in a distro: Codex's own reading and writing, at the
// distro's home, with the distro's way to the gateway. A distro that isn't
// running is shown as magpie last saw it, and nothing of it is read: any
// access to its files starts it. Picking a value starts it, as asked.
func wslCodex(d distro) *Agent {
	id := "codex@wsl:" + d.Name
	a := codexIn(d.place(id))
	a.ID, a.Name, a.Aliases, a.Bin, a.UA, a.WSL = id, "Codex · WSL "+d.Name, nil, "", nil, d.Name
	a.detect = func() bool { return d.Has["dir:.codex"] || d.Has["bin:codex"] }
	// its requests carry Codex's User-Agent and are counted as Codex's
	// on Windows, so a prompt with none of "its" own seen isn't a bypass
	a.LastUsed = nil
	a.Notice = func() string {
		if !d.Mirrored {
			return "WSL " + d.Name + " isn't in mirrored networking, so its Codex can't reach magpie on 127.0.0.1 and was pointed at Windows (" + d.base() +
				"), which answers only while the gateway listens beyond loopback and Windows' firewall lets WSL in. " +
				"Set networkingMode=mirrored under [wsl2] in %UserProfile%\\.wslconfig and run wsl --shutdown, then pick the model again."
		}
		return "Codex in WSL " + d.Name + " builds its model list at start-up — restart it (and the Codex app's WSL connection) to see this."
	}
	if !d.Running {
		return asleep(a, d)
	}
	if reached := a.Reached; reached != nil {
		a.Reached = func(since time.Time) (time.Time, string, bool) {
			at, to, refused := reached(since)
			if sameHost(to, d.base()) {
				to = gateway.URL() // the gateway, however WSL reaches it
			}
			return at, to, refused
		}
	}
	for i := range a.Fields {
		f := &a.Fields[i]
		get, key := f.Get, f.Key
		f.Get = func() string { v := get(); wslRemember(d.Name, key, v); return v }
	}
	// what a set leaves is kept at once, in case the distro stops before
	// the next look
	for i := range a.Fields {
		f := &a.Fields[i]
		if set := f.Set; set != nil {
			f.Set = func(v string) error {
				err := set(v)
				for _, g := range a.Fields {
					g.Get()
				}
				wslSave()
				return err
			}
		}
	}
	return a
}

// asleep is a stopped distro's Codex: its fields read what magpie last saw,
// their options come from magpie alone, and it has no files to check, sync
// or migrate (Path and Dir are empty); setting a field goes to the files,
// which starts the distro.
func asleep(live *Agent, d distro) *Agent {
	started := false
	a := &Agent{ID: live.ID, Name: live.Name, Icon: live.Icon, WSL: d.Name, detect: live.detect,
		Notice: func() string {
			if started {
				return live.Notice()
			}
			return "WSL " + d.Name + " isn't running: magpie shows what it last saw there, and starts it only to change something."
		}}
	own := func() []Option { return group("OpenAI", options(ownCodex(), "")) }
	for _, lf := range live.Fields {
		key, set := lf.Key, lf.Set
		f := Field{Key: key, Label: lf.Label, Quiet: lf.Quiet, Options: lf.Options,
			Get: func() string { return wslLastSeen(d.Name, key) },
			Set: func(v string) error {
				// opening a stopped distro's files is aborted rather than
				// waiting for it to start, so it is started first
				if _, err := wslRun(time.Minute, "-d", d.Name, "-e", "true"); err != nil {
					return fmt.Errorf("start WSL %s: %w", d.Name, err)
				}
				started = true
				err := set(v)
				// a model settles the effort too
				for _, f := range live.Fields {
					wslRemember(d.Name, f.Key, f.Get())
				}
				wslSave()
				return err
			}}
		switch key {
		case "model", "subagent":
			f.Options = func(map[string]string) []Option { return append(own(), viaMagpieFor("codex", "")...) }
		case "effort":
			f.Options = func(cur map[string]string) []Option {
				if e := catalog.Efforts(append(catalog.Codex(), magpieModels("codex")...), cur["model"]); len(e) > 0 {
					return static(e...)
				}
				return static("low", "medium", "high", "xhigh")
			}
		}
		a.Fields = append(a.Fields, f)
	}
	return a
}

// wslAgents are the agents in this machine's WSL distros; none off Windows.
func wslAgents() []*Agent {
	if runtime.GOOS != "windows" {
		return nil
	}
	return wslAgentsOf(wslDistros())
}

func wslAgentsOf(ds []distro) []*Agent {
	var out []*Agent
	for _, d := range ds {
		// only where Codex is: magpie writes nothing into a distro without it
		if a := wslCodex(d); a.Detected() {
			out = append(out, a)
		}
	}
	return out
}

// Only running distros are probed — asking one anything starts it — once
// each in a process, and again only after a probe that failed. Those Codex
// was found in are kept in wsl.json beside magpie's settings (not the
// stash, which profiles and backups carry), with their fields as last
// read, so a stopped one is listed without being started.
var wsl struct {
	sync.Mutex
	at      time.Time
	names   []string        // every distro installed
	running map[string]bool // those running
	listed  bool            // names is a real answer, and may forget distros
	seen    map[string]*distro
	probed  map[string]bool
	failed  map[string]time.Time
	dirty   bool
}

const (
	wslListAge  = time.Minute
	wslRetryAge = 10 * time.Minute
)

// wslRun runs wsl.exe; a var for tests.
var wslRun = func(timeout time.Duration, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := proc.CommandContext(ctx, "wsl.exe", args...)
	// wsl.exe speaks UTF-16 unless told otherwise; either is read
	cmd.Env = append(os.Environ(), "WSL_UTF8=1")
	return cmd.Output()
}

// wslRoot is where magpie opens a running distro's / from; a var for tests.
var wslRoot = func(name string) string {
	if _, err := os.Stat(`\\wsl.localhost\` + name + `\`); err == nil {
		return `\\wsl.localhost\` + name
	}
	return `\\wsl$\` + name // before Windows 11 / WSL 0.50
}

func wslStatePath() string { return filepath.Join(filepath.Dir(provider.Path()), "wsl.json") }

func wslDistros() []distro {
	wsl.Lock()
	defer wsl.Unlock()
	if wsl.seen == nil {
		wsl.seen, wsl.probed, wsl.failed = map[string]*distro{}, map[string]bool{}, map[string]time.Time{}
		if b, err := os.ReadFile(wslStatePath()); err == nil {
			json.Unmarshal(b, &wsl.seen)
		}
	}
	if time.Since(wsl.at) > wslListAge {
		wsl.names, wsl.listed = wslList("-l", "-q")
		run, _ := wslList("-l", "--running", "-q")
		wsl.running = map[string]bool{}
		for _, n := range run {
			wsl.running[n] = true
		}
		wsl.at = time.Now()
	}
	installed := map[string]bool{}
	mirrored := wslMirrored(wslConfig())
	var out []distro
	for _, n := range wsl.names {
		installed[n] = true
		if wsl.running[n] && !wsl.probed[n] {
			if t, ok := wsl.failed[n]; !ok || time.Since(t) > wslRetryAge {
				if d := wslProbe(n); d == nil {
					wsl.failed[n] = time.Now()
				} else {
					wsl.probed[n] = true
					if d.Has["dir:.codex"] || d.Has["bin:codex"] {
						if old := wsl.seen[n]; old != nil {
							d.Values = old.Values
						}
						wsl.seen[n] = d
					} else {
						delete(wsl.seen, n)
					}
					wsl.dirty = true
				}
			}
		}
		d := wsl.seen[n]
		if d == nil {
			continue
		}
		c := *d
		c.Running, c.Mirrored = wsl.running[n], mirrored
		out = append(out, c)
	}
	// an unregistered distro is forgotten
	for n := range wsl.seen {
		if wsl.listed && !installed[n] {
			delete(wsl.seen, n)
			wsl.dirty = true
		}
	}
	wslSaveLocked()
	return out
}

// wslSave writes wsl.json if anything in it changed.
func wslSave() {
	wsl.Lock()
	defer wsl.Unlock()
	wslSaveLocked()
}

func wslSaveLocked() {
	if wsl.dirty {
		if b, err := json.MarshalIndent(wsl.seen, "", "  "); err == nil && edit.WriteAtomic(wslStatePath(), b) == nil {
			wsl.dirty = false
		}
	}
}

// wslRemember keeps what a distro's field reads, for while it is stopped.
func wslRemember(name, key, v string) {
	wsl.Lock()
	defer wsl.Unlock()
	d := wsl.seen[name]
	if d == nil || d.Values[key] == v {
		return
	}
	if d.Values == nil {
		d.Values = map[string]string{}
	}
	d.Values[key] = v
	wsl.dirty = true
}

func wslLastSeen(name, key string) string {
	wsl.Lock()
	defer wsl.Unlock()
	if d := wsl.seen[name]; d != nil {
		return d.Values[key]
	}
	return ""
}

// wslList is the names wsl.exe lists with args; ok is false when it
// couldn't say (no WSL, or none installed).
func wslList(args ...string) (names []string, ok bool) {
	b, err := wslRun(10*time.Second, args...)
	if err != nil {
		return nil, false
	}
	return parseDistros(b), true
}

// wslProbeScript prints the distro's home, what of Codex it has, and its
// default route (the Windows host under NAT).
const wslProbeScript = `echo "home:$HOME"; [ -d "$HOME/.codex" ] && echo dir:.codex; ` +
	`command -v codex >/dev/null 2>&1 && echo bin:codex; ` +
	`ip route show default 2>/dev/null | head -n1 | sed 's/^/route:/'; ` +
	`grep -m1 '^nameserver' /etc/resolv.conf 2>/dev/null | sed 's/^/ns:/'; true`

func wslProbe(name string) *distro {
	b, err := wslRun(30*time.Second, "-d", name, "-e", "sh", "-lc", wslProbeScript)
	if err != nil {
		return nil
	}
	d := parseProbe(name, string(b))
	if d == nil {
		return nil
	}
	if d.Has["dir:.codex"] || d.Has["bin:codex"] {
		d.Root = wslRoot(name)
	}
	return d
}

// parseProbe reads wslProbeScript's output.
func parseProbe(name, out string) *distro {
	d := &distro{Name: name, Has: map[string]bool{}}
	var ns string
	sc := bufio.NewScanner(strings.NewReader(out))
	for sc.Scan() {
		l := strings.TrimSpace(sc.Text())
		switch k, v, _ := strings.Cut(l, ":"); k {
		case "home":
			d.Home = strings.TrimRight(v, "/")
		case "dir", "bin":
			d.Has[l] = true
		case "route":
			// default via 172.20.0.1 dev eth0 …
			if f := strings.Fields(v); len(f) >= 3 && f[1] == "via" && net.ParseIP(f[2]) != nil {
				d.Gateway = f[2]
			}
		case "ns":
			if f := strings.Fields(v); len(f) >= 2 && net.ParseIP(f[1]) != nil {
				ns = f[1]
			}
		}
	}
	if !strings.HasPrefix(d.Home, "/") {
		return nil
	}
	if d.Gateway == "" {
		d.Gateway = ns
	}
	return d
}

// parseDistros reads wsl.exe -l -q: UTF-16LE (with or without a BOM), or
// UTF-8 under WSL_UTF8. Docker Desktop's own distros are left out.
func parseDistros(b []byte) []string {
	s := decodeWSL(b)
	var out []string
	for _, l := range strings.Split(s, "\n") {
		l = strings.TrimSpace(strings.Trim(l, "\x00\ufeff\r"))
		if l == "" || strings.HasPrefix(strings.ToLower(l), "docker-desktop") {
			continue
		}
		out = append(out, l)
	}
	return out
}

func decodeWSL(b []byte) string {
	utf16le := len(b) >= 2 && (b[0] == 0xff && b[1] == 0xfe || b[1] == 0 && b[0] != 0)
	if !utf16le {
		return string(b)
	}
	if b[0] == 0xff && b[1] == 0xfe {
		b = b[2:]
	}
	u := make([]uint16, len(b)/2)
	for i := range u {
		u[i] = uint16(b[2*i]) | uint16(b[2*i+1])<<8
	}
	return string(utf16.Decode(u))
}

// wslConfig is %UserProfile%\.wslconfig, "" if there is none.
func wslConfig() string {
	home, _ := os.UserHomeDir()
	b, _ := os.ReadFile(filepath.Join(home, ".wslconfig"))
	return string(b)
}

// wslMirrored reports whether a .wslconfig puts WSL 2 in mirrored
// networking: networkingMode=mirrored under [wsl2].
func wslMirrored(cfg string) bool {
	section, mirrored := "", false
	sc := bufio.NewScanner(strings.NewReader(strings.TrimPrefix(cfg, "\ufeff")))
	for sc.Scan() {
		l := strings.TrimSpace(sc.Text())
		if l == "" || l[0] == '#' || l[0] == ';' {
			continue
		}
		if strings.HasPrefix(l, "[") && strings.HasSuffix(l, "]") {
			section = strings.ToLower(strings.TrimSpace(l[1 : len(l)-1]))
			continue
		}
		k, v, ok := strings.Cut(l, "=")
		if !ok || section != "wsl2" || !strings.EqualFold(strings.TrimSpace(k), "networkingMode") {
			continue
		}
		if i := strings.IndexAny(v, "#;"); i >= 0 {
			v = v[:i]
		}
		mirrored = strings.EqualFold(strings.Trim(strings.TrimSpace(v), `"`), "mirrored")
	}
	return mirrored
}
