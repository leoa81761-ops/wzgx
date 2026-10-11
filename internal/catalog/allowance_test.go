package catalog

import "testing"

func TestImageAllowanceByTier(t *testing.T) {
	cover, inline := ImageAllowance("free")
	if cover || inline != 0 {
		t.Fatal(cover, inline)
	}
	cover, inline = ImageAllowance("standard")
	if !cover || inline != 1 {
		t.Fatal(cover, inline)
	}
	cover, inline = ImageAllowance("premium")
	if !cover || inline != 3 {
		t.Fatal(cover, inline)
	}
	if !TierAllows("lifetime", "standard") || TierAllows("free", "standard") {
		t.Fatal("tier rank")
	}
}
