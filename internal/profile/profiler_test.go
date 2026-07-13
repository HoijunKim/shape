package profile

import "testing"

func fieldByPath(res ProfileResult, path string) (FieldProfile, bool) {
	for _, f := range res.Fields {
		if f.Path == path {
			return f, true
		}
	}
	return FieldProfile{}, false
}

func TestProfilerPresenceAcrossRecords(t *testing.T) {
	p := NewProfiler()
	p.AddRecord(decode(t, `{"a":1,"b":"x"}`))
	p.AddRecord(decode(t, `{"a":2}`))
	res := p.Result()
	if res.Records != 2 {
		t.Fatalf("records = %d, want 2", res.Records)
	}
	a, _ := fieldByPath(res, "a")
	b, _ := fieldByPath(res, "b")
	if a.PresenceRate != 1.0 {
		t.Errorf("a presence = %v, want 1.0", a.PresenceRate)
	}
	if b.PresenceRate != 0.5 {
		t.Errorf("b presence = %v, want 0.5", b.PresenceRate)
	}
}

func TestProfilerArrayPresenceCountedOncePerRecord(t *testing.T) {
	p := NewProfiler()
	p.AddRecord(decode(t, `{"tags":["x","y","z"]}`))
	res := p.Result()
	tags, _ := fieldByPath(res, "tags[]")
	if tags.PresenceRate != 1.0 {
		t.Errorf("tags[] presence = %v, want 1.0 (once per record)", tags.PresenceRate)
	}
	if tags.Observations != 3 {
		t.Errorf("tags[] observations = %d, want 3", tags.Observations)
	}
}

func TestIsTypeDrift(t *testing.T) {
	p := NewProfiler()
	p.AddRecord(decode(t, `{"id":1}`))
	p.AddRecord(decode(t, `{"id":"two"}`))
	res := p.Result()
	id, _ := fieldByPath(res, "id")
	if !IsTypeDrift(id) {
		t.Errorf("expected id to be flagged as drifting (int + string)")
	}

	p2 := NewProfiler()
	p2.AddRecord(decode(t, `{"id":1}`))
	p2.AddRecord(decode(t, `{"id":2.5}`))
	res2 := p2.Result()
	id2, _ := fieldByPath(res2, "id")
	if IsTypeDrift(id2) {
		t.Errorf("int + float should count as one number type, not drift")
	}
}
