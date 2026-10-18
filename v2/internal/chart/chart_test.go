package chart

import (
	"bytes"
	"image/png"
	"os"
	"testing"

	"gonghao/internal/render"
)

func TestBar(t *testing.T) {
	spec := render.ChartSpec{Title: "三款台灯的照度对比", Labels: []string{"甲款护眼灯", "乙款", "丙款学生灯"},
		Series: []Series{{Name: "照度", Unit: "lx", Values: []float64{300, 500, 750}}, {Name: "实测", Unit: "lx", Values: []float64{280, 510.5, 700}}}}
	b, err := Bar(spec)
	if err != nil {
		t.Fatal(err)
	}
	im, err := png.Decode(bytes.NewReader(b))
	if err != nil || im.Bounds().Dx() != 1080 {
		t.Fatal(err)
	}
	if p := os.Getenv("CHART_OUT"); p != "" {
		os.WriteFile(p, b, 0o644)
	}
	if _, err := Bar(render.ChartSpec{Title: "x", Labels: []string{"𠀀"}, Series: []Series{{Values: []float64{1}}}}); err == nil {
		t.Fatal("missing glyph accepted")
	}
}

type Series = render.Series
