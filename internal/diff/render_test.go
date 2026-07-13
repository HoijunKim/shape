package diff

import (
	"bytes"
	"strings"
	"testing"
)

func TestRenderTextMarksBreaking(t *testing.T) {
	d := DiffResult{
		Old: "old", New: "new", Compared: 3, Removed: 1, Changed: 1, Breaking: 2,
		Caveats: []string{"small sample"},
		Changes: []Change{
			{Path: "tags[]", Kind: Changed, Breaking: false, Details: []Detail{{Reason: ReasonEnum, Message: `enum -"beta"`}}},
			{Path: "email", Kind: Removed, Breaking: true, Details: []Detail{{Reason: ReasonPresence, Breaking: true, Message: "removed (was always-present)"}}},
		},
	}
	var b bytes.Buffer
	RenderText(&b, d)
	out := b.String()
	if !strings.Contains(out, "2 breaking") {
		t.Errorf("missing breaking count:\n%s", out)
	}
	if !strings.Contains(out, "! small sample") {
		t.Errorf("missing caveat line:\n%s", out)
	}
	if !strings.Contains(out, "BREAK") || !strings.Contains(out, "email") {
		t.Errorf("missing breaking marker for email:\n%s", out)
	}
	// email (breaking) must be listed before tags[] (non-breaking).
	if strings.Index(out, "email") > strings.Index(out, "tags[]") {
		t.Errorf("breaking change should sort first:\n%s", out)
	}
}

func TestRenderTextNoChanges(t *testing.T) {
	var b bytes.Buffer
	RenderText(&b, DiffResult{Old: "a", New: "b", Compared: 5})
	if !strings.Contains(b.String(), "no changes") {
		t.Errorf("expected 'no changes', got:\n%s", b.String())
	}
}
