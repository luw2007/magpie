package provider

import (
	"maps"
	"regexp"
	"slices"
)

var secretHeader = regexp.MustCompile(`(?i)auth|key|token|secret|cookie|session|password`)

// SecretHeader reports whether a header or an environment variable by that
// name looks like it holds a key: what a backup without keys leaves empty.
func SecretHeader(name string) bool { return secretHeader.MatchString(name) }

// KeepSecrets is in, as sync or a restore brings it over here, with the
// secrets that didn't come along kept from here (#1821). keyless is that
// in came from a copy without keys: its keys and its secret headers are
// here's then. An AccessKey ID and Secret, or a balance token, that in
// hasn't at all are kept when it came without keys or with the key here
// has, the same account: a magpie from before access keys were synced
// (v0.1.1142), or one the key was never given on, sends a provider with
// its key and nothing else, and taking that whole wiped the Volcengine
// and Tencent plan cards' access keys on this computer. A key of its own
// is another account's, whose access key this one's isn't.
func KeepSecrets(in, here Provider, keyless bool) Provider {
	if keyless {
		in.Key, in.KeyID, in.Keys = here.Key, here.KeyID, slices.Clone(here.Keys)
		// the headers a copy without keys left out, in a map of its own:
		// in's may be shared with the list it came in
		cloned := false
		for k, v := range here.Headers {
			if _, ok := in.Headers[k]; ok || !SecretHeader(k) {
				continue
			}
			if !cloned {
				in.Headers, cloned = maps.Clone(in.Headers), true
				if in.Headers == nil {
					in.Headers = map[string]string{}
				}
			}
			in.Headers[k] = v
		}
	}
	inKey, hereKey := primaryKey(in), primaryKey(here)
	if !keyless && (inKey == "" || inKey != hereKey) {
		return in
	}
	if in.BalanceToken == "" {
		in.BalanceToken = here.BalanceToken
	}
	if in.AccessKeyID == "" && in.SecretAccessKey == "" {
		in.AccessKeyID, in.SecretAccessKey = here.AccessKeyID, here.SecretAccessKey
	}
	return in
}

// primaryKey is the credential a provider's account is signed in with: its
// first enabled saved key, else an unsaved constructor's runtime Key. The
// provider-wide secrets (balance token, access key pair) belong to that
// account, not to any spare key it also carries.
func primaryKey(p Provider) string {
	for _, k := range p.Keys {
		if !k.Off && k.Key != "" {
			return k.Key
		}
	}
	return p.Key
}
