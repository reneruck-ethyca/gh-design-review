// Package github holds every call to GitHub. It is the only code that sees the token.
package github

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"

	gh "github.com/cli/go-gh/v2"
	"github.com/cli/go-gh/v2/pkg/api"
)

// PRRef is what `gh pr view` tells us about the PR.
type PRRef struct {
	Number     int    `json:"number"`
	Title      string `json:"title"`
	URL        string `json:"url"`
	HeadRefOid string `json:"headRefOid"`
	BaseRefOid string `json:"baseRefOid"`
	HeadRepo   *struct {
		Name string `json:"name"`
	} `json:"headRepository"`
	HeadOwner *struct {
		Login string `json:"login"`
	} `json:"headRepositoryOwner"`

	Host, Owner, Repo string `json:"-"`
}

// ResolvePR reuses gh's own rules for numbers, URLs, branches and the current branch.
func ResolvePR(arg, repo string) (*PRRef, error) {
	args := []string{"pr", "view"}
	if arg != "" {
		args = append(args, arg)
	}
	if repo != "" {
		args = append(args, "-R", repo)
	}
	args = append(args, "--json", "number,title,url,headRefOid,baseRefOid,headRepository,headRepositoryOwner")
	out, stderr, err := gh.Exec(args...)
	if err != nil {
		return nil, fmt.Errorf("gh pr view: %s", strings.TrimSpace(stderr.String()))
	}
	var pr PRRef
	if err := json.Unmarshal(out.Bytes(), &pr); err != nil {
		return nil, err
	}
	u, err := url.Parse(pr.URL)
	if err != nil {
		return nil, err
	}
	parts := strings.Split(strings.Trim(u.Path, "/"), "/") // owner/repo/pull/N
	if len(parts) < 2 {
		return nil, fmt.Errorf("unexpected PR url %q", pr.URL)
	}
	pr.Host, pr.Owner, pr.Repo = u.Host, parts[0], parts[1]
	return &pr, nil
}

type Client struct {
	rest, raw           *api.RESTClient
	gql                 *api.GraphQLClient
	Owner, Repo         string
	Number              int
	HeadOwner, HeadRepo string
	PRID                string
	Viewer              string

	mu       sync.Mutex
	reviewID string // cached pending review id
}

func New(pr *PRRef) (*Client, error) {
	opts := api.ClientOptions{Host: pr.Host}
	rest, err := api.NewRESTClient(opts)
	if err != nil {
		return nil, err
	}
	opts.Headers = map[string]string{"Accept": "application/vnd.github.raw+json"}
	raw, err := api.NewRESTClient(opts)
	if err != nil {
		return nil, err
	}
	gql, err := api.NewGraphQLClient(api.ClientOptions{Host: pr.Host})
	if err != nil {
		return nil, err
	}
	c := &Client{rest: rest, raw: raw, gql: gql, Owner: pr.Owner, Repo: pr.Repo, Number: pr.Number,
		HeadOwner: pr.Owner, HeadRepo: pr.Repo}
	// The head repo is null when the fork was deleted; fall back to the base repo.
	if pr.HeadRepo != nil && pr.HeadOwner != nil {
		c.HeadOwner, c.HeadRepo = pr.HeadOwner.Login, pr.HeadRepo.Name
	}
	var resp struct {
		Viewer     struct{ Login string }
		Repository struct{ PullRequest struct{ ID string } }
	}
	err = c.gql.Do(`query($owner:String!,$repo:String!,$number:Int!){
		viewer { login }
		repository(owner:$owner,name:$repo){ pullRequest(number:$number){ id } } }`, c.vars(nil), &resp)
	if err != nil {
		return nil, err
	}
	c.Viewer, c.PRID = resp.Viewer.Login, resp.Repository.PullRequest.ID
	return c, nil
}

func (c *Client) vars(extra map[string]any) map[string]any {
	v := map[string]any{"owner": c.Owner, "repo": c.Repo, "number": c.Number}
	for k, x := range extra {
		v[k] = x
	}
	return v
}

func (c *Client) Head() (string, error) {
	var resp struct{ Repository struct{ PullRequest struct{ HeadRefOid string } } }
	err := c.gql.Do(`query($owner:String!,$repo:String!,$number:Int!){
		repository(owner:$owner,name:$repo){ pullRequest(number:$number){ headRefOid } } }`, c.vars(nil), &resp)
	return resp.Repository.PullRequest.HeadRefOid, err
}

type File struct {
	Filename string  `json:"filename"`
	Status   string  `json:"status"`
	Patch    *string `json:"patch"`
}

func (c *Client) Files() ([]File, error) {
	var all []File
	for page := 1; ; page++ {
		var batch []File
		path := fmt.Sprintf("repos/%s/%s/pulls/%d/files?per_page=100&page=%d", c.Owner, c.Repo, c.Number, page)
		if err := c.rest.Get(path, &batch); err != nil {
			return nil, err
		}
		all = append(all, batch...)
		if len(batch) < 100 {
			return all, nil
		}
	}
}

// Content returns raw file bytes from the head repo, where fork commits live.
func (c *Client) Content(path, ref string) ([]byte, error) {
	segs := strings.Split(path, "/")
	for i, s := range segs {
		segs[i] = url.PathEscape(s)
	}
	resp, err := c.raw.Request("GET", fmt.Sprintf("repos/%s/%s/contents/%s?ref=%s",
		c.HeadOwner, c.HeadRepo, strings.Join(segs, "/"), url.QueryEscape(ref)), nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	return io.ReadAll(resp.Body)
}

type Comment struct {
	ID         string `json:"id"`
	DatabaseID string `json:"databaseId"`
	Author     string `json:"author"`
	AvatarURL  string `json:"avatarUrl"`
	BodyHTML   string `json:"bodyHTML"`
	CreatedAt  string `json:"createdAt"`
	URL        string `json:"url"`
	State      string `json:"state"`
}

type Anchor struct {
	StartLine int    `json:"startLine"`
	Line      int    `json:"line"`
	Sha       string `json:"sha"`
}

type Thread struct {
	ID                string    `json:"id"`
	Path              string    `json:"path"`
	SubjectType       string    `json:"subjectType"`
	StartLine         *int      `json:"startLine"`
	Line              *int      `json:"line"`
	OriginalStartLine *int      `json:"originalStartLine"`
	OriginalLine      *int      `json:"originalLine"`
	IsResolved        bool      `json:"isResolved"`
	IsOutdated        bool      `json:"isOutdated"`
	Anchor            *Anchor   `json:"anchor"`
	Comments          []Comment `json:"comments"`
}

var markerRe = regexp.MustCompile(`<!-- design-review:L(\d+)-L(\d+)@([0-9a-f]+) -->`)

func parseMarker(body string) *Anchor {
	m := markerRe.FindStringSubmatch(body)
	if m == nil {
		return nil
	}
	s, _ := strconv.Atoi(m[1])
	e, _ := strconv.Atoi(m[2])
	return &Anchor{s, e, m[3]}
}

const threadsQuery = `query($owner:String!,$repo:String!,$number:Int!,$cursor:String){
  repository(owner:$owner,name:$repo){ pullRequest(number:$number){
    reviewThreads(first:50, after:$cursor){
      pageInfo { hasNextPage endCursor }
      nodes {
        id path subjectType isResolved isOutdated
        line startLine originalLine originalStartLine
        comments(first:100){ nodes {
          id fullDatabaseId body bodyHTML createdAt url state
          author { login avatarUrl }
        } }
      }
    }
  } } }`

// threadNode is one reviewThreads node as GraphQL returns it. Its Comments field
// carries the same json name as Thread.Comments, so it needs the explicit tag:
// without it the embedded Thread.Comments wins the exact-name match and the
// {"nodes": [...]} object fails to decode into []Comment.
type threadNode struct {
	Thread
	Comments struct {
		Nodes []struct {
			ID, Body, BodyHTML, CreatedAt, URL, State string
			FullDatabaseID                            string // BigInt, encoded as a string
			Author                                    *struct{ Login, AvatarURL string }
		}
	} `json:"comments"`
}

// Threads returns every review thread on path, normalised.
func (c *Client) Threads(path string) ([]Thread, error) {
	out := []Thread{}
	var cursor *string
	for {
		var resp struct {
			Repository struct {
				PullRequest struct {
					ReviewThreads struct {
						PageInfo struct {
							HasNextPage bool
							EndCursor   string
						}
						Nodes []threadNode
					}
				}
			}
		}
		if err := c.gql.Do(threadsQuery, c.vars(map[string]any{"cursor": cursor}), &resp); err != nil {
			return nil, err
		}
		rt := resp.Repository.PullRequest.ReviewThreads
		for _, n := range rt.Nodes {
			if n.Path != path {
				continue
			}
			t := n.Thread
			t.Comments = []Comment{}
			for i, cm := range n.Comments.Nodes {
				if i == 0 {
					t.Anchor = parseMarker(cm.Body)
				}
				author, avatar := "ghost", ""
				if cm.Author != nil {
					author, avatar = cm.Author.Login, cm.Author.AvatarURL
				}
				t.Comments = append(t.Comments, Comment{cm.ID, cm.FullDatabaseID, author, avatar, cm.BodyHTML, cm.CreatedAt, cm.URL, cm.State})
			}
			out = append(out, t)
		}
		if !rt.PageInfo.HasNextPage {
			return out, nil
		}
		cursor = &rt.PageInfo.EndCursor
	}
}

type CreateThread struct {
	Path        string `json:"path"`
	Body        string `json:"body"`
	SubjectType string `json:"subjectType"`
	StartLine   int    `json:"startLine,omitempty"`
	Line        int    `json:"line,omitempty"`
	Mode        string `json:"mode"`
}

func (c *Client) CreateThread(in CreateThread, headSha string) error {
	if in.Mode == "now" {
		body := map[string]any{"commit_id": headSha, "path": in.Path, "body": in.Body}
		if in.SubjectType == "FILE" {
			body["subject_type"] = "file"
		} else {
			body["line"], body["side"] = in.Line, "RIGHT"
			if in.StartLine != 0 && in.StartLine != in.Line {
				body["start_line"], body["start_side"] = in.StartLine, "RIGHT"
			}
		}
		return c.restPost(fmt.Sprintf("repos/%s/%s/pulls/%d/comments", c.Owner, c.Repo, c.Number), body)
	}
	return c.withReview(headSha, func(reviewID string) error {
		input := map[string]any{"pullRequestReviewId": reviewID, "path": in.Path, "body": in.Body, "subjectType": in.SubjectType}
		if in.SubjectType == "LINE" {
			input["line"], input["side"] = in.Line, "RIGHT"
			if in.StartLine != 0 && in.StartLine != in.Line {
				input["startLine"], input["startSide"] = in.StartLine, "RIGHT"
			}
		}
		return c.mutate(`mutation($input:AddPullRequestReviewThreadInput!){ addPullRequestReviewThread(input:$input){ clientMutationId } }`, input)
	})
}

// Reply adds a reply; "now" mode needs the database id of the thread's first comment.
func (c *Client) Reply(threadID, firstCommentDBID, body, mode, headSha string) error {
	if mode == "now" {
		return c.restPost(fmt.Sprintf("repos/%s/%s/pulls/%d/comments/%s/replies", c.Owner, c.Repo, c.Number, url.PathEscape(firstCommentDBID)),
			map[string]any{"body": body})
	}
	return c.withReview(headSha, func(reviewID string) error {
		return c.mutate(`mutation($input:AddPullRequestReviewThreadReplyInput!){ addPullRequestReviewThreadReply(input:$input){ clientMutationId } }`,
			map[string]any{"pullRequestReviewThreadId": threadID, "pullRequestReviewId": reviewID, "body": body})
	})
}

func (c *Client) SetResolved(threadID string, resolved bool) error {
	name := "unresolveReviewThread"
	if resolved {
		name = "resolveReviewThread"
	}
	input := map[string]any{"threadId": threadID}
	return c.mutate(fmt.Sprintf(`mutation($input:%sInput!){ %s(input:$input){ clientMutationId } }`, strings.ToUpper(name[:1])+name[1:], name), input)
}

type Review struct {
	ID    string `json:"id"`
	Count int    `json:"count"`
}

// PendingReview returns the viewer's pending review, or nil.
func (c *Client) PendingReview() (*Review, error) {
	var resp struct {
		Repository struct {
			PullRequest struct {
				Reviews struct {
					Nodes []struct {
						ID       string
						Author   *struct{ Login string }
						Comments struct{ TotalCount int }
					}
				}
			}
		}
	}
	err := c.gql.Do(`query($owner:String!,$repo:String!,$number:Int!){
		repository(owner:$owner,name:$repo){ pullRequest(number:$number){
			reviews(states:PENDING, first:10){ nodes { id author { login } comments { totalCount } } } } } }`, c.vars(nil), &resp)
	if err != nil {
		return nil, err
	}
	for _, n := range resp.Repository.PullRequest.Reviews.Nodes {
		if n.Author != nil && n.Author.Login == c.Viewer {
			return &Review{n.ID, n.Comments.TotalCount}, nil
		}
	}
	return nil, nil
}

// withReview runs fn with the pending review id, reusing an existing pending
// review (possibly started on github.com) or creating one. If a cached id has
// gone stale (submitted or discarded elsewhere) it retries once with a fresh one.
func (c *Client) withReview(headSha string, fn func(string) error) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	cached := c.reviewID != ""
	if !cached {
		if err := c.ensureReview(headSha); err != nil {
			return err
		}
	}
	err := fn(c.reviewID)
	if err != nil && cached {
		c.reviewID = ""
		if err := c.ensureReview(headSha); err != nil {
			return err
		}
		err = fn(c.reviewID)
	}
	return err
}

func (c *Client) ensureReview(headSha string) error {
	r, err := c.PendingReview()
	if err != nil {
		return err
	}
	if r != nil {
		c.reviewID = r.ID
		return nil
	}
	var resp struct {
		AddPullRequestReview struct{ PullRequestReview struct{ ID string } }
	}
	err = c.gql.Do(`mutation($input:AddPullRequestReviewInput!){ addPullRequestReview(input:$input){ pullRequestReview { id } } }`,
		map[string]any{"input": map[string]any{"pullRequestId": c.PRID, "commitOID": headSha}}, &resp)
	c.reviewID = resp.AddPullRequestReview.PullRequestReview.ID
	return err
}

var ErrNoPendingReview = errors.New("no pending review")

func (c *Client) pendingID() (string, error) {
	r, err := c.PendingReview()
	if err != nil {
		return "", err
	}
	if r == nil {
		return "", ErrNoPendingReview
	}
	return r.ID, nil
}

// SubmitReview submits the pending review, or posts a fresh review (e.g. a bare
// Approve) when there is none.
func (c *Client) SubmitReview(event, body, headSha string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	id, err := c.pendingID()
	if errors.Is(err, ErrNoPendingReview) {
		input := map[string]any{"pullRequestId": c.PRID, "commitOID": headSha, "event": event}
		if body != "" {
			input["body"] = body
		}
		return c.mutate(`mutation($input:AddPullRequestReviewInput!){ addPullRequestReview(input:$input){ clientMutationId } }`, input)
	}
	if err != nil {
		return err
	}
	c.reviewID = ""
	input := map[string]any{"pullRequestReviewId": id, "event": event}
	if body != "" {
		input["body"] = body
	}
	return c.mutate(`mutation($input:SubmitPullRequestReviewInput!){ submitPullRequestReview(input:$input){ clientMutationId } }`, input)
}

func (c *Client) DiscardReview() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	id, err := c.pendingID()
	if err != nil {
		return err
	}
	c.reviewID = ""
	return c.mutate(`mutation($input:DeletePullRequestReviewInput!){ deletePullRequestReview(input:$input){ clientMutationId } }`,
		map[string]any{"pullRequestReviewId": id})
}

func (c *Client) mutate(query string, input map[string]any) error {
	var resp any
	return c.gql.Do(query, map[string]any{"input": input}, &resp)
}

func (c *Client) restPost(path string, body any) error {
	b, err := json.Marshal(body)
	if err != nil {
		return err
	}
	var resp any
	return c.rest.Post(path, bytes.NewReader(b), &resp)
}
