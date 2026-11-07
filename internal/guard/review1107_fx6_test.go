package guard

import "testing"

// B-A1 (review 2026-11-07): a change typed after a key token's {enter} is a
// command of its own and is flagged like one; a line that only reads
// through its tokens is not.
func TestCheckLooksPastEnterTokens(t *testing.T) {
	hits := Check([]string{"show clock{enter}reload{enter}"})
	if len(hits) != 1 || hits[0].Reason != ReasonReload || hits[0].Index != 0 {
		t.Fatalf("reload after {enter} not flagged: %+v", hits)
	}
	hits = Check([]string{"show version{enter}", "{ctrl+c}", "show ip route{enter}show arp{enter}"})
	if len(hits) != 0 {
		t.Errorf("reads with tokens flagged: %+v", hits)
	}
	// The control key itself is no command: "{ctrl+z}" followed by a
	// write is the write.
	hits = Check([]string{"{ctrl+z}{enter}wr{enter}"})
	if len(hits) != 1 || hits[0].Reason != ReasonWrite {
		t.Errorf("write after {ctrl+z}{enter} not flagged: %+v", hits)
	}
	// A key token in the middle of a line (Cisco's escape out of a ping)
	// does not split a read into a change.
	if hits := Check([]string{"ping 192.0.2.1{ctrl+^}x"}); len(hits) != 0 {
		t.Errorf("ping with an escape flagged: %+v", hits)
	}
}

// F-B-4: "reset session" (ScreenOS: every session dropped) is not a read.
func TestResetSessionIsNotARead(t *testing.T) {
	if hits := Check([]string{"reset session"}); len(hits) != 1 || hits[0].Reason != ReasonErase {
		t.Errorf("reset session: %+v", hits)
	}
	if hits := Check([]string{"reset counters", "reset arp"}); len(hits) != 0 {
		t.Errorf("reset counters/arp flagged: %+v", hits)
	}
}
