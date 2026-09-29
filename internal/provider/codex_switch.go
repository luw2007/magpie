package provider

// Signing Codex or Claude Code in to the next of its accounts when the one
// it is on has used its allowance up. Codex's requests on its own models go
// through magpie while more of its accounts are on there, and move on to
// the next account when one is out — but the Codex app, once it knows the
// account it is signed in to is out, may not send at all, so no request
// reaches magpie to move on. Claude Code on its own models never comes
// through magpie: it asks Anthropic as the account it is signed in to, and
// that account being out ends its turns with "You've hit your session
// limit" while the gateway has long moved on to another (#208). So
// whichever magpie runs the gateway signs the agent in to the next account
// that is on and has room, as switching it on the Accounts page would: a
// session started after that is on it from the first turn.
//
// It moves at the share Smart routing counts an account spent at, so the
// account the agent is signed in to and the one the gateway goes to agree
// on which is out (#209): the sign-in follows Smart; Smart doesn't follow
// the sign-in.

import (
	"context"
	"log"
	"time"

	"github.com/yetone/magpie/internal/catalog"
)

// loginSwitchEvery is how often the account each agent is on is looked at.
const loginSwitchEvery = 5 * time.Minute

// SpentShare is the share of an allowance past which an account is all
// but used up: Smart routing keeps it for when no other can take a
// request, and the agent signed in to it is signed in to another.
const SpentShare = 98

// switchedAgents are the agents whose account magpie moves on.
var switchedAgents = []string{"codex", "claude"}

// usedUp reports whether an account's allowance is used up for now: a
// window that stops the account, for every model, at 100%.
func usedUp(q SubscriptionQuota) bool {
	return usedPast(q, 100)
}

// spent reports whether an account's allowance is all but used up, as
// Smart routing counts it: a window for every model at SpentShare.
func spent(q SubscriptionQuota) bool {
	return usedPast(q, SpentShare)
}

func usedPast(q SubscriptionQuota, share float64) bool {
	for _, w := range q.Windows {
		if !w.Aside && w.Model == "" && w.Used >= share {
			return true
		}
	}
	return false
}

// NextLogin is the account agent should be signed in to instead of the
// one it is on, when that one is spent: the first of its other accounts
// that are on in magpie, in the order they were saved, whose allowance is
// known and not spent. ok is false when the agent should stay.
func NextLogin(ctx context.Context, agent string) (from, to string, ok bool) {
	if accountRemoved(agent) {
		return "", "", false // magpie has no say in its sign-in
	}
	var spares []Login
	for _, l := range Logins(agent) {
		switch {
		case l.Active:
			from = l.User
		case l.On && l.Lapsed == "":
			spares = append(spares, l)
		}
	}
	if from == "" || len(spares) == 0 {
		return "", "", false
	}
	u := LoginUsage(ctx, agent)
	if q, known := u[from]; !known || q.Error != "" || !spent(q) {
		return "", "", false
	}
	for _, l := range spares {
		if q, known := u[l.User]; known && q.Error == "" && !spent(q) {
			return from, l.User, true
		}
	}
	return "", "", false
}

// SwitchWhenSpent signs agent in to the next of its accounts when the one
// it is on is spent (NextLogin), and answers the account it signed it in
// to, "" when it stayed.
func SwitchWhenSpent(ctx context.Context, agent string) (string, error) {
	from, to, ok := NextLogin(ctx, agent)
	if !ok {
		return "", nil
	}
	if err := SwitchLogin(agent, to); err != nil {
		return "", err
	}
	log.Printf("%s: %s has used %d%% or more of its allowance; signed it in to %s", agent, from, SpentShare, to)
	// the models the agent is offered are the new account's plan's
	catalog.Touched()
	return to, nil
}

// KeepOnAnAccountWithRoom runs SwitchWhenSpent for Codex and Claude Code a
// minute after it starts and every loginSwitchEvery after that, until ctx
// ends.
func KeepOnAnAccountWithRoom(ctx context.Context) {
	t := time.NewTimer(time.Minute)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		for _, agent := range switchedAgents {
			c, cancel := context.WithTimeout(ctx, time.Minute)
			if _, err := SwitchWhenSpent(c, agent); err != nil {
				log.Printf("%s: switching to an account with room: %v", agent, err)
			}
			cancel()
		}
		t.Reset(loginSwitchEvery)
	}
}
