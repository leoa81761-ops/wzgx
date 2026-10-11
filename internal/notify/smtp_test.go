package notify

import (
	"context"
	"errors"
	"testing"
)

func TestSMTPNoopWithoutHostOrEmail(t *testing.T) {
	ctx := context.Background()
	if err := (SMTP{}).Send(ctx, Message{Account: "reader@example.com", Title: "失败"}); err != nil {
		t.Fatal(err)
	}
	if err := (SMTP{Host: "smtp.example.com", From: "bot@example.com"}).Send(ctx, Message{Account: "13800138000", Title: "失败"}); err != nil {
		t.Fatal(err)
	}
}

type stubSender struct{ err error }

func (s stubSender) Send(ctx context.Context, msg Message) error { return s.err }

func TestFanoutKeepsGoing(t *testing.T) {
	err := Fanout{Senders: []Sender{stubSender{err: errors.New("hook")}, SMTP{}}}.Send(context.Background(), Message{Title: "失败"})
	if err == nil || err.Error() != "hook" {
		t.Fatal(err)
	}
}
