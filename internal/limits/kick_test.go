package limits

import "testing"

func TestKickCoalescesAndNeverBlocks(t *testing.T) {
	var e Enforcer
	for range 5 {
		e.Kick() // no Run yet: must not block
	}
	if n := len(e.kicks()); n != 1 {
		t.Fatalf("pending kicks = %d, want 1", n)
	}
	<-e.kicks()
	if n := len(e.kicks()); n != 0 {
		t.Fatalf("pending kicks after receive = %d, want 0", n)
	}
}
