package cover

import (
	"bytes"
	"compress/zlib"
	"encoding/binary"
	"hash/crc32"
	"sync"
)

var colors = map[string][3]byte{
	"simple":   {156, 59, 36},
	"fresh":    {31, 106, 77},
	"business": {30, 58, 95},
}

var cache sync.Map

func CoverPNG(templateKey string) []byte {
	rgb, ok := colors[templateKey]
	if !ok {
		rgb = colors["simple"]
	}
	if v, ok := cache.Load(templateKey); ok {
		return v.([]byte)
	}
	png := solidPNG(rgb, 900, 383)
	cache.Store(templateKey, png)
	return png
}

// ContentPNG 是没有配图服务时写入正文的默认小图，经 uploadimg 换成微信 URL。
func ContentPNG() []byte {
	if v, ok := cache.Load("content"); ok {
		return v.([]byte)
	}
	png := solidPNG([3]byte{232, 220, 204}, 640, 360)
	cache.Store("content", png)
	return png
}

func solidPNG(rgb [3]byte, width, height int) []byte {
	row := make([]byte, 1+width*3)
	for x := 0; x < width; x++ {
		copy(row[1+x*3:], rgb[:])
	}
	raw := bytes.Repeat(row, height)
	var idat bytes.Buffer
	zw, _ := zlib.NewWriterLevel(&idat, zlib.BestCompression)
	_, _ = zw.Write(raw)
	_ = zw.Close()
	ihdr := make([]byte, 13)
	binary.BigEndian.PutUint32(ihdr[0:], uint32(width))
	binary.BigEndian.PutUint32(ihdr[4:], uint32(height))
	ihdr[8] = 8
	ihdr[9] = 2
	var buf bytes.Buffer
	buf.Write([]byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n'})
	writeChunk(&buf, "IHDR", ihdr)
	writeChunk(&buf, "IDAT", idat.Bytes())
	writeChunk(&buf, "IEND", nil)
	return buf.Bytes()
}

func writeChunk(buf *bytes.Buffer, tag string, data []byte) {
	var lenb [4]byte
	binary.BigEndian.PutUint32(lenb[:], uint32(len(data)))
	buf.Write(lenb[:])
	buf.WriteString(tag)
	buf.Write(data)
	sum := crc32.NewIEEE()
	_, _ = sum.Write([]byte(tag))
	_, _ = sum.Write(data)
	var crc [4]byte
	binary.BigEndian.PutUint32(crc[:], sum.Sum32())
	buf.Write(crc[:])
}
