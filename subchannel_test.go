package main

import (
	"os"
	"path/filepath"
	"testing"
)

func testQ(lba int) [12]byte {
	var q [12]byte
	q[0] = 0x41 // data track, ADR 1
	q[1] = 0x01
	q[2] = 0x01
	setMSF(&q, 3, lba)
	setMSF(&q, 7, lba+150)
	crc := qCRC(q)
	q[10] = byte(crc >> 8)
	q[11] = byte(crc)
	return q
}

// writeTestBin writes frames raw sectors + raw subchannel. The sector at
// index i carries the Q of LBA i+shift (drive offset); LBAs in bad get a
// broken CRC like LibCrypt.
func writeTestBin(t *testing.T, path string, frames, shift int, bad map[int]bool) {
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	sector := make([]byte, rawSubStride)
	for i := 0; i < frames; i++ {
		for k := range sector {
			sector[k] = byte(i)
		}
		lba := i + shift
		raw := sector[rawSectorLen:]
		for k := range raw {
			raw[k] = 0x80 // P set, R-W clear
		}
		q := testQ(lba)
		if bad[lba] {
			q[8] ^= 0x01 // alter the time and keep the old CRC: broken CRC
		}
		qIntoRaw(raw, q)
		if _, err := f.Write(sector); err != nil {
			t.Fatal(err)
		}
	}
}

func TestRawSubchannelRealign(t *testing.T) {
	for _, shift := range []int{0, -1, 1} {
		dir := t.TempDir()
		bin := filepath.Join(dir, "disc.bin")
		sub := filepath.Join(dir, "disc.sub")
		const frames = 3000
		bad := map[int]bool{1200: true, 1203: true, 2100: true}
		writeTestBin(t, bin, frames, shift, bad)

		var progress int64
		rep, err := processRawSubchannel(bin, sub, make(chan struct{}), func(n int64) { progress += n })
		if err != nil {
			t.Fatalf("shift %d: %v", shift, err)
		}
		if rep.Offset != shift {
			t.Fatalf("shift %d: measured offset %d", shift, rep.Offset)
		}
		if want := int64(2 * frames * rawSubStride); progress != want {
			t.Fatalf("shift %d: progress %d, want %d", shift, progress, want)
		}

		// every LBA must now carry its own Q, bad ones must stay bad
		b, _ := os.ReadFile(bin)
		for lba := 0; lba < frames; lba++ {
			raw := b[lba*rawSubStride+rawSectorLen : (lba+1)*rawSubStride]
			q := qFromRaw(raw)
			if b[lba*rawSubStride] != byte(lba) {
				t.Fatalf("shift %d: sector data of LBA %d was modified", shift, lba)
			}
			if bad[lba] {
				if qCRCOK(q) {
					t.Fatalf("shift %d: LBA %d lost its bad CRC", shift, lba)
				}
				continue
			}
			if !qCRCOK(q) {
				t.Fatalf("shift %d: LBA %d has a bad CRC", shift, lba)
			}
			if f, _ := qAbsFrame(q); f != lba+150 {
				t.Fatalf("shift %d: LBA %d holds Q of frame %d", shift, lba, f)
			}
			if raw[0]&0x80 == 0 {
				t.Fatalf("shift %d: P channel lost at LBA %d", shift, lba)
			}
		}
		if len(rep.BadCRCLBAs) != len(bad) {
			t.Fatalf("shift %d: %d bad CRC sectors reported, want %d (%v)", shift, len(rep.BadCRCLBAs), len(bad), rep.BadCRCLBAs)
		}
		for _, lba := range rep.BadCRCLBAs {
			if !bad[lba] {
				t.Fatalf("shift %d: unexpected bad CRC at %d", shift, lba)
			}
		}

		// cooked .sub: Q is bytes 12..23 of each 96-byte entry
		s, _ := os.ReadFile(sub)
		if len(s) != frames*subLen {
			t.Fatalf("shift %d: .sub size %d", shift, len(s))
		}
		for _, lba := range []int{0, 1, 1500, frames - 1} {
			var q [12]byte
			copy(q[:], s[lba*subLen+12:lba*subLen+24])
			if q != testQ(lba) {
				t.Fatalf("shift %d: .sub Q of LBA %d = % x", shift, lba, q)
			}
			if s[lba*subLen] != 0xff {
				t.Fatalf("shift %d: .sub P of LBA %d = %02x", shift, lba, s[lba*subLen])
			}
		}
	}
}

func TestTocHasRawSubchannel(t *testing.T) {
	dir := t.TempDir()
	toc := filepath.Join(dir, "a.toc")
	os.WriteFile(toc, []byte("CD_ROM_XA\n\n// Track 1\nTRACK MODE2_RAW RW_RAW\nNO COPY\nDATAFILE \"a.bin\" 70:00:00 // length in bytes: 1\n"), 0644)
	if !tocHasRawSubchannel(toc) {
		t.Fatal("RW_RAW not detected")
	}
	os.WriteFile(toc, []byte("CD_ROM_XA\nTRACK MODE2_RAW\n"), 0644)
	if tocHasRawSubchannel(toc) {
		t.Fatal("false positive")
	}
}
