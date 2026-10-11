package cover

import (
	"bytes"
	"fmt"
	"image"
	_ "image/gif"
	"image/jpeg"
	_ "image/png"
)

const (
	softCoverLimit = 2 << 20
	hardCoverLimit = 10 << 20
)

// PrepareCover 按文件头识别 JPEG / PNG。微信封面接受 jpg 和 png。
// 超过约 2MB 时重新编码为 JPEG；无法识别且超过 10MB 时返回错误。
func PrepareCover(raw []byte) (body []byte, filename, contentType string, err error) {
	if len(raw) == 0 {
		return nil, "", "", fmt.Errorf("封面为空")
	}
	kind := sniffImage(raw)
	if (kind == "jpeg" || kind == "png") && len(raw) <= softCoverLimit {
		return namedCover(raw, kind)
	}
	img, _, decErr := image.Decode(bytes.NewReader(raw))
	if decErr != nil {
		if (kind == "jpeg" || kind == "png") && len(raw) <= hardCoverLimit {
			return namedCover(raw, kind)
		}
		return nil, "", "", fmt.Errorf("封面无法识别")
	}
	encoded, encErr := encodeJPEGUnder(img, softCoverLimit)
	if encErr == nil {
		return encoded, "cover.jpg", "image/jpeg", nil
	}
	if (kind == "jpeg" || kind == "png") && len(raw) <= hardCoverLimit {
		return namedCover(raw, kind)
	}
	return nil, "", "", encErr
}

func namedCover(raw []byte, kind string) ([]byte, string, string, error) {
	if kind == "jpeg" {
		return raw, "cover.jpg", "image/jpeg", nil
	}
	return raw, "cover.png", "image/png", nil
}

func sniffImage(raw []byte) string {
	if len(raw) >= 3 && raw[0] == 0xff && raw[1] == 0xd8 && raw[2] == 0xff {
		return "jpeg"
	}
	if len(raw) >= 8 && bytes.Equal(raw[:8], []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n'}) {
		return "png"
	}
	return ""
}

func encodeJPEGUnder(img image.Image, limit int) ([]byte, error) {
	var last []byte
	for _, quality := range []int{85, 70, 50, 30} {
		var buf bytes.Buffer
		if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: quality}); err != nil {
			return nil, fmt.Errorf("封面重新编码失败")
		}
		last = buf.Bytes()
		if len(last) <= limit {
			return last, nil
		}
	}
	if len(last) <= hardCoverLimit {
		return last, nil
	}
	return nil, fmt.Errorf("封面超过大小限制")
}
