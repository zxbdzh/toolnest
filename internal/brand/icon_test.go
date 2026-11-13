package brand

import (
	"encoding/binary"
	"testing"
)

func TestWindowsIconIncludesHighDPIAndTransparentEdges(t *testing.T) {
	icon := ICO()
	if binary.LittleEndian.Uint16(icon[2:]) != 1 || binary.LittleEndian.Uint16(icon[4:]) != 9 {
		t.Fatal("missing Windows multi-resolution icon")
	}
	for index := 0; index < 9; index++ {
		entry := icon[6+index*16:]
		start, size := int(binary.LittleEndian.Uint32(entry[12:])), int(binary.LittleEndian.Uint32(entry[8:]))
		if start < 150 || start+size > len(icon) || binary.LittleEndian.Uint32(icon[start:]) != 40 {
			t.Fatal("invalid DIB resource bounds")
		}
	}
	if Image(32, false).NRGBAAt(0, 0).A != 0 || Image(32, false).NRGBAAt(16, 16).A == 0 {
		t.Fatal("icon silhouette lost transparency or readable center")
	}
}
