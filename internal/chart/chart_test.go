package chart

import (
	"bytes"
	"strings"
	"testing"
)

func TestBarPNGDiffersByValue(t *testing.T) {
	low, err := BarPNG([]int{1, 2})
	if err != nil || !bytes.HasPrefix(low, []byte{0x89, 'P', 'N', 'G'}) {
		t.Fatal(err, len(low))
	}
	high, err := BarPNG([]int{1, 40})
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(low, high) {
		t.Fatal("different values produced the same image")
	}
	if _, err := BarPNG(nil); err == nil {
		t.Fatal("empty chart should fail")
	}
}

func TestAttachUsesOnlyGivenNumbers(t *testing.T) {
	draft := map[string]any{
		"sections": []any{
			map[string]any{
				"heading": "一周",
				"chart": map[string]any{
					"labels": []any{"周一", "周二"},
					"values": []any{float64(3), float64(9)},
				},
			},
		},
	}
	n := Attach(draft, func(png []byte) (string, error) {
		if !bytes.HasPrefix(png, []byte{0x89, 'P', 'N', 'G'}) {
			t.Fatal("not png")
		}
		return "https://mmbiz.qpic.cn/mock/chart", nil
	})
	if n != 1 {
		t.Fatal(n)
	}
	section := draft["sections"].([]any)[0].(map[string]any)
	if section["image_url"] != "https://mmbiz.qpic.cn/mock/chart" {
		t.Fatal(section["image_url"])
	}
	paragraphs := section["paragraphs"].([]any)
	text := paragraphs[0].(string)
	if !strings.Contains(text, "周一 3") || !strings.Contains(text, "周二 9") || strings.Contains(text, "10") {
		t.Fatal(text)
	}
}
