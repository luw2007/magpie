package provider

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// KeyAccount is a persisted credential with an identity independent of its secret.
type KeyAccount struct {
	ID       string   `json:"id"`
	Name     string   `json:"name,omitempty"`
	Key      string   `json:"key"`
	Protocol Protocol `json:"protocol,omitempty"`
	Off      bool     `json:"off,omitempty"`
	PoolRefs []string `json:"poolRefs,omitempty"`
	Models   []string `json:"models,omitempty"`
	// Weight is the key's share of the requests when the provider's
	// routing is Weighted (#841): one with 3 takes three for every one a
	// key with 1 takes. None, or 0, counts as 1.
	Weight int `json:"weight,omitempty"`
}

type KeyInfo struct {
	ID       string   `json:"id"`
	Name     string   `json:"name,omitempty"`
	Masked   string   `json:"masked"`
	Active   bool     `json:"active"`
	On       bool     `json:"on"`
	Protocol Protocol `json:"protocol,omitempty"`
	PoolRefs []string `json:"poolRefs,omitempty"`
	Models   []string `json:"models,omitempty"`
	Weight   int      `json:"weight,omitempty"`
	// Rest is why the gateway passes it over now, after a failure, and
	// until when; nil while it takes requests.
	Rest *KeyRest `json:"rest,omitempty"`
}

// KeyRest is a key's rest as the gateway keeps it: failQuota, failRate…,
// the status it answered, until when, and the key to lift it by.
type KeyRest struct {
	Why    string    `json:"why"`
	Status int       `json:"status"`
	Until  time.Time `json:"until"`
	Key    string    `json:"key"`
}

// RestKey is what the gateway rests the key k of a provider by: the
// provider's id while it has one key on, else the id and the key's.
func (p Provider) RestKey(k KeyInfo) string { return p.RestKeys()(k) }

// RestKeys is RestKey for each of a provider's keys in turn, whether it
// has more than one on found once: found again for every key, a list of
// a thousand keys was read a thousand times over, and the Providers page
// took seconds to draw it, minutes for five thousand (@NetRozee on X).
func (p Provider) RestKeys() func(KeyInfo) string {
	several := p.severalOn()
	return func(k KeyInfo) string {
		if several {
			return p.ID + "#" + k.ID
		}
		return p.ID
	}
}

// severalOn is len(p.KeysOn()) > 1 without listing them: a key on after
// the first that isn't the first again.
func (p Provider) severalOn() bool {
	if p.Key == "" {
		return false
	}
	for _, k := range p.Keys {
		if !k.Off && k.Key != "" && k.Key != p.Key {
			return true
		}
	}
	return false
}

func newKeyID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic("generating key identity: " + err.Error())
	}
	return hex.EncodeToString(b[:])
}

// mergeKey lists the constructor's Key among Keys when none of them holds
// it; Key is never persisted as a mirror.
func (p *Provider) mergeKey() {
	if p.Account != nil {
		return
	}
	if p.KeyID == "" && strings.TrimSpace(p.Key) != "" && !slices.ContainsFunc(p.Keys, func(k KeyAccount) bool { return strings.TrimSpace(k.Key) == strings.TrimSpace(p.Key) }) {
		p.Keys = append([]KeyAccount{{Key: strings.TrimSpace(p.Key)}}, p.Keys...)
	}
}

// adoptKeyIDs gives each key with no id the id of the stored key holding
// the same secret, so saving a provider built from a bare Key, or from keys
// that lost their ids, keeps the identities that pools, rests and usage
// name them by instead of minting new ones.
func (p *Provider) adoptKeyIDs(was []KeyAccount) {
	if p.Account != nil || len(was) == 0 {
		return
	}
	p.mergeKey()
	taken := make(map[string]bool, len(p.Keys))
	for _, k := range p.Keys {
		if k.ID != "" {
			taken[k.ID] = true
		}
	}
	for i := range p.Keys {
		k := &p.Keys[i]
		if k.ID != "" {
			continue
		}
		for _, w := range was {
			if w.ID != "" && !taken[w.ID] && strings.TrimSpace(w.Key) == strings.TrimSpace(k.Key) {
				k.ID = w.ID
				taken[w.ID] = true
				break
			}
		}
	}
}

func (p *Provider) normalizeKeys() {
	if p.Account != nil {
		return
	} // OAuth credentials never become saved keys
	p.mergeKey()
	seen := make(map[string]bool, len(p.Keys))
	for i := range p.Keys {
		k := &p.Keys[i]
		k.Key, k.Name = strings.TrimSpace(k.Key), strings.TrimSpace(k.Name)
		if k.ID == "" || seen[k.ID] {
			k.ID = newKeyID()
		}
		seen[k.ID] = true
		k.Models = cleanList(k.Models)
	}
	p.Key, p.KeyID = "", ""
	for _, k := range p.Keys {
		if !k.Off && k.Key != "" {
			p.Key, p.KeyID = k.Key, k.ID
			break
		}
	}
}

// SelectedKey returns metadata for the runtime credential.
func (p Provider) SelectedKey() KeyAccount {
	for _, k := range p.Keys {
		if k.ID == p.KeyID {
			return k
		}
	}
	return KeyAccount{Key: p.Key}
}

func (p Provider) KeyList() []KeyInfo {
	p.Keys = append([]KeyAccount(nil), p.Keys...)
	p.normalizeKeys()
	var out []KeyInfo
	for _, k := range p.Keys {
		out = append(out, KeyInfo{
			ID:       k.ID,
			Name:     k.Name,
			Masked:   Mask(k.Key),
			Active:   k.ID == p.KeyID,
			On:       !k.Off,
			Protocol: k.Protocol,
			PoolRefs: k.PoolRefs,
			Models:   k.Models,
			Weight:   k.Weight,
		})
	}
	return out
}

// KeysOn is the keys in use, first to last, each secret once.
func (p Provider) KeysOn() []KeyAccount {
	keysListed.Add(1)
	if p.Account != nil {
		return nil
	}
	var out []KeyAccount
	seen := make(map[string]bool, len(p.Keys)+1)
	if p.KeyID == "" && p.Key != "" && !slices.ContainsFunc(p.Keys, func(k KeyAccount) bool { return k.Key == p.Key }) {
		out = append(out, KeyAccount{Key: p.Key}) // unsaved constructor only
		seen[p.Key] = true
	}
	for _, k := range p.Keys {
		if !k.Off && k.Key != "" && !seen[k.Key] {
			seen[k.Key] = true
			out = append(out, k)
		}
	}
	return out
}

func keyProtocolOK(proto Protocol) error {
	switch proto {
	case "", Chat, Responses, Anthropic:
		return nil
	}
	return fmt.Errorf("unknown protocol %q", proto)
}

// keysListed counts KeysOn's lists (KeysListed).
var keysListed atomic.Int64

// KeysListed is how many times a provider's keys on have been listed in
// this process: a test counts them, not the time a page or request took,
// to say they are listed once and not once a key (@NetRozee on X).
func KeysListed() int64 { return keysListed.Load() }

// AddKey saves one more key for a provider, on: it takes requests after
// the ones before it. It becomes the first when the provider has none.
func AddKey(id, name, key string, proto Protocol) error {
	if err := keyProtocolOK(proto); err != nil {
		return err
	}
	key = strings.TrimSpace(key)
	p, err := Find(id)
	if err != nil {
		return err
	}
	if p.Account != nil {
		return errors.New("a signed-in account has no keys")
	}
	// Nothing pasted: use the vendor's public key when it offers one.
	anon := false
	if key == "" {
		if key = p.AnonymousKey(); key == "" {
			return errors.New("paste the key to add")
		}
		anon = true
	}
	if p.HasKey(key) {
		if anon {
			return fmt.Errorf("%s already has %s, the key its free models answer with none of yours", p.Name, key)
		}
		return fmt.Errorf("%s already has this key", p.Name)
	}
	p.Keys = append(p.Keys, KeyAccount{ID: newKeyID(), Name: strings.TrimSpace(name), Key: key, Protocol: proto})
	return Save(*p)
}

// AnonymousKey is the key the provider is asked with when the user has
// none of their own, "" when it has no such key: OpenCode Zen's public,
// which its free models answer (normalize gives it to a Zen provider
// saved with no key).
func (p Provider) AnonymousKey() string {
	if p.Preset == "opencode-zen" {
		return OpenCodeAnonymousKey
	}
	return ""
}

// HasKey reports whether key is one of the provider's, on or off.
func (p Provider) HasKey(key string) bool {
	return p.Key == key && key != "" || slices.ContainsFunc(p.Keys, func(k KeyAccount) bool { return k.Key == key })
}

// SplitKeys is the keys in text pasted at once: one a line, or separated
// by commas, semicolons or spaces, trimmed of quotes, each once.
func SplitKeys(text string) []string {
	var out []string
	seen := map[string]bool{}
	for _, f := range strings.FieldsFunc(text, func(r rune) bool {
		return r == ',' || r == ';' || r == '，' || r == '；' || r == ' ' || r == '\t' || r == '\n' || r == '\r' || r == '\u3000'
	}) {
		f = strings.Trim(f, "\"'`")
		if f != "" && !seen[f] {
			seen[f] = true
			out = append(out, f)
		}
	}
	return out
}

// AddKeys imports distinct credentials without changing existing identities.
func AddKeys(id string, keys []string, proto Protocol) (added, had int, err error) {
	if err := keyProtocolOK(proto); err != nil {
		return 0, 0, err
	}
	p, err := Find(id)
	if err != nil {
		return 0, 0, err
	}
	if p.Account != nil {
		return 0, 0, errors.New("a signed-in account has no keys")
	}
	seen := map[string]bool{}
	for _, k := range p.Keys {
		seen[k.Key] = true
	}
	for _, key := range keys {
		key = strings.TrimSpace(key)
		if key == "" {
			continue
		}
		if seen[key] {
			had++
			continue
		}
		seen[key] = true
		added++
		p.Keys = append(p.Keys, KeyAccount{ID: newKeyID(), Key: key, Protocol: proto})
	}
	if added == 0 {
		if had > 0 {
			return 0, had, fmt.Errorf("%s already has these keys", p.Name)
		}
		return 0, 0, errors.New("paste the keys to add")
	}
	return added, had, Save(*p)
}

// MaxKeyWeight is the most a key's weight can be.
const MaxKeyWeight = 1000

// keyReadsAtOnce is how many of one provider's keys have their balance,
// plan or limits read from the vendor at once. Every key at once was a
// thousand requests to one relay together for a provider of a thousand
// keys (@NetRozee on X); other providers' keys are read beside them.
const keyReadsAtOnce = 8

// keySlots is one read's slots, keyReadsAtOnce for each provider.
type keySlots struct {
	sync.Mutex
	m map[string]chan struct{}
}

// take waits for a slot of the provider id's, and gives what frees it. A
// read given up on (ctx done) goes on without one, to fail at once.
func (s *keySlots) take(ctx context.Context, id string) (release func()) {
	s.Lock()
	if s.m == nil {
		s.m = map[string]chan struct{}{}
	}
	c := s.m[id]
	if c == nil {
		c = make(chan struct{}, keyReadsAtOnce)
		s.m[id] = c
	}
	s.Unlock()
	select {
	case c <- struct{}{}:
		return func() { <-c }
	case <-ctx.Done():
		return func() {}
	}
}

// WeightOf is the key's weight as Weighted routing counts it: 1 when none.
func (k KeyAccount) WeightOf() int { return max(k.Weight, 1) }

func findKey(p *Provider, ref string) (int, bool) {
	for i, k := range p.Keys {
		if k.ID == ref {
			return i, true
		}
	}
	return 0, false
}

func editKey(id, ref string, change func(*Provider, int) error) error {
	p, err := Find(id)
	if err != nil {
		return err
	}
	i, ok := findKey(p, ref)
	if !ok {
		return fmt.Errorf("%s has no such key", p.Name)
	}
	if err := change(p, i); err != nil {
		return err
	}
	return Save(*p)
}

func SetKeyProtocol(id, ref string, proto Protocol) error {
	if err := keyProtocolOK(proto); err != nil {
		return err
	}
	return editKey(id, ref, func(p *Provider, i int) error { p.Keys[i].Protocol = proto; return nil })
}

// SetKeyWeight sets a key's share of the requests under Weighted routing;
// 0 takes it back to the default, 1.
func SetKeyWeight(id, keyRef string, weight int) error {
	if weight < 0 || weight > MaxKeyWeight {
		return fmt.Errorf("a key's weight is 0 to %d", MaxKeyWeight)
	}
	p, err := Find(id)
	if err != nil {
		return err
	}
	i, ok := findKey(p, keyRef)
	if !ok {
		return fmt.Errorf("%s has no such key", p.Name)
	}
	if weight == 1 {
		weight = 0 // the default: kept out of the file
	}
	return editKey(id, keyRef, func(p *Provider, _ int) error { p.Keys[i].Weight = weight; return nil })
}

// WithKey is p using key k: its endpoints narrowed to k's protocol when k
// has one. It has none left when p doesn't serve that protocol.
func (p Provider) WithKey(k KeyAccount) Provider {
	p.Key, p.KeyID = k.Key, k.ID
	if k.Protocol != "" {
		if k.Protocol != Chat {
			p.Chat = ""
		}
		if k.Protocol != Responses {
			p.Responses = ""
		}
		if k.Protocol != Anthropic {
			p.Anthropic = ""
		}
	}
	return p
}

// KeyOf returns a key's secret by its stable identity for its editor row.
func KeyOf(id, ref string) (string, error) {
	p, err := Find(id)
	if err != nil {
		return "", err
	}
	i, ok := findKey(p, ref)
	if !ok {
		return "", fmt.Errorf("%s has no such key", p.ID)
	}
	return p.Keys[i].Key, nil
}

// UseKey makes a key first and turns it on, keeping the others in order.
func UseKey(id, ref string) error {
	return editKey(id, ref, func(p *Provider, i int) error {
		k := p.Keys[i]
		k.Off = false
		copy(p.Keys[1:i+1], p.Keys[:i])
		p.Keys[0] = k
		return nil
	})
}

func SetKeyOn(id, ref string, on bool) error {
	return editKey(id, ref, func(p *Provider, i int) error {
		if !on && !p.Keys[i].Off && len(p.KeysOn()) == 1 {
			return errors.New("that's the only key in use; turn another on first")
		}
		p.Keys[i].Off = !on
		return nil
	})
}

func RemoveKey(id, ref string) error {
	return editKey(id, ref, func(p *Provider, i int) error {
		if !p.Keys[i].Off && len(p.KeysOn()) == 1 {
			return errors.New("that's the only key in use; turn another on before removing it")
		}
		p.Keys = slices.Delete(p.Keys, i, i+1)
		return nil
	})
}

func RenameKey(id, ref, name string) error {
	return editKey(id, ref, func(p *Provider, i int) error { p.Keys[i].Name = strings.TrimSpace(name); return nil })
}

// AllowsModel applies only explicit key restrictions, to the outbound model.
func (k KeyAccount) AllowsModel(model string) bool {
	return len(k.Models) == 0 || slices.Contains(k.Models, "*") || slices.Contains(k.Models, model)
}

// RemoveKeys forgets several keys at once (361 on Discord: hundreds of
// keys, some dead): those named by id that it has. One key in use always
// stays: removing every key in use is refused.
func RemoveKeys(id string, refs []string) (removed int, err error) {
	var n int
	err = func() error {
		p, err := Find(id)
		if err != nil {
			return err
		}
		gone := map[string]bool{}
		for _, r := range refs {
			gone[r] = true
		}
		var kept []KeyAccount
		for _, k := range p.Keys {
			if gone[k.ID] {
				n++
				continue
			}
			kept = append(kept, k)
		}
		if n == 0 {
			return fmt.Errorf("%s has none of these keys", p.Name)
		}
		p.Keys = kept
		if len(p.KeysOn()) == 0 {
			return errors.New("those are all the keys in use; keep one on")
		}
		return Save(*p)
	}()
	if err != nil {
		return 0, err
	}
	return n, nil
}

// KeyBinding is the quota pools and model scope a key is set to.
type KeyBinding struct {
	PoolRefs []string `json:"poolRefs"`
	Models   []string `json:"models"`
}

// checkBinding validates and cleans one key's pools and models.
func checkBinding(pools []QuotaPool, poolRefs, models []string) (refs, cleaned []string, err error) {
	refs = make([]string, 0, len(poolRefs))
	for _, ref := range poolRefs {
		ref = strings.TrimSpace(ref)
		if ref == "" {
			return nil, nil, errors.New("quota pool reference must not be empty")
		}
		if slices.Contains(refs, ref) {
			return nil, nil, fmt.Errorf("duplicate quota pool %q", ref)
		}
		if !slices.ContainsFunc(pools, func(p QuotaPool) bool { return p.ID == ref }) {
			return nil, nil, fmt.Errorf("unknown quota pool %q", ref)
		}
		refs = append(refs, ref)
	}
	cleaned = cleanList(models)
	for _, m := range cleaned {
		if m != "*" && strings.ContainsAny(m, "*?[]") {
			return nil, nil, fmt.Errorf("model %q must be an exact ID or *", m)
		}
	}
	return refs, cleaned, nil
}

func SetKeyBinding(id, ref string, poolRefs []string, models []string) error {
	refs, models, err := checkBinding(QuotaPools(), poolRefs, models)
	if err != nil {
		return err
	}
	return editKey(id, ref, func(p *Provider, i int) error { p.Keys[i].PoolRefs, p.Keys[i].Models = refs, models; return nil })
}

// ApplyKeyBindings sets the pools and models of several keys of p, by key
// id, in memory: all or none, so one bad pick saves nothing. Keys not named
// keep theirs.
func ApplyKeyBindings(p *Provider, bindings map[string]KeyBinding) error {
	pools := QuotaPools()
	keys := append([]KeyAccount(nil), p.Keys...)
	for ref, b := range bindings {
		i, ok := findKey(p, ref)
		if !ok {
			return fmt.Errorf("%s has no such key %q", p.Name, ref)
		}
		refs, models, err := checkBinding(pools, b.PoolRefs, b.Models)
		if err != nil {
			return fmt.Errorf("key %s: %w", ref, err)
		}
		keys[i].PoolRefs, keys[i].Models = refs, models
	}
	p.Keys = keys
	return nil
}

// ReplaceKey rotates a secret, preserving its stable identity, order and
// bindings. Both returned identities are the same, so callers retaining
// account references keep pointing at this key. A repeated secret is a no-op.
func ReplaceKey(id, keyRef, key string) (was, now string, err error) {
	key = strings.TrimSpace(key)
	if key == "" {
		return "", "", errors.New("paste the key's new value")
	}
	p, err := Find(id)
	if err != nil {
		return "", "", err
	}
	i, ok := findKey(p, keyRef)
	if !ok {
		return "", "", fmt.Errorf("%s has no such key", p.Name)
	}
	now = keyRef
	if p.Keys[i].Key == key {
		return keyRef, now, nil
	}
	if p.HasKey(key) {
		return "", "", fmt.Errorf("%s already has this key", p.Name)
	}
	p.Keys[i].Key = key
	return keyRef, now, Save(*p)
}

// moveKeyed moves m's entry for the key was, if it has one, to now.
func moveKeyed[V any](m map[string]V, was, now string) {
	if v, ok := m[was]; ok {
		delete(m, was)
		m[now] = v
	}
}
