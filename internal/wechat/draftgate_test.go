package wechat

import (
	"context"
	"testing"
)

func TestDraftWaitSwitchAndCap(t *testing.T) {
	ResetMock()
	client := Build("wx_mock_gate", "mock-secret", "mock", "", nil, nil)
	if msg, wait := DraftWait(context.Background(), client, 1000); wait || msg != "" {
		t.Fatal(msg, wait)
	}
	SetMockDraftClosed("wx_mock_gate", true)
	msg, wait := DraftWait(context.Background(), client, 1000)
	if !wait || msg == "" {
		t.Fatal("closed switch should wait")
	}
	SetMockDraftClosed("wx_mock_gate", false)
	SetMockDraftCount("wx_mock_gate", 1000)
	msg, wait = DraftWait(context.Background(), client, 1000)
	if !wait || msg == "" {
		t.Fatal("full box should wait")
	}
	if _, wait = DraftWait(context.Background(), client, 0); wait {
		t.Fatal("cap 0 does not block on count")
	}
}
