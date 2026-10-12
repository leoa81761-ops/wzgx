package chart

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
)

// BarPNG 把已经写在稿件里的数字画成柱状图。没有字体库，图里不写字，数字留在正文。
func BarPNG(values []int) ([]byte, error) {
	if len(values) == 0 {
		return nil, errEmpty
	}
	if len(values) > 8 {
		values = values[:8]
	}
	const width, height = 640, 360
	img := image.NewRGBA(image.Rect(0, 0, width, height))
	bg := color.RGBA{248, 244, 238, 255}
	bar := color.RGBA{156, 59, 36, 255}
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			img.SetRGBA(x, y, bg)
		}
	}
	max := 1
	for _, v := range values {
		if v > max {
			max = v
		}
	}
	gap := 16
	slot := (width - gap) / len(values)
	barW := slot - gap
	if barW < 8 {
		barW = 8
	}
	base := height - 28
	for i, v := range values {
		h := 4
		if v > 0 {
			h = v * (base - 24) / max
			if h < 8 {
				h = 8
			}
		}
		x0 := gap + i*slot
		for y := base - h; y < base; y++ {
			for x := x0; x < x0+barW && x < width; x++ {
				img.SetRGBA(x, y, bar)
			}
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

type emptyErr string

func (e emptyErr) Error() string { return string(e) }

const errEmpty emptyErr = "没有可画的数字"

// Attach 把章节里的 chart.values 画成图并交给 upload。数字必须已经在稿件里。
func Attach(draft map[string]any, upload func([]byte) (string, error)) int {
	if draft == nil || upload == nil {
		return 0
	}
	sections, _ := draft["sections"].([]any)
	used := 0
	for _, raw := range sections {
		section, _ := raw.(map[string]any)
		if section == nil {
			continue
		}
		spec, _ := section["chart"].(map[string]any)
		if spec == nil {
			continue
		}
		values := ints(spec["values"])
		if len(values) == 0 {
			continue
		}
		pngBody, err := BarPNG(values)
		if err != nil {
			continue
		}
		url, err := upload(pngBody)
		if err != nil || url == "" {
			continue
		}
		if stringsTrim(section["image_url"]) == "" {
			section["image_url"] = url
		}
		labels := texts(spec["labels"])
		note := "图表数字："
		for i, v := range values {
			if i > 0 {
				note += "，"
			}
			if i < len(labels) && labels[i] != "" {
				note += labels[i] + " "
			}
			note += itoa(v)
		}
		paragraphs, _ := section["paragraphs"].([]any)
		section["paragraphs"] = append(paragraphs, note)
		used++
	}
	return used
}

func ints(v any) []int {
	switch raw := v.(type) {
	case []int:
		return raw
	case []any:
		out := make([]int, 0, len(raw))
		for _, item := range raw {
			switch n := item.(type) {
			case float64:
				out = append(out, int(n))
			case int:
				out = append(out, n)
			case int64:
				out = append(out, int(n))
			}
		}
		return out
	default:
		return nil
	}
}

func texts(v any) []string {
	raw, _ := v.([]any)
	out := make([]string, 0, len(raw))
	for _, item := range raw {
		text, _ := item.(string)
		out = append(out, text)
	}
	return out
}

func stringsTrim(v any) string {
	text, _ := v.(string)
	for len(text) > 0 && (text[0] == ' ' || text[0] == '\n') {
		text = text[1:]
	}
	return text
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [16]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
