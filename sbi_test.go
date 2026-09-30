package main

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

// disc of n frames with a valid Q everywhere
func cleanSubs(n int) []byte {
	subs := make([]byte, n*subLen)
	for lba := 0; lba < n; lba++ {
		f := lba + 150
		q := [12]byte{0x41, 0x01, 0x01, toBCD(lba / 4500), toBCD(lba / 75 % 60), toBCD(lba % 75), 0, toBCD(f / 4500), toBCD(f / 75 % 60), toBCD(f % 75)}
		c := qCRC(q)
		q[10], q[11] = byte(c>>8), byte(c)
		qIntoRaw(subs[lba*subLen:(lba+1)*subLen], q)
	}
	return subs
}

// LibCrypt-like modification: position changed, CRC broken
func breakQ(subs []byte, frame int) {
	raw := subs[(frame-150)*subLen : (frame-149)*subLen]
	q := qFromRaw(raw)
	q[9] ^= 0x04
	q[10] ^= 0x80
	qIntoRaw(raw, q)
}

func TestLibCryptSBI(t *testing.T) {
	const n = 50000
	subs := cleanSubs(n)
	if r := libcryptFromSubchannel(subs, n); r.SBI != nil || r.Key03 != 0 {
		t.Fatalf("clean disc: got key %04X", r.Key03)
	}

	// FF8 PAL key 94CD in both copies, plus disc defects: one single broken
	// sector on a LibCrypt position and a few elsewhere
	const key = 0x94CD
	for bit, p := range libcryptPairs {
		if key&(1<<uint(15-bit)) == 0 {
			continue
		}
		for _, first := range p {
			breakQ(subs, first)
			breakQ(subs, first+5)
		}
	}
	breakQ(subs, 14231) // defect on an unused LibCrypt position (bit 14), pair not complete
	breakQ(subs, 20000)
	breakQ(subs, 20005)

	r := libcryptFromSubchannel(subs, n)
	if r.Key03 != key || r.Key09 != key {
		t.Fatalf("keys %04X %04X, want %04X", r.Key03, r.Key09, key)
	}
	if len(r.Frames) != 32 || len(r.SBI) != 4+32*14 {
		t.Fatalf("%d frames, %d bytes", len(r.Frames), len(r.SBI))
	}
	if !bytes.Equal(r.SBI[:4], []byte("SBI\x00")) {
		t.Fatal("header")
	}
	prev := 0
	for i := 0; i < 32; i++ {
		e := r.SBI[4+i*14 : 4+(i+1)*14]
		m, _ := bcd(e[0])
		s, _ := bcd(e[1])
		f, _ := bcd(e[2])
		frame := (m*60+s)*75 + f
		if frame <= prev || e[3] != 1 {
			t.Fatalf("entry %d: frame %d type %d", i, frame, e[3])
		}
		prev = frame
		q, _ := subQAt(subs, n, frame)
		if !bytes.Equal(e[4:], q[:10]) {
			t.Fatalf("entry %d: Q differs", i)
		}
		if frame == 14231 || frame == 20000 {
			t.Fatalf("defect %d written to the .sbi", frame)
		}
	}

	path := filepath.Join(t.TempDir(), "x.sbi")
	if _, err := writeLibCryptSBI(path, subs, n); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(path); !bytes.Equal(b, r.SBI) {
		t.Fatal("file contents")
	}
	if _, err := writeLibCryptSBI(path, cleanSubs(n), n); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); err == nil {
		t.Fatal("no LibCrypt: .sbi must not exist")
	}
}
