// Package identity extracts the acting PVE user from a request.
//
// In audit mode (P2) uq-proxy only observes: pveproxy upstream still performs
// real authentication, so we parse the identity without verifying the ticket
// signature. Optional signature verification can be layered on later (see
// architecture.md). The token secret is never read or logged.
package identity

import (
	"net/http"
	"net/url"
	"strings"
)

// Kind is how the caller authenticated.
type Kind string

const (
	KindNone   Kind = "none"
	KindCookie Kind = "cookie"
	KindToken  Kind = "token"
)

// Identity is the parsed caller identity. User is "user@realm".
type Identity struct {
	Kind  Kind
	User  string
	Realm string
	Token string // API token name, only for KindToken
}

const cookieName = "PVEAuthCookie"

// FromRequest parses identity from the API token header or the PVE auth
// cookie. Token auth takes precedence, matching how automation calls the API.
func FromRequest(r *http.Request) Identity {
	if h := r.Header.Get("Authorization"); h != "" {
		if id, ok := parseToken(h); ok {
			return id
		}
	}
	if c, err := r.Cookie(cookieName); err == nil {
		if id, ok := parseCookie(c.Value); ok {
			return id
		}
	}
	return Identity{Kind: KindNone}
}

// parseToken parses `PVEAPIToken=user@realm!tokenid=secret` (a space after
// PVEAPIToken is also tolerated). The secret is discarded.
func parseToken(h string) (Identity, bool) {
	const prefix = "PVEAPIToken"
	if !strings.HasPrefix(h, prefix) {
		return Identity{}, false
	}
	rest := strings.TrimSpace(h[len(prefix):])
	rest = strings.TrimSpace(strings.TrimPrefix(rest, "="))
	idPart, _, _ := strings.Cut(rest, "=") // drop the secret
	user, token, ok := strings.Cut(idPart, "!")
	if !ok {
		return Identity{}, false
	}
	user = strings.TrimSpace(user)
	token = strings.TrimSpace(token)
	realm, ok := realmOf(user)
	if !ok || token == "" {
		return Identity{}, false
	}
	return Identity{Kind: KindToken, User: user, Realm: realm, Token: token}, true
}

// parseCookie parses `PVE:user@realm:HEXTIME::SIGNATURE` (URL-decoded).
// Only the userid is extracted; timestamp and signature are not verified.
func parseCookie(v string) (Identity, bool) {
	if dec, err := url.QueryUnescape(v); err == nil {
		v = dec
	}
	const prefix = "PVE:"
	if !strings.HasPrefix(v, prefix) {
		return Identity{}, false
	}
	rest := v[len(prefix):]
	// Skip non-login tickets such as TFA challenges ("PVE:!tfa!...").
	if strings.HasPrefix(rest, "!") {
		return Identity{}, false
	}
	user, _, ok := strings.Cut(rest, ":")
	if !ok {
		return Identity{}, false
	}
	realm, ok := realmOf(user)
	if !ok {
		return Identity{}, false
	}
	return Identity{Kind: KindCookie, User: user, Realm: realm}, true
}

func realmOf(user string) (string, bool) {
	at := strings.LastIndex(user, "@")
	if at <= 0 || at == len(user)-1 {
		return "", false
	}
	return user[at+1:], true
}
