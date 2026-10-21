package auth_test

import (
	"context"
	"testing"
	"time"

	"gonghao/internal/auth"
	"gonghao/internal/testdb"
)

func TestPGStoreFlow(t *testing.T) {
	pool := testdb.Open(t)
	ctx := context.Background()
	svc := auth.NewService(auth.NewPGStore(pool), time.Hour)
	if _, err := svc.Register(ctx, "a@example.com", "password1", "A", auth.Consent{TermsVersion: "1", PrivacyVersion: "1"}, "1.2.3.4", "ua"); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Register(ctx, "A@EXAMPLE.com", "password1", "", auth.Consent{TermsVersion: "1", PrivacyVersion: "1"}, "", ""); err != auth.ErrEmailTaken {
		t.Fatalf("case-insensitive duplicate: %v", err)
	}
	u, tok, err := svc.Login(ctx, "a@example.com", "password1", "1.2.3.4", "ua")
	if err != nil {
		t.Fatal(err)
	}
	got, err := svc.Authenticate(ctx, tok)
	if err != nil || got.ID != u.ID || got.Role != auth.RoleUser || got.MaxAccounts != 1 {
		t.Fatalf("auth %+v %v", got, err)
	}
	var consents int
	pool.QueryRow(ctx, `SELECT count(*) FROM consent_log WHERE user_id=$1`, u.ID).Scan(&consents)
	if consents != 2 {
		t.Fatalf("consents=%d", consents)
	}
	// raw token is never stored
	var stored int
	pool.QueryRow(ctx, `SELECT count(*) FROM sessions WHERE token_hash=$1`, []byte(tok)).Scan(&stored)
	if stored != 0 {
		t.Fatal("raw token stored")
	}
	if ok, err := svc.Bootstrap(ctx, "root@example.com", "rootpass1"); !ok || err != nil {
		t.Fatal(ok, err)
	}
	_, rootTok, _ := svc.Login(ctx, "root@example.com", "rootpass1", "", "")
	root, _ := svc.Authenticate(ctx, rootTok)
	op, err := svc.CreateOperator(ctx, root, "op@example.com", "oppass123", "运营")
	if err != nil || op.Role != auth.RoleOperator {
		t.Fatal(op, err)
	}
	_, opTok, _ := svc.Login(ctx, "op@example.com", "oppass123", "", "")
	if err := svc.SetOperatorStatus(ctx, root, op.ID, false); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Authenticate(ctx, opTok); err == nil {
		t.Fatal("disabled operator session alive")
	}
	var audits int
	pool.QueryRow(ctx, `SELECT count(*) FROM audit_log`).Scan(&audits)
	if audits < 3 {
		t.Fatalf("audits=%d", audits)
	}
	if err := svc.Logout(ctx, tok); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Authenticate(ctx, tok); err == nil {
		t.Fatal("logout")
	}
}
