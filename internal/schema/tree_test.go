package schema

import (
	"testing"

	"github.com/hoijunkim/shape/internal/profile"
)

func TestParsePath(t *testing.T) {
	cases := []struct {
		in   string
		want []step
	}{
		{"$", nil},
		{"", nil},
		{"email", []step{{key: "email"}}},
		{"user.email", []step{{key: "user"}, {key: "email"}}},
		{"items[].price", []step{{key: "items"}, {isElem: true}, {key: "price"}}},
		{"[]", []step{{isElem: true}}},
		{"matrix[][]", []step{{key: "matrix"}, {isElem: true}, {isElem: true}}},
	}
	for _, c := range cases {
		got := parsePath(c.in)
		if len(got) != len(c.want) {
			t.Errorf("parsePath(%q) = %v, want %v", c.in, got, c.want)
			continue
		}
		for i := range got {
			if got[i] != c.want[i] {
				t.Errorf("parsePath(%q)[%d] = %v, want %v", c.in, i, got[i], c.want[i])
			}
		}
	}
}

func TestBuildTree(t *testing.T) {
	fields := []profile.FieldProfile{
		{Path: "id"},
		{Path: "user"},
		{Path: "user.name"},
		{Path: "tags"},
		{Path: "tags[]"},
	}
	root := buildTree(fields)
	if root.props["id"] == nil || root.props["id"].profile == nil {
		t.Fatal("id not attached")
	}
	if root.props["user"].props["name"] == nil {
		t.Fatal("user.name not nested under user")
	}
	tags := root.props["tags"]
	if tags.elem == nil || tags.elem.profile == nil {
		t.Fatal("tags[] not attached to tags.elem")
	}
	if !tags.elem.underArr {
		t.Error("tags[] element should be marked underArr")
	}
}
