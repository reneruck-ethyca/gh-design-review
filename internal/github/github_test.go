package github

import (
	"encoding/json"
	"testing"
)

func TestThreadNodeDecodesCommentsConnection(t *testing.T) {
	var n threadNode
	in := `{"id":"T1","path":"docs/a.md","comments":{"nodes":[{"id":"C1","body":"hi","author":{"login":"rene"}}]}}`
	if err := json.Unmarshal([]byte(in), &n); err != nil {
		t.Fatal(err)
	}
	if len(n.Comments.Nodes) != 1 || n.Comments.Nodes[0].Author.Login != "rene" || n.Path != "docs/a.md" {
		t.Fatalf("unexpected decode: %+v", n)
	}
}
