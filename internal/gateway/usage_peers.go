package gateway

import (
	"context"
	"log"
	"net/http"
	"time"

	"github.com/yetone/magpie/internal/provider"
	"github.com/yetone/magpie/internal/usage"
)

// peerPullEvery is how often a remote magpie's days of calls are asked for.
const peerPullEvery = 5 * time.Minute

func init() {
	WhileServing = append(WhileServing, func(ctx context.Context) {
		select {
		case <-ctx.Done():
			return
		case <-time.After(15 * time.Second):
		}
		tick := time.NewTicker(peerPullEvery)
		defer tick.Stop()
		for {
			if len(provider.RemoteMagpies()) > 0 {
				c, cancel := context.WithTimeout(ctx, time.Minute)
				for id, err := range usage.PullPeers(c) {
					log.Printf("usage of remote magpie %s: %v", id, err)
				}
				cancel()
			}
			select {
			case <-ctx.Done():
				return
			case <-tick.C:
			}
		}
	})
}

// usageDays is this computer's own calls, a day each, for another magpie
// that has this one as its provider to show beside its own (?since=, a date;
// the whole window when none). It carries what the ledger lists of a call,
// never a request's content, and it is told as the quotas are: to this
// machine, or to another that carries a gateway key.
func (s *Server) usageDays(w http.ResponseWriter, r *http.Request) {
	if !local(r) && !sharedWith(r) {
		writeError(w, provider.Chat, http.StatusForbidden, "magpie's usage is told to another machine only when magpie is shared on the local network (Settings → Share on local network) and the request carries its API key (Authorization: Bearer <key> or x-api-key: <key>)")
		return
	}
	since := time.Now().AddDate(0, 0, 1-usage.SharedDays)
	if t, err := time.ParseInLocation(time.DateOnly, r.URL.Query().Get("since"), time.Local); err == nil && t.After(since) {
		since = t
	}
	writeJSON(w, 200, map[string]any{"object": "list", "data": usage.PeerDays(since)})
}
