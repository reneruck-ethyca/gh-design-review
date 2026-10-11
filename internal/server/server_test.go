package server

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"testing/fstest"
)

func TestGuard(t *testing.T) {
	s := &Server{Token: "secret", Port: 4000, Assets: fstest.MapFS{"index.html": {Data: []byte(`<meta content="__DR_TOKEN__">`)}}}
	h := s.Handler()
	cases := []struct {
		name, method, path, host, cookie, header string
		want                                     int
	}{
		{"wrong host", "GET", "/api/review", "evil.com:4000", "secret", "", 403},
		{"no cookie", "GET", "/api/review", "127.0.0.1:4000", "", "", 401},
		{"wrong cookie", "GET", "/api/review", "127.0.0.1:4000", "nope", "", 401},
		{"write without header", "POST", "/api/threads/x/resolve", "localhost:4000", "secret", "", 403},
		{"write wrong header", "POST", "/api/threads/x/resolve", "localhost:4000", "secret", "nope", 403},
		{"token in url sets cookie", "GET", "/?t=secret", "127.0.0.1:4000", "", "", 302},
		{"index without cookie", "GET", "/", "127.0.0.1:4000", "", "", 401},
		{"index with cookie", "GET", "/", "127.0.0.1:4000", "secret", "", 200},
	}
	for _, c := range cases {
		r := httptest.NewRequest(c.method, c.path, nil)
		r.Host = c.host
		if c.cookie != "" {
			r.AddCookie(&http.Cookie{Name: cookieName, Value: c.cookie})
		}
		if c.header != "" {
			r.Header.Set("X-DR-Token", c.header)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != c.want {
			t.Errorf("%s: got %d, want %d", c.name, w.Code, c.want)
		}
	}
}

func TestGlob(t *testing.T) {
	re, _ := GlobRegexp("docs/design/**/*.md")
	for p, want := range map[string]bool{
		"docs/design/a.md":       true,
		"docs/design/x/y/b.md":   true,
		"docs/design/a.mdx":      false,
		"docs/designs/a.md":      false,
		"other/docs/design/a.md": false,
	} {
		if re.MatchString(p) != want {
			t.Errorf("%s: want %v", p, want)
		}
	}
}
