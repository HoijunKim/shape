package profile

import (
	"encoding/json"
	"testing"
)

func decode(t *testing.T, s string) any {
	t.Helper()
	dec := json.NewDecoder(stringReader(s))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		t.Fatalf("decode %q: %v", s, err)
	}
	return v
}

func TestKindOf(t *testing.T) {
	cases := []struct {
		in   string
		want JSONKind
	}{
		{`null`, KindNull},
		{`true`, KindBool},
		{`42`, KindInt},
		{`4.2`, KindFloat},
		{`1e3`, KindFloat},
		{`"hi"`, KindString},
		{`[1,2]`, KindArray},
		{`{"a":1}`, KindObject},
	}
	for _, c := range cases {
		if got := KindOf(decode(t, c.in)); got != c.want {
			t.Errorf("KindOf(%s) = %s, want %s", c.in, got, c.want)
		}
	}
}
