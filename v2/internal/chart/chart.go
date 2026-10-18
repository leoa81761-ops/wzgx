// Package chart draws a simple horizontal bar chart as a PNG, in Go, with an embedded Chinese
// font (Noto Sans CJK SC, subset to GB2312, SIL Open Font License; see font/LICENSE-OFL.txt).
// Labels with characters outside the font are refused rather than drawn as boxes.
package chart

import (
	"bytes"
	_ "embed"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"math"
	"strconv"
	"strings"
	"sync"

	"golang.org/x/image/font"
	"golang.org/x/image/font/opentype"
	"golang.org/x/image/font/sfnt"
	"golang.org/x/image/math/fixed"

	"gonghao/internal/render"
)

//go:embed font/NotoSansSC-chart.ttf
var fontData []byte

var (
	once    sync.Once
	theFont *opentype.Font
	fontErr error
)

func loadFont() (*opentype.Font, error) {
	once.Do(func() { theFont, fontErr = opentype.Parse(fontData) })
	return theFont, fontErr
}

var palette = []color.RGBA{{0x3b, 0x7d, 0xd8, 255}, {0xf0, 0xa3, 0x3b, 255}, {0x4c, 0xaf, 0x7d, 255}}

const width = 1080

// Bar draws the chart. It never panics on odd data; it returns an error instead.
func Bar(spec render.ChartSpec) ([]byte, error) {
	if len(spec.Labels) == 0 || len(spec.Series) == 0 {
		return nil, errors.New("没有数据")
	}
	f, err := loadFont()
	if err != nil {
		return nil, err
	}
	var buf sfnt.Buffer
	texts := append([]string{spec.Title}, spec.Labels...)
	for _, s := range spec.Series {
		texts = append(texts, s.Name, s.Unit)
	}
	for _, t := range texts {
		for _, r := range t {
			if r == ' ' {
				continue
			}
			if gi, err := f.GlyphIndex(&buf, r); err != nil || gi == 0 {
				return nil, fmt.Errorf("字体里没有「%c」", r)
			}
		}
	}
	face := func(size float64) font.Face {
		fc, _ := opentype.NewFace(f, &opentype.FaceOptions{Size: size, DPI: 72, Hinting: font.HintingNone})
		return fc
	}
	titleF, labelF, valueF := face(34), face(26), face(24)
	measure := func(fc font.Face, s string) int { return font.MeasureString(fc, s).Ceil() }

	const pad, barH, gap, rowGap = 40, 34, 6, 22
	n, ns := len(spec.Labels), len(spec.Series)
	labelW := 0
	for _, l := range spec.Labels {
		labelW = max(labelW, measure(labelF, l))
	}
	labelW = min(labelW, 380) + 20
	top := pad + 50
	if ns > 1 {
		top += 44 // legend
	}
	rowH := ns*barH + (ns-1)*gap
	height := top + n*(rowH+rowGap) + pad
	img := image.NewRGBA(image.Rect(0, 0, width, height))
	draw.Draw(img, img.Bounds(), &image.Uniform{color.White}, image.Point{}, draw.Src)

	text := func(fc font.Face, s string, x, y int, c color.Color) {
		d := &font.Drawer{Dst: img, Src: image.NewUniform(c), Face: fc, Dot: fixed.P(x, y)}
		d.DrawString(s)
	}
	dark, grey := color.RGBA{0x22, 0x22, 0x22, 255}, color.RGBA{0x66, 0x66, 0x66, 255}
	text(titleF, clipTo(titleF, spec.Title, width-2*pad), pad, pad+30, dark)
	if ns > 1 {
		x := pad
		for i, s := range spec.Series {
			fill(img, x, pad+62, x+22, pad+84, palette[i%len(palette)])
			name := s.Name
			if s.Unit != "" {
				name += "（" + s.Unit + "）"
			}
			text(labelF, name, x+30, pad+82, grey)
			x += 30 + measure(labelF, name) + 36
		}
	}
	maxV := 0.0
	for _, s := range spec.Series {
		for _, v := range s.Values {
			maxV = math.Max(maxV, v)
		}
	}
	if maxV <= 0 {
		return nil, errors.New("数值都是 0")
	}
	valueW := 0
	for _, s := range spec.Series {
		for _, v := range s.Values {
			valueW = max(valueW, measure(valueF, fmtNum(v)+s.Unit))
		}
	}
	x0 := pad + labelW
	barMax := width - pad - x0 - valueW - 16
	if barMax < 200 {
		return nil, errors.New("标签太长，画不下")
	}
	axis := color.RGBA{0xdd, 0xdd, 0xdd, 255}
	fill(img, x0, top-8, x0+2, height-pad+4, axis)
	for i, l := range spec.Labels {
		y := top + i*(rowH+rowGap)
		text(labelF, clipTo(labelF, l, labelW-20), pad, y+rowH/2+9, dark)
		for k, s := range spec.Series {
			by := y + k*(barH+gap)
			w := int(math.Round(s.Values[i] / maxV * float64(barMax)))
			if s.Values[i] > 0 && w < 3 {
				w = 3
			}
			fill(img, x0+2, by, x0+2+w, by+barH, palette[k%len(palette)])
			text(valueF, fmtNum(s.Values[i])+s.Unit, x0+2+w+10, by+barH/2+9, grey)
		}
	}
	var out bytes.Buffer
	if err := png.Encode(&out, img); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

func fill(img *image.RGBA, x0, y0, x1, y1 int, c color.Color) {
	draw.Draw(img, image.Rect(x0, y0, x1, y1), &image.Uniform{c}, image.Point{}, draw.Src)
}

func fmtNum(v float64) string {
	if v == math.Trunc(v) && math.Abs(v) < 1e15 {
		return strconv.FormatInt(int64(v), 10)
	}
	return strings.TrimRight(strings.TrimRight(strconv.FormatFloat(v, 'f', 2, 64), "0"), ".")
}

func clipTo(fc font.Face, s string, w int) string {
	if font.MeasureString(fc, s).Ceil() <= w {
		return s
	}
	r := []rune(s)
	for len(r) > 1 && font.MeasureString(fc, string(r)+"…").Ceil() > w {
		r = r[:len(r)-1]
	}
	return string(r) + "…"
}
