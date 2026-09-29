package gateway

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"net"
	"net/http"
	"os"
	"strings"
	"sync/atomic"

	"github.com/yetone/magpie/internal/settings"
)

// The gateway listens on loopback and takes any token, which is safe only
// because nothing but this computer reaches it. Shared on the local network
// (settings' LAN), it listens on every interface, and a request from
// another machine must carry the key magpie made for it, as its API key;
// this computer's agents go on as before.

// lanKey is the key a request from another machine must carry, "" while
// the gateway isn't shared.
var lanKey atomic.Pointer[string]

// listenAddr is where the gateway listens: every interface while it is
// shared, on its port, else its address.
func listenAddr() string {
	if s := settings.Load(); s.LAN && s.LANKey != "" {
		return "0.0.0.0:" + Port()
	}
	return Addr()
}

// Port is the gateway's port.
func Port() string {
	_, p, err := net.SplitHostPort(Addr())
	if err != nil {
		return "3425"
	}
	return p
}

func loadLANKey() {
	k := ""
	if s := settings.Load(); s.LAN {
		k = s.LANKey
	}
	lanKey.Store(&k)
}

// NewLANKey makes a key for sharing the gateway.
func NewLANKey() string {
	b := make([]byte, 20)
	rand.Read(b)
	return "sk-magpie-" + hex.EncodeToString(b)
}

// LANURLs are the addresses other machines on the network reach the
// gateway at, one per IPv4 address this computer has there.
func LANURLs() []string {
	var out []string
	ifs, _ := net.Interfaces()
	for _, i := range ifs {
		if i.Flags&net.FlagUp == 0 || i.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, _ := i.Addrs()
		for _, a := range addrs {
			n, ok := a.(*net.IPNet)
			if !ok || n.IP.To4() == nil || n.IP.IsLinkLocalUnicast() {
				continue
			}
			out = append(out, "http://"+net.JoinHostPort(n.IP.String(), Port()))
		}
	}
	return out
}

// Relisten moves the gateway to where settings now say it listens — onto
// the network or back to loopback — and takes up the key; requests in
// flight finish.
func (s *Server) Relisten() error {
	loadLANKey()
	s.lnMu.Lock()
	defer s.lnMu.Unlock()
	if s.ln == nil {
		return nil // not serving
	}
	to := listenAddr()
	if s.ln.Addr().String() == to {
		return nil
	}
	was := s.ln.Addr().String()
	// the port is the same, so the old one goes first
	s.ln.Close()
	ln, err := Listen(to)
	if err != nil {
		if ln, _ = Listen(was); ln == nil {
			return err
		}
		s.ln = ln
		return err
	}
	s.ln = ln
	return nil
}

// lanGuard lets a request from another machine through only with the key,
// and hands it on as though it came from here, with the gateway's token.
// A gateway put on the network with MAGPIE_ADDR and not shared from the
// Settings page is open, as it has always been.
func lanGuard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if local(r) {
			next.ServeHTTP(w, r)
			return
		}
		key := ""
		if k := lanKey.Load(); k != nil {
			key = *k
		}
		if key == "" {
			if os.Getenv("MAGPIE_ADDR") != "" {
				next.ServeHTTP(w, r)
				return
			}
			http.Error(w, "magpie isn't shared on the local network", http.StatusForbidden)
			return
		}
		if subtle.ConstantTimeCompare([]byte(callerKey(r)), []byte(key)) != 1 {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusUnauthorized)
			w.Write([]byte(`{"error":{"type":"authentication_error","message":"use the API key shown in magpie's Settings, under Share on local network"}}`))
			return
		}
		r.Header.Set("Authorization", "Bearer "+Token)
		if r.Header.Get("x-api-key") != "" {
			r.Header.Set("x-api-key", Token)
		}
		if r.Header.Get("x-goog-api-key") != "" {
			r.Header.Set("x-goog-api-key", Token)
		}
		if q := r.URL.Query(); q.Get("key") != "" {
			q.Set("key", Token)
			r.URL.RawQuery = q.Encode()
		}
		next.ServeHTTP(w, r)
	})
}

// callerKey is the API key a request carries, however its client sends one.
func callerKey(r *http.Request) string {
	if a := r.Header.Get("Authorization"); a != "" {
		return strings.TrimSpace(strings.TrimPrefix(a, "Bearer "))
	}
	for _, h := range []string{"x-api-key", "x-goog-api-key"} {
		if v := r.Header.Get(h); v != "" {
			return v
		}
	}
	return r.URL.Query().Get("key")
}

// local is a request from this computer.
func local(r *http.Request) bool {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return false
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
