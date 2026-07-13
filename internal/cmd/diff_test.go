package cmd

import (
	"bytes"
	"encoding/json"
	"errors"
	"testing"
)

func runDiff(t *testing.T, args ...string) (string, error) {
	t.Helper()
	root := NewRootCmd()
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetArgs(append([]string{"diff"}, args...))
	err := root.Execute()
	return out.String(), err
}

func exitCodeOf(err error) int {
	var ec interface{ ExitCode() int }
	if errors.As(err, &ec) {
		return ec.ExitCode()
	}
	return -1
}

func TestDiffJSONReportsBreaking(t *testing.T) {
	out, err := runDiff(t, "--json", "--fail-on", "none", "testdata/diff_old.ndjson", "testdata/diff_new.ndjson")
	if err != nil {
		t.Fatalf("fail-on none must not error: %v", err)
	}
	var d map[string]any
	if e := json.Unmarshal([]byte(out), &d); e != nil {
		t.Fatalf("not JSON: %v\n%s", e, out)
	}
	if d["breaking"].(float64) != 3 {
		t.Errorf("breaking = %v, want 3", d["breaking"])
	}
}

func TestDiffFailOnBreakingExits1(t *testing.T) {
	_, err := runDiff(t, "testdata/diff_old.ndjson", "testdata/diff_new.ndjson")
	if err == nil {
		t.Fatal("default --fail-on breaking must return an error on breaking changes")
	}
	if got := exitCodeOf(err); got != 1 {
		t.Errorf("exit code = %d, want 1", got)
	}
}

func TestDiffFailOnNoneExits0(t *testing.T) {
	_, err := runDiff(t, "--fail-on", "none", "testdata/diff_old.ndjson", "testdata/diff_new.ndjson")
	if err != nil {
		t.Errorf("fail-on none must return nil, got %v", err)
	}
}

func TestDiffInvalidFailOn(t *testing.T) {
	_, err := runDiff(t, "--fail-on", "bogus", "testdata/diff_old.ndjson", "testdata/diff_new.ndjson")
	if err == nil {
		t.Fatal("invalid --fail-on must error")
	}
	if exitCodeOf(err) == 1 {
		t.Error("invalid --fail-on is a usage error (exit 2), not a gate failure (exit 1)")
	}
}
