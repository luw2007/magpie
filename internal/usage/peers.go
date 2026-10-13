package usage

import (
	"context"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/yetone/magpie/internal/provider"
)

// Usage pulled from a remote magpie: a computer whose providers include
// another magpie (remote-magpie) asks it for the days of calls that magpie
// made itself, and keeps them under PeersDir, where the Usage page reads them
// beside the synced ones. Nothing is pushed and nothing is stored on the
// remote; each computer remains the only writer of its own calls.

// PeersPath is the gateway's answer of its own days of calls.
const PeersPath = "/v1/magpie/usage/days"

// PeerDays is this computer's own calls from the day of since on, a day each,
// as PeersPath answers them. A call another magpie passed on here is left to
// that computer (OwnDays), so a pair that pulls from each other counts every
// call once.
func PeerDays(since time.Time) []SharedDay {
	id, name := Computer()
	days := OwnDays(since)
	out := make([]SharedDay, 0, len(days))
	for day, calls := range days {
		out = append(out, SharedDay{Computer: id, Name: name, Day: day, Calls: calls})
	}
	return out
}

var pulled struct {
	sync.Mutex
	whole  map[string]bool // remote magpie id → its whole window was pulled once
	status map[string]PeerStatus
}

// PeerStatus is how the last pull of one remote magpie went, for the Usage
// page to say so beside the computers it lists: data older than it looks must
// not pass for current.
type PeerStatus struct {
	ID   string `json:"id"`   // the remote-magpie provider's id
	Name string `json:"name"` // what the provider is called
	// Pulling is set until the first pull of a remote has ended, so a remote
	// just added is not read as one that has nothing.
	Pulling bool `json:"pulling,omitempty"`
	// At is when days were last kept from it; zero before a pull succeeded.
	At time.Time `json:"at,omitzero"`
	// Error is why the last pull failed, as the remote's answer tells it
	// (provider.RemoteGet); empty when it went well.
	Error string `json:"error,omitempty"`
	// Kind sorts Error for the page: "unreachable", "key", "notShared",
	// "tooOld" or "other"; empty when there is no error.
	Kind string `json:"kind,omitempty"`
}

// Peers are the remote magpies this one pulls usage from and how each last
// went, in the order the providers are listed. A provider switched off or
// removed is not among them: its days stay kept, nothing is promised of them.
func Peers() []PeerStatus {
	ps := provider.RemoteMagpies()
	pulled.Lock()
	defer pulled.Unlock()
	out := make([]PeerStatus, 0, len(ps))
	for _, p := range ps {
		s, ok := pulled.status[p.ID]
		if !ok {
			s = PeerStatus{Pulling: true}
		}
		s.ID, s.Name = p.ID, p.Name
		if s.Name == "" {
			s.Name = p.ID
		}
		out = append(out, s)
	}
	return out
}

func notePeer(id string, err error) {
	pulled.Lock()
	defer pulled.Unlock()
	if pulled.status == nil {
		pulled.status = map[string]PeerStatus{}
	}
	s := pulled.status[id]
	s.Pulling = false
	if err == nil {
		s.At, s.Error, s.Kind = time.Now(), "", ""
	} else {
		s.Error, s.Kind = err.Error(), provider.RemoteErrorKind(err)
	}
	pulled.status[id] = s
}

// PullPeers asks every remote magpie for its days and keeps them. The first
// pull of a remote takes the whole SharedDays window, later ones yesterday
// and today. A remote that can't be asked keeps showing what it answered
// before, and its error is returned for the caller to log.
func PullPeers(ctx context.Context) map[string]error {
	ps := provider.RemoteMagpies()
	errs := make([]error, len(ps))
	var wg sync.WaitGroup
	for i, p := range ps {
		wg.Add(1)
		go func() { defer wg.Done(); errs[i] = pullPeer(ctx, p); notePeer(p.ID, errs[i]) }()
	}
	wg.Wait()
	out := map[string]error{}
	for i, err := range errs {
		if err != nil {
			out[ps[i].ID] = err
		}
	}
	return out
}

// PullPeerNow asks one remote magpie for its days at once, for the page's
// Pull now: after a remote is added or its key fixed, nobody waits for the
// next round. It returns the status the pull ended with; a provider that is
// not a remote magpie in use is an error.
func PullPeerNow(ctx context.Context, id string) (PeerStatus, error) {
	for _, p := range provider.RemoteMagpies() {
		if p.ID != id {
			continue
		}
		notePeer(id, pullPeer(ctx, p))
		for _, s := range Peers() {
			if s.ID == id {
				return s, nil
			}
		}
	}
	return PeerStatus{}, os.ErrNotExist
}

func pullPeer(ctx context.Context, p provider.Provider) error {
	now := Clock()
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.Local)
	first := today.AddDate(0, 0, 1-SharedDays)
	since := first
	pulled.Lock()
	if pulled.whole[p.ID] {
		since = today.AddDate(0, 0, -1)
	}
	pulled.Unlock()
	var days []SharedDay
	if err := provider.RemoteGet(ctx, p, PeersPath+"?since="+url.QueryEscape(since.Format(time.DateOnly)), &days); err != nil {
		return err
	}
	self, _ := Computer()
	from := first.Format(time.DateOnly)
	seen := map[string]bool{}
	for _, d := range days {
		// a day that is named for this computer is its own come back round
		if d.Computer == self || d.Day < from || !validID(d.Computer) {
			continue
		}
		if err := keepIn(PeersDir(), d); err != nil {
			return err
		}
		seen[d.Computer] = true
	}
	// the window's end: a day past it goes, for every computer pulled
	for c := range seen {
		for _, k := range keptIn(PeersDir()) {
			who, day, _ := strings.Cut(k, "/")
			if who == c && day < from {
				dropIn(PeersDir(), who, day)
			}
		}
	}
	pulled.Lock()
	if pulled.whole == nil {
		pulled.whole = map[string]bool{}
	}
	pulled.whole[p.ID] = true
	pulled.Unlock()
	return nil
}

// ForgetPeersForTest has the next pull take the whole window again and
// forgets how the pulls went.
func ForgetPeersForTest() {
	pulled.Lock()
	pulled.whole, pulled.status = nil, nil
	pulled.Unlock()
}
