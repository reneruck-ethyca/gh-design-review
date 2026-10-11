// Package diff turns a unified-diff patch into RIGHT-side commentable ranges.
package diff

import (
	"regexp"
	"strconv"
	"strings"
)

type Hunk struct {
	Start int `json:"start"`
	End   int `json:"end"`
}

var header = regexp.MustCompile(`^@@ -\d+(?:,\d+)? \+(\d+)(?:,(\d+))? @@`)

// Parse returns the RIGHT-side hunk ranges (1-based, inclusive) and the lines
// added in the patch.
func Parse(patch string) (hunks []Hunk, added []int) {
	hunks, added = []Hunk{}, []int{}
	right := 0
	for _, l := range strings.Split(patch, "\n") {
		if m := header.FindStringSubmatch(l); m != nil {
			c, _ := strconv.Atoi(m[1])
			d := 1
			if m[2] != "" {
				d, _ = strconv.Atoi(m[2])
			}
			right = c
			if d > 0 {
				hunks = append(hunks, Hunk{c, c + d - 1})
			}
			continue
		}
		if l == "" || right == 0 {
			continue
		}
		switch l[0] {
		case '+':
			added = append(added, right)
			right++
		case ' ':
			right++
		}
	}
	return hunks, added
}
