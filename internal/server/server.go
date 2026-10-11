// Package server is the authenticated localhost proxy between the page and GitHub.
package server

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"mime"
	"net/http"
	"path"
	"regexp"
	"strings"
	"sync"

	"github.com/cli/go-gh/v2/pkg/api"
	"github.com/reneruck/gh-design-review/internal/diff"
	"github.com/reneruck/gh-design-review/internal/github"
)

const cookieName = "dr_session"

type Server struct {
	GH     *github.Client
	PR     *github.PRRef
	Glob   *regexp.Regexp
	Token  string
	Port   int
	Assets fs.FS

	mu   sync.Mutex
	head string
	docs []github.File
}

// GlobRegexp compiles a path glob where ** spans directories and * does not.
func GlobRegexp(glob string) (*regexp.Regexp, error) {
	var b strings.Builder
	b.WriteString("^")
	for i := 0; i < len(glob); i++ {
		switch {
		case strings.HasPrefix(glob[i:], "**/"):
			b.WriteString("(?:.*/)?")
			i += 2
		case strings.HasPrefix(glob[i:], "**"):
			b.WriteString(".*")
			i++
		case glob[i] == '*':
			b.WriteString("[^/]*")
		case glob[i] == '?':
			b.WriteString("[^/]")
		default:
			b.WriteString(regexp.QuoteMeta(glob[i : i+1]))
		}
	}
	b.WriteString("$")
	return regexp.Compile(b.String())
}

// Snapshot returns the current head SHA and the matching docs at that head,
// re-listing files only when the head moved.
func (s *Server) Snapshot() (string, []github.File, error) {
	head, err := s.GH.Head()
	if err != nil {
		return "", nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if head != s.head {
		files, err := s.GH.Files()
		if err != nil {
			return "", nil, err
		}
		docs := []github.File{}
		for _, f := range files {
			if f.Status != "removed" && s.Glob.MatchString(f.Filename) {
				docs = append(docs, f)
			}
		}
		s.head, s.docs = head, docs
	}
	return s.head, s.docs, nil
}

func status(s string) string {
	if s == "added" || s == "renamed" {
		return s
	}
	return "modified"
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", s.index)
	mux.Handle("GET /", http.FileServerFS(s.Assets))

	mux.HandleFunc("GET /api/pr", s.getPR)
	mux.HandleFunc("GET /api/file", s.getFile)
	mux.HandleFunc("GET /api/blob", s.getBlob)
	mux.HandleFunc("GET /api/threads", func(w http.ResponseWriter, r *http.Request) {
		t, err := s.GH.Threads(r.URL.Query().Get("path"))
		reply(w, t, err)
	})
	mux.HandleFunc("POST /api/threads", s.write(func(r *http.Request, head string) error {
		var in github.CreateThread
		if err := decode(r, &in); err != nil {
			return err
		}
		return s.GH.CreateThread(in, head)
	}))
	mux.HandleFunc("POST /api/threads/{id}/replies", s.write(func(r *http.Request, head string) error {
		var in struct{ Body, Mode, CommentID string }
		if err := decode(r, &in); err != nil {
			return err
		}
		return s.GH.Reply(r.PathValue("id"), in.CommentID, in.Body, in.Mode, head)
	}))
	mux.HandleFunc("POST /api/threads/{id}/resolve", s.write(func(r *http.Request, _ string) error {
		return s.GH.SetResolved(r.PathValue("id"), true)
	}))
	mux.HandleFunc("POST /api/threads/{id}/unresolve", s.write(func(r *http.Request, _ string) error {
		return s.GH.SetResolved(r.PathValue("id"), false)
	}))
	mux.HandleFunc("GET /api/review", func(w http.ResponseWriter, r *http.Request) {
		rv, err := s.GH.PendingReview()
		reply(w, rv, err)
	})
	mux.HandleFunc("POST /api/review/submit", s.write(func(r *http.Request, head string) error {
		var in struct{ Event, Body string }
		if err := decode(r, &in); err != nil {
			return err
		}
		return s.GH.SubmitReview(in.Event, in.Body, head)
	}))
	mux.HandleFunc("DELETE /api/review", s.write(func(r *http.Request, _ string) error {
		return s.GH.DiscardReview()
	}))
	return s.guard(mux)
}

func (s *Server) eq(v string) bool {
	return subtle.ConstantTimeCompare([]byte(v), []byte(s.Token)) == 1
}

// guard blocks DNS rebinding (Host check) and any caller without the session
// cookie; writes additionally need the token header, which only our page can read.
func (s *Server) guard(next http.Handler) http.Handler {
	hosts := map[string]bool{fmt.Sprintf("127.0.0.1:%d", s.Port): true, fmt.Sprintf("localhost:%d", s.Port): true}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !hosts[r.Host] {
			fail(w, http.StatusForbidden, "bad_host", "Unexpected Host header")
			return
		}
		if strings.HasPrefix(r.URL.Path, "/api/") {
			c, err := r.Cookie(cookieName)
			if err != nil || !s.eq(c.Value) {
				fail(w, http.StatusUnauthorized, "unauthorized", "Missing or invalid session")
				return
			}
			if r.Method != http.MethodGet && !s.eq(r.Header.Get("X-DR-Token")) {
				fail(w, http.StatusForbidden, "bad_token", "Missing or invalid X-DR-Token header")
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) index(w http.ResponseWriter, r *http.Request) {
	if t := r.URL.Query().Get("t"); t != "" && s.eq(t) {
		http.SetCookie(w, &http.Cookie{Name: cookieName, Value: s.Token, Path: "/", HttpOnly: true, SameSite: http.SameSiteStrictMode})
		http.Redirect(w, r, "/", http.StatusFound)
		return
	}
	if c, err := r.Cookie(cookieName); err != nil || !s.eq(c.Value) {
		http.Error(w, "Open the URL printed by gh design-review.", http.StatusUnauthorized)
		return
	}
	b, err := fs.ReadFile(s.Assets, "index.html")
	if err != nil {
		http.Error(w, "frontend not built: run `make build`", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Write([]byte(strings.Replace(string(b), "__DR_TOKEN__", s.Token, 1)))
}

type doc struct {
	Path   string `json:"path"`
	Status string `json:"status"`
}

func (s *Server) getPR(w http.ResponseWriter, r *http.Request) {
	head, files, err := s.Snapshot()
	if err != nil {
		reply(w, nil, err)
		return
	}
	docs := []doc{}
	for _, f := range files {
		docs = append(docs, doc{f.Filename, status(f.Status)})
	}
	reply(w, map[string]any{
		"owner": s.GH.Owner, "repo": s.GH.Repo, "number": s.PR.Number, "title": s.PR.Title, "url": s.PR.URL,
		"headSha": head, "baseSha": s.PR.BaseRefOid, "headOwner": s.GH.HeadOwner, "headRepo": s.GH.HeadRepo,
		"viewer": s.GH.Viewer, "docs": docs,
	}, nil)
}

func (s *Server) getFile(w http.ResponseWriter, r *http.Request) {
	p := r.URL.Query().Get("path")
	head, files, err := s.Snapshot()
	if err != nil {
		reply(w, nil, err)
		return
	}
	for _, f := range files {
		if f.Filename != p {
			continue
		}
		content, err := s.GH.Content(p, head)
		if err != nil {
			reply(w, nil, err)
			return
		}
		out := map[string]any{"path": p, "headSha": head, "content": string(content), "status": status(f.Status),
			"patchMissing": f.Patch == nil, "hunks": []diff.Hunk{}, "addedLines": []int{}}
		if f.Patch != nil {
			out["hunks"], out["addedLines"] = diff.Parse(*f.Patch)
		}
		reply(w, out, nil)
		return
	}
	fail(w, http.StatusNotFound, "not_found", "Not a design doc in this PR: "+p)
}

func (s *Server) getBlob(w http.ResponseWriter, r *http.Request) {
	p := r.URL.Query().Get("path")
	head, _, err := s.Snapshot()
	if err != nil {
		reply(w, nil, err)
		return
	}
	b, err := s.GH.Content(p, head)
	if err != nil {
		reply(w, nil, err)
		return
	}
	if ct := mime.TypeByExtension(path.Ext(p)); ct != "" {
		w.Header().Set("Content-Type", ct)
	}
	// Repo content is untrusted: never let it run as a page on our origin (e.g. an SVG opened directly).
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Security-Policy", "sandbox")
	w.Write(b)
}

// write wraps a mutating handler with the stale-head guard: the page sends the
// head SHA it rendered, and we refuse if new commits landed since.
func (s *Server) write(fn func(r *http.Request, head string) error) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		head, err := s.GH.Head()
		if err != nil {
			reply(w, nil, err)
			return
		}
		if r.Header.Get("X-DR-Head") != head {
			fail(w, http.StatusConflict, "stale_head", "New commits were pushed. Reload to comment.")
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
		if err := fn(r, head); err != nil {
			reply(w, nil, err)
			return
		}
		reply(w, map[string]bool{"ok": true}, nil)
	}
}

func decode(r *http.Request, v any) error {
	if err := json.NewDecoder(r.Body).Decode(v); err != nil {
		return badRequest{err}
	}
	return nil
}

type badRequest struct{ error }

func reply(w http.ResponseWriter, v any, err error) {
	if err != nil {
		status, code := http.StatusBadGateway, "github_error"
		var he *api.HTTPError
		var ge *api.GraphQLError
		var br badRequest
		switch {
		case errors.As(err, &br):
			status, code = http.StatusBadRequest, "bad_request"
		case errors.Is(err, github.ErrNoPendingReview):
			status, code = http.StatusNotFound, "no_pending_review"
		case errors.As(err, &he) && he.StatusCode >= 400 && he.StatusCode < 500:
			status = he.StatusCode
		case errors.As(err, &ge):
			status = http.StatusUnprocessableEntity
		}
		fail(w, status, code, err.Error())
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v)
}

func fail(w http.ResponseWriter, status int, code, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(map[string]any{"error": map[string]string{"code": code, "message": msg}})
}
