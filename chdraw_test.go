package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// testdata/uncompressed.chd: chdman createcd -c none of testdata/uncompressed.bin
// (track 1: 5 MODE2_RAW frames, track 2: 3 AUDIO frames, both RW_RAW; chdman
// pads each track to 4 frames).
func TestVerifyUncompressedCHD(t *testing.T) {
	chd := filepath.Join("testdata", "uncompressed.chd")
	bin := filepath.Join("testdata", "uncompressed.bin")
	var n int64
	if err := verifyUncompressedCHD(chd, bin, nil, func(b int64) { n += b }); err != nil {
		t.Fatal(err)
	}
	if n != 8*2448 {
		t.Fatalf("checked %d bytes, want %d", n, 8*2448)
	}

	orig, _ := os.ReadFile(bin)
	bad := filepath.Join(t.TempDir(), "bad.bin")
	for _, c := range []struct {
		off  int
		want string
	}{
		{6*2448 + 2352 + 10, "track 2 frame 1 (subchannel)"},
		{2*2448 + 100, "track 1 frame 2 (data)"},
	} {
		b := append([]byte(nil), orig...)
		b[c.off] ^= 0x40
		os.WriteFile(bad, b, 0644)
		err := verifyUncompressedCHD(chd, bad, nil, nil)
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Fatalf("got %v, want %q", err, c.want)
		}
	}
	os.WriteFile(bad, orig[:len(orig)-2448], 0644)
	if err := verifyUncompressedCHD(chd, bad, nil, nil); err == nil {
		t.Fatal("short BIN accepted")
	}
	os.WriteFile(bad, append(append([]byte(nil), orig...), 1), 0644)
	if err := verifyUncompressedCHD(chd, bad, nil, nil); err == nil {
		t.Fatal("long BIN accepted")
	}
}
