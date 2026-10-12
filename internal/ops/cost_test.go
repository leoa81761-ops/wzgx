package ops

import "testing"

func TestDecideRespectsCapAndAction(t *testing.T) {
	p := DefaultPolicy()
	if Decide(10_000, p) != "" {
		t.Fatal("zero cap is off")
	}
	p.MonthCapCents = 100
	if Decide(99, p) != "" {
		t.Fatal("under cap")
	}
	if Decide(100, p) != ActionDegrade {
		t.Fatal("default action")
	}
	p.Action = ActionPause
	if Decide(100, p) != ActionPause {
		t.Fatal("pause")
	}
	p.Action = "nope"
	if Normalize(p).Action != ActionDegrade {
		t.Fatal("unknown action")
	}
}

func TestChatCentsRoundsUp(t *testing.T) {
	if ChatCents(0, 2) != 0 || ChatCents(1, 2) != 1 || ChatCents(1000, 2) != 2 || ChatCents(1500, 2) != 3 {
		t.Fatalf("%d %d %d", ChatCents(1, 2), ChatCents(1000, 2), ChatCents(1500, 2))
	}
}
