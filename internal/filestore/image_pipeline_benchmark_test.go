package filestore

import (
	"encoding/base64"
	"encoding/binary"
	"testing"
)

// The same benchmark file runs on the unchanged baseline and candidate.
// The RIFF JUNK chunk models a large encoded input with a small decoded image,
// isolating duplicate encoded-byte buffering from pixel resampling cost.
func BenchmarkThumbnailBytePipeline(b *testing.B) {
	still, err := base64.StdEncoding.DecodeString("UklGRiIAAABXRUJQVlA4IBYAAAAwAQCdASoBAAEADsD+JaQAA3AAAAAA")
	if err != nil {
		b.Fatal(err)
	}
	for _, tc := range []struct {
		name    string
		padding int
	}{{"small", 0}, {"metadata_1MiB", 1 << 20}} {
		data := append([]byte(nil), still...)
		if tc.padding > 0 {
			data = appendWebPChunk(data, "JUNK", make([]byte, tc.padding))
			binary.LittleEndian.PutUint32(data[4:8], uint32(len(data)-8))
		}
		service := &Service{options: Options{MaximumBytes: 8 << 20, MaximumImagePixels: 4096}}
		b.Run(tc.name, func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				if _, err := service.RenderThumbnailData(data, "image/webp", "16x16"); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
