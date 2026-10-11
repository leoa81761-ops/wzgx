package wechat

import "testing"

func TestDiffDraft(t *testing.T) {
	same := DiffDraft("标题", "<p>正文</p>", DraftView{Title: "标题", Content: "<p>正文</p>"})
	if same.State != "unchanged" || same.ChangeRatio != 0 {
		t.Fatal(same)
	}
	changed := DiffDraft("标题", "<p>正文</p>", ReadDraft(map[string]any{"news_item": []any{map[string]any{"title": "新标题", "content": "<p>完全不同的一段话</p>"}}}))
	if changed.State != "modified" || changed.ChangeRatio <= 0 {
		t.Fatal(changed)
	}
	missing := DiffDraft("标题", "正文", DraftView{Missing: true})
	if missing.State != "missing" {
		t.Fatal(missing)
	}
	if !MissingDraft(&Error{Code: 40007, Message: "invalid media_id"}) {
		t.Fatal("40007 should be a missing draft")
	}
}
