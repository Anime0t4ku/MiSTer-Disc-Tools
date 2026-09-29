package main

// LibCrypt .sbi into the Main's sbi.zip, so a burned copy boots.
//
// A burned CD-R cannot carry the modified subchannel of a LibCrypt disc, so
// the Main needs the key from an .sbi file. For a physical disc it only looks
// in <PSX games folder>/sbi.zip/<game ID>.sbi. Disc Tools adds the .sbi there
// when it rips a LibCrypt disc and when it burns a LibCrypt image, so the
// burned copy works without any manual step. An .sbi already in sbi.zip for
// the same game ID is never replaced.

import (
	"archive/zip"
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Same prefixes and order as the Main (support/psx/psx.cpp, region_info_table).
var psxIDPrefixes = []string{
	"SCES", "SLES", "SCUS", "SLUS", "SCPM", "SLPM", "SCPS", "SLPS", "SIPS",
	"PUPX", "PEPX", "PAPX", "PCPX", "SCZS", "SCED", "SLED",
}

var psxIDSafe = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)

// psxGameID reads the game ID the way the Main does: the first known prefix
// in the root folder sectors (LBA 22..46), up to ";1", with SLES_020.83
// normalized to SLES-02083. read returns the 2352-byte sector at an LBA.
func psxGameID(read func(lba int) ([]byte, error)) string {
	for lba := 22; lba < 22+25; lba++ {
		buf, err := read(lba)
		if err != nil {
			continue
		}
		start := -1
		for _, p := range psxIDPrefixes {
			if i := bytes.Index(buf, []byte(p)); i >= 0 {
				start = i
				break
			}
		}
		if start < 0 {
			continue
		}
		end := bytes.Index(buf[start:], []byte(";1"))
		if end < 0 {
			continue
		}
		id := append([]byte(nil), buf[start:start+end]...)
		if len(id) == 11 {
			if id[4] == '_' {
				id[4] = '-'
			}
			if id[8] == '.' {
				id = append(id[:8], id[9:]...)
			}
		}
		if len(id) > 10 {
			id = id[:10]
		}
		if !psxIDSafe.Match(id) {
			return ""
		}
		return string(id)
	}
	return ""
}

// sectorReader reads the 2352-byte data of sector lba from a BIN where
// sector 0 starts at offset and sectors are stride bytes apart (2352, or
// 2448 for a rip with the raw subchannel).
func sectorReader(path string, offset, stride int64) func(lba int) ([]byte, error) {
	return func(lba int) ([]byte, error) {
		f, err := os.Open(path)
		if err != nil {
			return nil, err
		}
		defer f.Close()
		buf := make([]byte, rawSectorLen)
		_, err = f.ReadAt(buf, offset+int64(lba)*stride)
		return buf, err
	}
}

// cueGameID reads the game ID from the first track of a CUE image.
func cueGameID(cue string) string {
	b, err := os.ReadFile(cue)
	if err != nil {
		return ""
	}
	fileRE := regexp.MustCompile(`(?i)^\s*FILE\s+(?:"([^"]+)"|(\S+))\s+(\S+)\s*$`)
	trackRE := regexp.MustCompile(`(?i)^\s*TRACK\s+(\d+)\s+(\S+)\s*$`)
	indexRE := regexp.MustCompile(`(?i)^\s*INDEX\s+01\s+(\d{1,3}:\d{1,2}:\d{1,2})\s*$`)
	var bin, mode string
	for _, raw := range strings.Split(string(b), "\n") {
		line := strings.TrimRight(raw, "\r")
		if m := fileRE.FindStringSubmatch(line); len(m) == 4 {
			if bin != "" {
				break // track 1 has no INDEX 01 in its file
			}
			bin = m[1]
			if bin == "" {
				bin = m[2]
			}
			continue
		}
		if m := trackRE.FindStringSubmatch(line); len(m) == 3 {
			if mode != "" {
				break
			}
			mode = strings.ToUpper(m[2])
			continue
		}
		if m := indexRE.FindStringSubmatch(line); len(m) == 2 && bin != "" && mode != "" {
			if mode != "MODE2/2352" && mode != "MODE1/2352" {
				return ""
			}
			start, err := cueMSFBlocks(m[1])
			if err != nil {
				return ""
			}
			if !filepath.IsAbs(bin) {
				bin = filepath.Join(filepath.Dir(cue), bin)
			}
			return psxGameID(sectorReader(bin, start*rawSectorLen, rawSectorLen))
		}
	}
	return ""
}

// psxHomeDir is the folder the Main uses as the PSX home (HomeDir), found in
// the same order as findPrefixDir() in the Main: USB drives first, then the
// network share, cifs and the SD card.
func psxHomeDir() string {
	var dirs []string
	for x := 0; x < 6; x++ {
		dirs = append(dirs, fmt.Sprintf("/media/usb%d/PSX", x), fmt.Sprintf("/media/usb%d/games/PSX", x))
	}
	dirs = append(dirs,
		"/media/network/PSX", "/media/network/games/PSX",
		"/media/fat/cifs/PSX", "/media/fat/cifs/games/PSX",
		"/media/fat/PSX", "/media/fat/games/PSX")
	for _, d := range dirs {
		if st, err := os.Stat(d); err == nil && st.IsDir() {
			return d
		}
	}
	return "/media/fat/games/PSX"
}

// addToSBIZip adds name to the zip (created when missing). An entry with the
// same name (any case, like the Main) is never replaced. It returns a short message for the user.
func addToSBIZip(zipPath, name string, data []byte) (string, error) {
	var old *zip.ReadCloser
	if _, err := os.Stat(zipPath); err == nil {
		old, err = zip.OpenReader(zipPath)
		if err != nil {
			return "", fmt.Errorf("cannot read %s: %v", zipPath, err)
		}
		defer old.Close()
		for _, f := range old.File {
			if !strings.EqualFold(f.Name, name) {
				continue
			}
			rc, err := f.Open()
			if err != nil {
				return "", err
			}
			cur, err := io.ReadAll(rc)
			rc.Close()
			if err != nil {
				return "", err
			}
			if bytes.Equal(cur, data) {
				return name + " already in sbi.zip", nil
			}
			return "sbi.zip already has a different " + name + ", kept", nil
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", err
	}

	if err := os.MkdirAll(filepath.Dir(zipPath), 0755); err != nil {
		return "", err
	}
	tmp := zipPath + ".new"
	out, err := os.Create(tmp)
	if err != nil {
		return "", err
	}
	fail := func(e error) (string, error) {
		out.Close()
		_ = os.Remove(tmp)
		return "", e
	}
	zw := zip.NewWriter(out)
	if old != nil {
		for _, f := range old.File {
			if err := zw.Copy(f); err != nil {
				return fail(err)
			}
		}
	}
	w, err := zw.CreateHeader(&zip.FileHeader{Name: name, Method: zip.Deflate, Modified: time.Now()})
	if err != nil {
		return fail(err)
	}
	if _, err := w.Write(data); err != nil {
		return fail(err)
	}
	if err := zw.Close(); err != nil {
		return fail(err)
	}
	if err := out.Sync(); err != nil {
		return fail(err)
	}
	if err := out.Close(); err != nil {
		_ = os.Remove(tmp)
		return "", err
	}
	if old != nil {
		old.Close()
	}
	if err := os.Rename(tmp, zipPath); err != nil {
		_ = os.Remove(tmp)
		return "", err
	}
	return name + " added to sbi.zip", nil
}

// addLibCryptToSBIZip adds the .sbi of a LibCrypt game to the Main's sbi.zip
// and returns one line for the user (empty when there is nothing to do).
func addLibCryptToSBIZip(gameID string, sbi []byte) string {
	if sbi == nil {
		return ""
	}
	if gameID == "" {
		return "LibCrypt: game ID not found, .sbi not added to sbi.zip"
	}
	zipPath := filepath.Join(psxHomeDir(), "sbi.zip")
	msg, err := addToSBIZip(zipPath, gameID+".sbi", sbi)
	if err != nil {
		msg = "LibCrypt: could not update sbi.zip: " + err.Error()
	}
	appendDiscToolsLog("sbi.zip (" + zipPath + "): " + msg)
	return msg
}

// ---------------------------------------------------------------- .sbi sources

// sbiKey returns the LibCrypt key (minute 03) listed in an .sbi file.
func sbiKey(sbi []byte) (uint16, bool) {
	if len(sbi) < 4 || !bytes.Equal(sbi[:4], []byte{'S', 'B', 'I', 0}) {
		return 0, false
	}
	var key uint16
	for p := 4; p+4 <= len(sbi); {
		m, ok1 := bcd(sbi[p])
		s, ok2 := bcd(sbi[p+1])
		f, ok3 := bcd(sbi[p+2])
		size := 10
		if sbi[p+3] == 2 || sbi[p+3] == 3 {
			size = 3
		}
		if ok1 && ok2 && ok3 {
			frame := (m*60+s)*75 + f
			for bit, pr := range libcryptPairs {
				if frame == pr[0] {
					key |= 1 << uint(15-bit)
				}
			}
		}
		p += 4 + size
	}
	return key, true
}

// cookedSubQ reads the Q of absolute frames from a CloneCD .sub (96 bytes per
// sector, P..W de-interleaved, Q at bytes 12..23).
func cookedSubQ(path string) (qAtFrame, func(), error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, nil, err
	}
	st, err := f.Stat()
	if err != nil || st.Size()%subLen != 0 {
		f.Close()
		return nil, nil, fmt.Errorf("%s is not a CloneCD .sub", filepath.Base(path))
	}
	frames := int(st.Size() / subLen)
	get := func(frame int) ([12]byte, bool) {
		var q [12]byte
		lba := frame - 150
		if lba < 0 || lba >= frames {
			return q, false
		}
		if _, err := f.ReadAt(q[:], int64(lba)*subLen+12); err != nil {
			return q, false
		}
		return q, true
	}
	return get, func() { f.Close() }, nil
}

// subWindow holds consecutive 96-byte subchannel entries; entry k belongs to
// absolute frame first+k once the position is known.
type subWindow struct {
	raw   bool
	first int
	subs  []byte
}

// locateSubWindow finds the format (raw interleaved or cooked) and position
// of a block of subchannel entries from its valid Q. expectFirst is the
// absolute frame the first entry should hold if the image starts at LBA 0.
func locateSubWindow(subs []byte, expectFirst int) (subWindow, bool) {
	n := len(subs) / subLen
	type key struct {
		raw bool
		d   int
	}
	votes := map[key]int{}
	total := 0
	for k := 0; k < n; k++ {
		e := subs[k*subLen : (k+1)*subLen]
		var cooked [12]byte
		copy(cooked[:], e[12:24])
		for _, c := range []struct {
			raw bool
			q   [12]byte
		}{{true, qFromRaw(e)}, {false, cooked}} {
			if !qCRCOK(c.q) {
				continue
			}
			if f, ok := qAbsFrame(c.q); ok {
				votes[key{c.raw, f - (expectFirst + k)}]++
				total++
			}
		}
	}
	best, bestN := key{}, 0
	for k, v := range votes {
		if v > bestN {
			best, bestN = k, v
		}
	}
	if bestN < 16 || bestN*2 <= total {
		return subWindow{}, false
	}
	return subWindow{raw: best.raw, first: expectFirst + best.d, subs: subs}, true
}

func (w subWindow) q(frame int) ([12]byte, bool) {
	k := frame - w.first
	if k < 0 || (k+1)*subLen > len(w.subs) {
		return [12]byte{}, false
	}
	e := w.subs[k*subLen : (k+1)*subLen]
	if w.raw {
		return qFromRaw(e), true
	}
	var q [12]byte
	copy(q[:], e[12:24])
	return q, true
}

// chdLibCrypt reads the subcode of the two LibCrypt areas from a CHD with
// `chdman extractraw` (a few MB, not the whole disc) and returns the result.
// ok is false when the CHD has no usable subcode.
func chdLibCrypt(chd, workDir string) (libcryptResult, bool, error) {
	const unit = rawSectorLen + subLen // CD CHD unit: sector + subcode
	const margin = 40
	areas := [][2]int{
		{libcryptPairs[0][0], libcryptPairs[15][0] + 5},
		{libcryptPairs[0][1], libcryptPairs[15][1] + 5},
	}
	extract := func(i, startLBA, count int) ([]byte, error) {
		out := filepath.Join(workDir, "libcrypt"+strconv.Itoa(i)+".raw")
		defer os.Remove(out)
		cmd := exec.Command(helper("chdman"), "extractraw", "-i", chd, "-o", out, "-f",
			"-isb", strconv.Itoa(startLBA*unit), "-ib", strconv.Itoa(count*unit))
		if b, err := cmd.CombinedOutput(); err != nil {
			return nil, fmt.Errorf("chdman extractraw: %v %s", err, lastLine(string(b)))
		}
		data, err := os.ReadFile(out)
		if err != nil {
			return nil, err
		}
		n := len(data) / unit
		subs := make([]byte, 0, n*subLen)
		for k := 0; k < n; k++ {
			subs = append(subs, data[k*unit+rawSectorLen:(k+1)*unit]...)
		}
		return subs, nil
	}
	var wins []subWindow
	for i, a := range areas {
		short := false
		count := a[1] - a[0] + 1 + 2*margin
		startLBA := a[0] - 150 - margin // CHD unit = LBA when the image starts at LBA 0
		for try := 0; try < 2; try++ {
			if startLBA < 0 {
				startLBA = 0
			}
			subs, err := extract(i, startLBA, count)
			if err != nil {
				if i == 0 {
					return libcryptResult{}, false, err
				}
				short = true // disc shorter than minute 09: the first copy is enough
				break
			}
			w, ok := locateSubWindow(subs, startLBA+150)
			if !ok {
				break
			}
			if w.first <= a[0] && w.first+len(subs)/subLen > a[1] {
				wins = append(wins, w)
				break
			}
			// the CHD stores something before LBA 0 (pregap): read the
			// area again where the Q says it is
			startLBA += a[0] - margin - w.first
		}
		if short {
			break
		}
		if len(wins) != i+1 {
			// every LibCrypt sector of this copy must be readable, or a
			// missing pair would give a wrong key
			return libcryptResult{}, false, nil
		}
	}
	if len(wins) == 0 {
		return libcryptResult{}, false, nil
	}
	return libcryptFromQ(func(f int) ([12]byte, bool) {
		for _, w := range wins {
			if q, ok := w.q(f); ok {
				return q, true
			}
		}
		return [12]byte{}, false
	}), true, nil
}

func lastLine(s string) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	return lines[len(lines)-1]
}

// imageLibCrypt finds the .sbi of an image to burn: an .sbi next to it
// (same name), else the subchannel (.sub next to a CUE, subcode of a CHD).
// It returns the .sbi contents (nil when the image has no LibCrypt) and a
// description of where the key came from.
func imageLibCrypt(image, workDir string) ([]byte, string) {
	base := strings.TrimSuffix(image, filepath.Ext(image))
	if b, err := os.ReadFile(base + ".sbi"); err == nil {
		if key, ok := sbiKey(b); ok && key != 0 {
			return b, fmt.Sprintf("key %04X from %s", key, filepath.Base(base+".sbi"))
		}
	}
	var lc libcryptResult
	var from string
	if strings.EqualFold(filepath.Ext(image), ".chd") {
		r, ok, err := chdLibCrypt(image, workDir)
		if err != nil {
			appendDiscToolsLog("LibCrypt check of " + filepath.Base(image) + ": " + err.Error())
		}
		if !ok {
			return nil, ""
		}
		lc, from = r, "the CHD subchannel"
	} else {
		get, done, err := cookedSubQ(base + ".sub")
		if err != nil {
			return nil, ""
		}
		lc, from = libcryptFromQ(get), filepath.Base(base+".sub")
		done()
	}
	if lc.SBI == nil {
		return nil, ""
	}
	if !lc.consistent() {
		appendDiscToolsLog(fmt.Sprintf("LibCrypt check of %s: the two copies of the key differ (%04X, %04X), .sbi not used", filepath.Base(image), lc.Key03, lc.Key09))
		return nil, fmt.Sprintf("the two copies of the key in %s differ (%04X, %04X)", from, lc.Key03, lc.Key09)
	}
	return lc.SBI, fmt.Sprintf("key %04X from %s", lc.Key03, from)
}

// libcryptForBurn adds the .sbi of a LibCrypt image to sbi.zip before it is
// burned. cue is the CUE that will be burned (for a CHD, the extracted one).
// It returns lines for the BURN COMPLETE message (none for other discs).
func libcryptForBurn(image, cue, workDir string) []string {
	sbi, from := imageLibCrypt(image, workDir)
	if sbi == nil {
		if from != "" {
			return []string{"PSX LibCrypt: " + from + ",", ".sbi not added to sbi.zip."}
		}
		return nil
	}
	id := cueGameID(cue)
	msg := addLibCryptToSBIZip(id, sbi)
	appendDiscToolsLog("burn " + filepath.Base(image) + ": LibCrypt " + from + ", game ID " + id)
	return []string{"PSX LibCrypt " + from + ":", msg}
}
