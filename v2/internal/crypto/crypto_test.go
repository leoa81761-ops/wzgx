package crypto

import "testing"

const mk = "0123456789abcdef0123456789abcdef"

func TestSealOpen(t *testing.T) {
	b, err := NewBox(mk)
	if err != nil {
		t.Fatal(err)
	}
	s1, _ := b.Seal("tvly-secret")
	s2, _ := b.Seal("tvly-secret")
	if s1 == s2 {
		t.Fatal("nonce not random")
	}
	got, err := b.Open(s1)
	if err != nil || got != "tvly-secret" {
		t.Fatalf("got %q %v", got, err)
	}
	other, _ := NewBox(mk + "x")
	if _, err := other.Open(s1); err == nil {
		t.Fatal("wrong key must fail")
	}
	if _, err := b.Open("garbage"); err == nil {
		t.Fatal("garbage must fail")
	}
}

func TestMask(t *testing.T) {
	cases := map[string]string{"": "", "short": "****", "sk-1234567890abcd": "sk-****abcd"}
	for in, want := range cases {
		if got := Mask(in); got != want {
			t.Errorf("Mask(%q)=%q want %q", in, got, want)
		}
	}
}
