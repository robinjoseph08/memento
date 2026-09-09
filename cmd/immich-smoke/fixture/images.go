package fixture

import (
	"bytes"
	"encoding/binary"
	"image"
	"image/color"
	"image/jpeg"
	"time"
)

// Photo describes expected EXIF wall-clock time, independently of the upload timestamp.
type Photo struct {
	Filename   string
	CapturedAt string
}

// Photos includes a midnight boundary, two equal capture times, and a later day.
func Photos() []Photo {
	return []Photo{
		{"before-midnight.jpg", "2024-07-01T23:59:59-07:00"},
		{"midnight-a.jpg", "2024-07-02T00:00:00-07:00"},
		{"midnight-b.jpg", "2024-07-02T00:00:00-07:00"},
		{"later-day.jpg", "2024-07-03T12:30:00-07:00"},
	}
}

// JPEG creates distinct JPEGs with DateTimeOriginal and OffsetTimeOriginal EXIF tags.
// No external image tools or downloaded media are needed.
func JPEG(photo Photo, index int) ([]byte, error) {
	captured, err := time.Parse(time.RFC3339, photo.CapturedAt)
	if err != nil {
		return nil, err
	}
	img := image.NewRGBA(image.Rect(0, 0, 64, 48))
	for y := range 48 {
		for x := range 64 {
			img.Set(x, y, color.RGBA{R: uint8((x*3 + index*17) & 255), G: uint8(y * 4), B: uint8((index * 53) & 255), A: 255})
		}
	}
	var encoded bytes.Buffer
	if err := jpeg.Encode(&encoded, img, &jpeg.Options{Quality: 85}); err != nil {
		return nil, err
	}
	// TIFF IFD0 contains the ExifIFD pointer. The Exif IFD holds two ASCII values.
	const tiffLength = 26 + 30 + 20 + 7
	tiff := make([]byte, tiffLength)
	copy(tiff, []byte{'I', 'I', 42, 0, 8, 0, 0, 0})
	binary.LittleEndian.PutUint16(tiff[8:], 1)
	binary.LittleEndian.PutUint16(tiff[10:], 0x8769)
	binary.LittleEndian.PutUint16(tiff[12:], 4)
	binary.LittleEndian.PutUint32(tiff[14:], 1)
	binary.LittleEndian.PutUint32(tiff[18:], 26)
	binary.LittleEndian.PutUint16(tiff[26:], 2)
	for i, tag := range []struct {
		id     uint16
		length uint32
		offset uint32
		value  string
	}{
		{0x9003, 20, 56, captured.Format("2006:01:02 15:04:05")},
		{0x9011, 7, 76, captured.Format("-07:00")},
	} {
		entry := 28 + i*12
		binary.LittleEndian.PutUint16(tiff[entry:], tag.id)
		binary.LittleEndian.PutUint16(tiff[entry+2:], 2)
		binary.LittleEndian.PutUint32(tiff[entry+4:], tag.length)
		binary.LittleEndian.PutUint32(tiff[entry+8:], tag.offset)
		copy(tiff[tag.offset:], tag.value)
	}
	exif := append([]byte("Exif\x00\x00"), tiff...)
	result := append([]byte{0xff, 0xd8, 0xff, 0xe1, 0, 0}, exif...)
	binary.BigEndian.PutUint16(result[4:], tiffLength+6+2)
	return append(result, encoded.Bytes()[2:]...), nil
}
