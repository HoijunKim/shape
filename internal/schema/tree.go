package schema

import (
	"strings"

	"github.com/hoijunkim/shape/internal/profile"
)

type step struct {
	key    string
	isElem bool
}

// parsePath turns a flattened profile path into ordered tree steps.
func parsePath(p string) []step {
	if p == "$" || p == "" {
		return nil
	}
	var out []step
	for _, seg := range strings.Split(p, ".") {
		n := 0
		for strings.HasSuffix(seg, "[]") {
			seg = seg[:len(seg)-2]
			n++
		}
		if seg != "" {
			out = append(out, step{key: seg})
		}
		for i := 0; i < n; i++ {
			out = append(out, step{isElem: true})
		}
	}
	return out
}

type node struct {
	props    map[string]*node
	elem     *node
	profile  *profile.FieldProfile
	underArr bool
}

func newNode() *node { return &node{props: map[string]*node{}} }

// buildTree threads every field down its parsed path into an intermediate tree.
func buildTree(fields []profile.FieldProfile) *node {
	root := newNode()
	for i := range fields {
		fp := &fields[i]
		cur := root
		under := false
		for _, s := range parsePath(fp.Path) {
			if s.isElem {
				if cur.elem == nil {
					cur.elem = newNode()
				}
				cur = cur.elem
				under = true
			} else {
				ch := cur.props[s.key]
				if ch == nil {
					ch = newNode()
					cur.props[s.key] = ch
				}
				cur = ch
			}
			cur.underArr = cur.underArr || under
		}
		cur.profile = fp
	}
	return root
}
