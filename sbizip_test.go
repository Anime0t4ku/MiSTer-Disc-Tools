package main

import (
	"archive/zip"
	"bytes"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// BIN of n sectors with a directory record naming the boot file at LBA 23
func writeIDBin(t *testing.T, path, name string, stride int) {
	t.Helper()
	const n = 60
	b := make([]byte, n*stride)
	copy(b[23*stride+24+100:], []byte("\x0dSYSTEM.CNF;1\x00"))
	copy(b[23*stride+24+200:], []byte(name+";1"))
	if err := os.WriteFile(path, b, 0644); err != nil {
		t.Fatal(err)
	}
}

func TestPSXGameID(t *testing.T) {
	dir := t.TempDir()
	cases := []struct{ in, want string }{
		{"SLES_020.83", "SLES-02083"},
		{"SCUS_944.91", "SCUS-94491"},
		{"SLPS_012.34", "SLPS-01234"},
	}
	for _, c := range cases {
		bin := filepath.Join(dir, "g.bin")
		writeIDBin(t, bin, c.in, rawSectorLen)
		cue := filepath.Join(dir, "g.cue")
		os.WriteFile(cue, []byte("FILE \"g.bin\" BINARY\r\n  TRACK 01 MODE2/2352\r\n    INDEX 01 00:00:00\r\n"), 0644)
		if got := cueGameID(cue); got != c.want {
			t.Errorf("cue %s: got %q want %q", c.in, got, c.want)
		}
		raw := filepath.Join(dir, "g.raw")
		writeIDBin(t, raw, c.in, rawSubStride)
		if got := psxGameID(sectorReader(raw, 0, rawSubStride)); got != c.want {
			t.Errorf("rip bin %s: got %q want %q", c.in, got, c.want)
		}
	}
	// audio CD / no game ID
	bin := filepath.Join(dir, "a.bin")
	os.WriteFile(bin, make([]byte, 60*rawSectorLen), 0644)
	cue := filepath.Join(dir, "a.cue")
	os.WriteFile(cue, []byte("FILE \"a.bin\" BINARY\n  TRACK 01 AUDIO\n    INDEX 01 00:00:00\n"), 0644)
	if got := cueGameID(cue); got != "" {
		t.Errorf("audio: got %q", got)
	}
}

func zipEntries(t *testing.T, path string) map[string][]byte {
	t.Helper()
	r, err := zip.OpenReader(path)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	m := map[string][]byte{}
	for _, f := range r.File {
		rc, _ := f.Open()
		b, _ := io.ReadAll(rc)
		rc.Close()
		m[f.Name] = b
	}
	return m
}

func TestAddToSBIZip(t *testing.T) {
	dir := t.TempDir()
	zp := filepath.Join(dir, "games", "PSX", "sbi.zip")
	a := []byte("SBI\x00aaaa")
	b := []byte("SBI\x00bbbb")

	if msg, err := addToSBIZip(zp, "SLES-02083.sbi", a); err != nil || !strings.Contains(msg, "added") {
		t.Fatalf("new zip: %q %v", msg, err)
	}
	if msg, err := addToSBIZip(zp, "SCES-01564.sbi", b); err != nil || !strings.Contains(msg, "added") {
		t.Fatalf("second entry: %q %v", msg, err)
	}
	if msg, _ := addToSBIZip(zp, "SLES-02083.sbi", a); !strings.Contains(msg, "already in") {
		t.Fatalf("same entry: %q", msg)
	}
	if msg, _ := addToSBIZip(zp, "SLES-02083.sbi", b); !strings.Contains(msg, "kept") {
		t.Fatalf("different entry: %q", msg)
	}
	m := zipEntries(t, zp)
	if len(m) != 2 || !bytes.Equal(m["SLES-02083.sbi"], a) || !bytes.Equal(m["SCES-01564.sbi"], b) {
		t.Fatalf("zip contents: %v", m)
	}
	if _, err := os.Stat(zp + ".new"); err == nil {
		t.Fatal("temporary file left behind")
	}
}

func TestSBIKey(t *testing.T) {
	subs := cleanSubs(50000)
	for bit, p := range libcryptPairs {
		if 0x94CD&(1<<uint(15-bit)) != 0 {
			for _, f := range p {
				breakQ(subs, f)
				breakQ(subs, f+5)
			}
		}
	}
	r := libcryptFromSubchannel(subs, 50000)
	if k, ok := sbiKey(r.SBI); !ok || k != 0x94CD {
		t.Fatalf("sbiKey %04X %v", k, ok)
	}
	if _, ok := sbiKey([]byte("nope")); ok {
		t.Fatal("bad header accepted")
	}
}

// a block of subchannel read from a CHD that starts 3 frames later than
// expected (image with a stored pregap) and in cooked format
func TestLocateSubWindow(t *testing.T) {
	subs := cleanSubs(20000)
	breakQ(subs, 14105)
	breakQ(subs, 14110)
	start := 13900
	var cooked []byte
	for lba := start + 3; lba < start+3+400; lba++ {
		e := make([]byte, subLen)
		cookedFromRaw(subs[lba*subLen:(lba+1)*subLen], e)
		cooked = append(cooked, e...)
	}
	w, ok := locateSubWindow(cooked, start+150)
	if !ok || w.raw || w.first != start+3+150 {
		t.Fatalf("locate: ok=%v raw=%v first=%d", ok, w.raw, w.first)
	}
	q, ok := w.q(14105)
	if !ok || qCRCOK(q) {
		t.Fatal("frame 14105 should be the broken one")
	}
	if q, ok := w.q(14104); !ok || !qCRCOK(q) {
		t.Fatal("frame 14104 should be valid")
	}
	// no subchannel at all (CHD made without subcode)
	if _, ok := locateSubWindow(make([]byte, 400*subLen), start+150); ok {
		t.Fatal("empty subchannel located")
	}
}

// Real data, when present: DT_REAL_SUB (cooked .sub of FF8 PAL disc 1),
// DT_REAL_SBI (its .sbi), DT_CHDMAN (chdman), DT_CHD (CHD made with subcode)
func TestRealLibCrypt(t *testing.T) {
	if sub, ref := os.Getenv("DT_REAL_SUB"), os.Getenv("DT_REAL_SBI"); sub != "" && ref != "" {
		get, done, err := cookedSubQ(sub)
		if err != nil {
			t.Fatal(err)
		}
		r := libcryptFromQ(get)
		done()
		want, _ := os.ReadFile(ref)
		t.Logf("%s: key %04X/%04X, %d sectors, consistent %v", filepath.Base(sub), r.Key03, r.Key09, len(r.Frames), r.consistent())
		if os.Getenv("DT_REAL_SUB_BAD") != "" {
			if r.consistent() {
				t.Fatal("the key copies of this .sub should differ")
			}
		} else if !bytes.Equal(r.SBI, want) {
			t.Fatalf(".sbi from the .sub differs from %s", ref)
		}
	}
	if cue, ref := os.Getenv("DT_CUE"), os.Getenv("DT_REAL_SBI"); cue != "" && ref != "" {
		info := imageLibCrypt(cue, t.TempDir())
		want, _ := os.ReadFile(ref)
		t.Logf("%s: game ID %q, %s, %d full Q", filepath.Base(cue), cueGameID(cue), info.From, len(info.Q))
		if !bytes.Equal(info.SBI, want) || cueGameID(cue) != "SLES-02083" || len(info.Q) != 32 {
			t.Fatalf("CUE + .sub: wrong .sbi, Q or game ID")
		}
		checkRawTrack(t, cue, info.Q)
	}
	if chdman, chd := os.Getenv("DT_CHDMAN"), os.Getenv("DT_CHD"); chdman != "" && chd != "" {
		if _, err := exec.LookPath(chdman); err != nil {
			t.Skip("no chdman")
		}
		helperDir = filepath.Dir(chdman)
		defer func() { helperDir = binDir }()
		for _, c := range strings.Split(chd, ",") {
			r, ok, err := chdLibCrypt(c, t.TempDir())
			t.Logf("%s: ok=%v key %04X/%04X, %d sectors, err %v", filepath.Base(c), ok, r.Key03, r.Key09, len(r.Frames), err)
			if strings.Contains(c, "nosub") {
				if ok || err != nil {
					t.Fatalf("%s: CHD without subcode must give no result", c)
				}
				continue
			}
			if want := os.Getenv("DT_REAL_SBI"); want != "" {
				w, _ := os.ReadFile(want)
				if !ok || !r.consistent() || r.Key03 != 0x94CD {
					t.Fatalf("%s: key %04X", c, r.Key03)
				}
				if r.Has09 && !bytes.Equal(r.SBI, w) {
					t.Fatalf("%s: .sbi differs", c)
				}
			}
		}
	}
}

// checkRawTrack builds the raw LibCrypt track of a single-BIN CUE and checks
// it: same sector data, Q only on the protected sectors, equal to the .sub.
func checkRawTrack(t *testing.T, cue string, q map[int][12]byte) {
	t.Helper()
	bin := cueSingleBinaryFile(cue)
	st, _ := os.Stat(bin)
	out := filepath.Join(t.TempDir(), "track01.raw")
	if err := copyRangeWithSubQ(bin, out, 0, st.Size(), 0, q, nil, nil); err != nil {
		t.Fatal(err)
	}
	src, _ := os.ReadFile(bin)
	raw, _ := os.ReadFile(out)
	n := len(src) / rawSectorLen
	if len(raw) != n*rawSubStride {
		t.Fatalf("raw size %d, want %d", len(raw), n*rawSubStride)
	}
	get, done, err := cookedSubQ(strings.TrimSuffix(cue, filepath.Ext(cue)) + ".sub")
	if err != nil {
		t.Fatal(err)
	}
	defer done()
	withQ := 0
	for lba := 0; lba < n; lba++ {
		if !bytes.Equal(raw[lba*rawSubStride:lba*rawSubStride+rawSectorLen], src[lba*rawSectorLen:(lba+1)*rawSectorLen]) {
			t.Fatalf("sector %d data differs", lba)
		}
		sub := raw[lba*rawSubStride+rawSectorLen : (lba+1)*rawSubStride]
		if !bytes.Equal(sub, make([]byte, subLen)) {
			withQ++
			want, _ := get(lba + 150)
			if _, ok := q[lba+150]; !ok || qFromRaw(sub) != want || qCRCOK(want) {
				t.Fatalf("sector %d: unexpected subchannel", lba)
			}
			for _, b := range sub {
				if b&^0x40 != 0 {
					t.Fatalf("sector %d: bits outside the Q channel", lba)
				}
			}
		}
	}
	if withQ != len(q) {
		t.Fatalf("%d sectors with Q, want %d", withQ, len(q))
	}
	t.Logf("raw track: %d sectors, Q on %d (the LibCrypt ones), data identical", n, withQ)
}
