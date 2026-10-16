package documents

import (
	"bytes"
	"encoding/binary"
	"image/color"
	"image/png"
	"testing"
)

func TestClipboardDIBRejectsTruncatedAlphaMask(t *testing.T) {
	for headerSize := 52; headerSize < 56; headerSize++ {
		data := make([]byte, headerSize)
		binary.LittleEndian.PutUint32(data[0:4], uint32(headerSize))
		binary.LittleEndian.PutUint32(data[4:8], 1)
		binary.LittleEndian.PutUint32(data[8:12], 1)
		binary.LittleEndian.PutUint16(data[12:14], 1)
		binary.LittleEndian.PutUint16(data[14:16], 32)
		binary.LittleEndian.PutUint32(data[16:20], 6)
		binary.LittleEndian.PutUint32(data[40:44], 0x00ff0000)
		binary.LittleEndian.PutUint32(data[44:48], 0x0000ff00)
		binary.LittleEndian.PutUint32(data[48:52], 0x000000ff)
		if _, err := dibToPNG(data); err == nil {
			t.Errorf("header length %d accepted a missing alpha mask", headerSize)
		}
	}
}

func TestClipboardDIBKeepsValidAlphaMasks(t *testing.T) {
	for _, headerSize := range []int{40, 52, 56} {
		data := make([]byte, 60)
		binary.LittleEndian.PutUint32(data[0:4], uint32(headerSize))
		binary.LittleEndian.PutUint32(data[4:8], 1)
		binary.LittleEndian.PutUint32(data[8:12], 1)
		binary.LittleEndian.PutUint16(data[12:14], 1)
		binary.LittleEndian.PutUint16(data[14:16], 32)
		binary.LittleEndian.PutUint32(data[16:20], 6)
		binary.LittleEndian.PutUint32(data[40:44], 0x00ff0000)
		binary.LittleEndian.PutUint32(data[44:48], 0x0000ff00)
		binary.LittleEndian.PutUint32(data[48:52], 0x000000ff)
		binary.LittleEndian.PutUint32(data[52:56], 0xff000000)
		copy(data[56:60], []byte{31, 61, 91, 127})
		encoded, err := dibToPNG(data)
		if err != nil {
			t.Fatalf("valid header %d: %v", headerSize, err)
		}
		decoded, err := png.Decode(bytes.NewReader(encoded))
		if err != nil {
			t.Fatal(err)
		}
		if pixel := color.NRGBAModel.Convert(decoded.At(0, 0)).(color.NRGBA); pixel != (color.NRGBA{R: 91, G: 61, B: 31, A: 127}) {
			t.Errorf("valid header %d changed pixel alpha/color: %#v", headerSize, pixel)
		}
	}
}
