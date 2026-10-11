package security

import "testing"

func TestFernetCompatibleWithPython(t *testing.T) {
	plain, err := DecryptSecret("dev-master-key-change-me", "gAAAAABqy2KradWPKZy_zWQcTuGgRd7znUW-oKKFt0rWCvV5sCB8mk7TGQqNoxkSJIqgq5KsQn5TlaGF8c6X7ukSKVHmHJYF6Q==")
	if err != nil {
		t.Fatal(err)
	}
	if plain != "hello-secret" {
		t.Fatalf("got %q", plain)
	}
	token, err := EncryptSecret("dev-master-key-change-me", "hello-secret")
	if err != nil {
		t.Fatal(err)
	}
	again, err := DecryptSecret("dev-master-key-change-me", token)
	if err != nil || again != "hello-secret" {
		t.Fatalf("round trip %q %v", again, err)
	}
	if _, err := DecryptSecret("other-key", token); err == nil {
		t.Fatal("wrong key should fail")
	}
}

func TestPasswordAndJWT(t *testing.T) {
	hash, err := HashPassword("password12")
	if err != nil {
		t.Fatal(err)
	}
	if !VerifyPassword("password12", hash) || VerifyPassword("password13", hash) {
		t.Fatal("password verify mismatch")
	}
	token, err := MakeToken(42, "test-jwt-secret", 7)
	if err != nil {
		t.Fatal(err)
	}
	id, err := ReadToken(token, "test-jwt-secret")
	if err != nil || id != 42 {
		t.Fatalf("token %d %v", id, err)
	}
	if _, err := ReadToken(token, "wrong"); err == nil {
		t.Fatal("wrong jwt secret should fail")
	}
}

func TestMaskSecret(t *testing.T) {
	if got := MaskSecret("sk-ant-secret"); got != "****cret" {
		t.Fatal(got)
	}
}
