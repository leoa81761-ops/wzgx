package cover

import (
	"bytes"
	"image"
	"image/jpeg"
	"testing"
)

func TestPrepareCoverNamesJPEGAndPNG(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 16, 9))
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: 80}); err != nil {
		t.Fatal(err)
	}
	body, name, ctype, err := PrepareCover(buf.Bytes())
	if err != nil || name != "cover.jpg" || ctype != "image/jpeg" || !bytes.HasPrefix(body, []byte{0xff, 0xd8, 0xff}) {
		t.Fatalf("%v %s %s %d", err, name, ctype, len(body))
	}
	pngBody, name, ctype, err := PrepareCover(CoverPNG("simple"))
	if err != nil || name != "cover.png" || ctype != "image/png" || !bytes.HasPrefix(pngBody, []byte{0x89, 'P', 'N', 'G'}) {
		t.Fatalf("%v %s %s", err, name, ctype)
	}
}

func TestPrepareCoverRejectsOversizedUnknownJPEG(t *testing.T) {
	raw := make([]byte, (10<<20)+8)
	raw[0], raw[1], raw[2] = 0xff, 0xd8, 0xff
	if _, _, _, err := PrepareCover(raw); err == nil {
		t.Fatal("expected size error")
	}
}
