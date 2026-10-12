package growth

import "testing"

func TestClassifyAndAdvance(t *testing.T) {
	if status, _ := Classify(true, false, false); status != StatusRevoked {
		t.Fatal(status)
	}
	if status, _ := Classify(false, true, false); status != StatusFrozen {
		t.Fatal(status)
	}
	if status, _ := Classify(false, false, false); status != StatusPending {
		t.Fatal(status)
	}
	status, _ := Advance(StatusPending, 2, true, false, false, 0, 3, 3)
	if status != StatusPending {
		t.Fatal("drafts")
	}
	status, reason := Advance(StatusPending, 3, true, false, false, 3, 3, 3)
	if status != StatusFrozen || reason == "" {
		t.Fatal(status, reason)
	}
	status, _ = Advance(StatusPending, 3, true, false, false, 1, 3, 3)
	if status != StatusValid {
		t.Fatal(status)
	}
	status, _ = Advance(StatusPending, 3, true, true, false, 0, 3, 3)
	if status != StatusRevoked {
		t.Fatal(status)
	}
	if Bonus(0, 10, "free", DefaultRules()) != 0 || Bonus(3, 10, "standard", DefaultRules()) != 0 {
		t.Fatal("tier")
	}
	if Bonus(100, 10, "free", DefaultRules()) != 20 {
		t.Fatal(Bonus(100, 10, "free", DefaultRules()))
	}
	if !VirtualPhone("17000001111") || VirtualPhone("13800138000") {
		t.Fatal("phone")
	}
	if IPPrefix("1.2.3.4") != "1.2.3" || DeviceHash("") != "" || DeviceHash("abc") == "abc" {
		t.Fatal("hash")
	}
}