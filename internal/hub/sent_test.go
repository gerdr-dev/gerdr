package hub

import "testing"

// An unchanged summary isn't pushed again; a changed one is, and so is one for a session removed and back.
func TestSameAsSent(t *testing.T) {
	h := &Hub{}
	a := Summary{Status: "running", LastSeq: 1}
	a.ID = "s1"
	if h.sameAsSent(a) {
		t.Fatal("first summary counted as already sent")
	}
	if !h.sameAsSent(a) {
		t.Fatal("identical summary would be pushed again")
	}
	b := a
	b.LastSeq = 2
	if h.sameAsSent(b) {
		t.Fatal("changed summary dropped")
	}
	h.forgetSent("s1")
	if h.sameAsSent(b) {
		t.Fatal("summary after forgetSent dropped")
	}
}
