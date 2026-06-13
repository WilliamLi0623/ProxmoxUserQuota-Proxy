package identity

import (
	"net/http"
	"testing"
)

func req(cookie, auth string) *http.Request {
	r, _ := http.NewRequest("POST", "/", nil)
	if cookie != "" {
		r.Header.Set("Cookie", "PVEAuthCookie="+cookie)
	}
	if auth != "" {
		r.Header.Set("Authorization", auth)
	}
	return r
}

func TestFromRequest(t *testing.T) {
	tests := []struct {
		name   string
		cookie string
		auth   string
		want   Identity
	}{
		{
			name:   "cookie root@pam",
			cookie: "PVE:root@pam:6A2CC5E2::abcSIGdef",
			want:   Identity{Kind: KindCookie, User: "root@pam", Realm: "pam"},
		},
		{
			name:   "cookie url-encoded",
			cookie: "PVE%3Auser%40pve%3A6A2CC5E2%3A%3AsigPart",
			want:   Identity{Kind: KindCookie, User: "user@pve", Realm: "pve"},
		},
		{
			name: "token",
			auth: "PVEAPIToken=automation@pve!ci=2f9c-secret-uuid",
			want: Identity{Kind: KindToken, User: "automation@pve", Realm: "pve", Token: "ci"},
		},
		{
			name: "token with space separator",
			auth: "PVEAPIToken root@pam!mon=deadbeef",
			want: Identity{Kind: KindToken, User: "root@pam", Realm: "pam", Token: "mon"},
		},
		{
			name:   "token wins over cookie",
			cookie: "PVE:cookieuser@pam:6A::sig",
			auth:   "PVEAPIToken=tok@pve!t=sec",
			want:   Identity{Kind: KindToken, User: "tok@pve", Realm: "pve", Token: "t"},
		},
		{
			name:   "tfa ticket ignored",
			cookie: "PVE:!tfa!eyJabc",
			want:   Identity{Kind: KindNone},
		},
		{
			name:   "malformed cookie",
			cookie: "garbage",
			want:   Identity{Kind: KindNone},
		},
		{
			name:   "user without realm",
			cookie: "PVE:nobody:6A::sig",
			want:   Identity{Kind: KindNone},
		},
		{
			name: "nothing",
			want: Identity{Kind: KindNone},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := FromRequest(req(tt.cookie, tt.auth))
			if got != tt.want {
				t.Errorf("FromRequest() = %+v, want %+v", got, tt.want)
			}
		})
	}
}
