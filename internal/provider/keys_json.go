package provider

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strconv"
)

// providerJSON avoids recursion while keeping every provider field in one schema.
type providerJSON Provider

func (p *Provider) UnmarshalJSON(data []byte) error {
	var v struct {
		*providerJSON
		Key         string   `json:"key"`
		KeyName     string   `json:"keyName"`
		KeyProtocol Protocol `json:"keyProtocol"`
	}
	var decoded Provider
	v.providerJSON = (*providerJSON)(&decoded)
	if err := json.Unmarshal(data, &v); err != nil {
		return err
	}
	if v.Key != "" {
		decoded.Keys = append([]KeyAccount{{Name: v.KeyName, Key: v.Key, Protocol: v.KeyProtocol}}, decoded.Keys...)
	}
	// Legacy files have no identities. Repeated reads before the next save
	// must agree so references from the GUI can resolve.
	for i := range decoded.Keys {
		if decoded.Keys[i].ID == "" {
			sum := sha256.Sum256([]byte(decoded.ID + "\x00" + strconv.Itoa(i) + "\x00" + decoded.Keys[i].Key))
			decoded.Keys[i].ID = hex.EncodeToString(sum[:16])
		}
	}
	decoded.normalizeKeys()
	*p = decoded
	return nil
}

func (p Provider) MarshalJSON() ([]byte, error) {
	// Only Keys carry secrets in persisted/exported JSON. Runtime selection is
	// deliberately omitted; decoding derives it from enabled key order.
	p.Keys = append([]KeyAccount(nil), p.Keys...)
	p.normalizeKeys()
	return json.Marshal(providerJSON(p))
}
