package provider

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"slices"
	"strings"
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
func (p Provider) RestKey(k KeyInfo) string {
	if len(p.KeysOn()) > 1 {
		return p.ID + "#" + k.ID
	}
	return p.ID
}

func newKeyID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic("generating key identity: " + err.Error())
	}
	return hex.EncodeToString(b[:])
}

func (p *Provider) normalizeKeys() {
	if p.Account != nil {
		return
	} // OAuth credentials never become saved keys
	// Constructors may supply Key, but it is never persisted as a mirror.
	if p.KeyID == "" && strings.TrimSpace(p.Key) != "" && !slices.ContainsFunc(p.Keys, func(k KeyAccount) bool { return k.Key == strings.TrimSpace(p.Key) }) {
		p.Keys = append([]KeyAccount{{Key: strings.TrimSpace(p.Key)}}, p.Keys...)
	}
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

func (p Provider) KeysOn() []KeyAccount {
	if p.Account != nil {
		return nil
	}
	var out []KeyAccount
	if p.KeyID == "" && p.Key != "" && !slices.ContainsFunc(p.Keys, func(k KeyAccount) bool { return k.Key == p.Key }) {
		out = append(out, KeyAccount{Key: p.Key}) // unsaved constructor only
	}
	for _, k := range p.Keys {
		if !k.Off && k.Key != "" {
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

func AddKey(id, name, key string, proto Protocol) error {
	if err := keyProtocolOK(proto); err != nil {
		return err
	}
	key = strings.TrimSpace(key)
	if key == "" {
		return errors.New("paste the key to add")
	}
	p, err := Find(id)
	if err != nil {
		return err
	}
	if p.Account != nil {
		return errors.New("a signed-in account has no keys")
	}
	if slices.ContainsFunc(p.Keys, func(k KeyAccount) bool { return k.Key == key }) {
		return fmt.Errorf("%s already has this key", p.Name)
	}
	p.Keys = append(p.Keys, KeyAccount{ID: newKeyID(), Name: strings.TrimSpace(name), Key: key, Protocol: proto})
	return Save(*p)
}

// SplitKeys accepts keys separated by whitespace, commas or semicolons.
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

// ReplaceKey rotates a secret without changing bindings, identity or order.
func ReplaceKey(id, ref, key string) error {
	key = strings.TrimSpace(key)
	if key == "" {
		return errors.New("paste the replacement key")
	}
	return editKey(id, ref, func(p *Provider, i int) error {
		for j, k := range p.Keys {
			if j != i && k.Key == key {
				return fmt.Errorf("%s already has this key", p.Name)
			}
		}
		p.Keys[i].Key = key
		return nil
	})
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

func SetKeyBinding(id, ref string, poolRefs []string, models []string) error {
	pools := QuotaPools()
	refs := make([]string, 0, len(poolRefs))
	for _, ref := range poolRefs {
		ref = strings.TrimSpace(ref)
		if ref == "" {
			return errors.New("quota pool reference must not be empty")
		}
		if slices.Contains(refs, ref) {
			return fmt.Errorf("duplicate quota pool %q", ref)
		}
		if !slices.ContainsFunc(pools, func(p QuotaPool) bool { return p.ID == ref }) {
			return fmt.Errorf("unknown quota pool %q", ref)
		}
		refs = append(refs, ref)
	}
	models = cleanList(models)
	for _, m := range models {
		if m != "*" && strings.ContainsAny(m, "*?[]") {
			return fmt.Errorf("model %q must be an exact ID or *", m)
		}
	}
	return editKey(id, ref, func(p *Provider, i int) error { p.Keys[i].PoolRefs, p.Keys[i].Models = refs, models; return nil })
}
