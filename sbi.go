package main

// LibCrypt .sbi from the verified subchannel of a rip.
//
// LibCrypt (PSX) stores a 16 bit key in the subchannel: each bit is a pair of
// sectors five apart at minute 03, with a backup copy at minute 09. A '1' bit
// is a pair whose Q was modified at the factory (broken CRC). Emulators and
// the MiSTer Main read this key from an .sbi file, which lists the modified
// sectors with their Q (without CRC).
//
// Only the 64 known LibCrypt sectors are looked at, and a pair is written only
// when both of its sectors are broken, so damaged sectors elsewhere on the
// disc (real defects, which the rip keeps in the .sub/CHD) never end up in
// the .sbi.

import (
	"fmt"
	"os"
	"sort"
)

// first sector (absolute frame, LBA + 150) of each LibCrypt pair, bit 15 first:
// minute 03 copy, minute 09 backup copy
var libcryptPairs = [16][2]int{
	{14105, 42045}, {14231, 42166}, {14485, 42432}, {14579, 42580},
	{14649, 42671}, {14899, 42813}, {15056, 43012}, {15130, 43177},
	{15242, 43289}, {15312, 43354}, {15378, 43408}, {15628, 43634},
	{15919, 43963}, {16031, 44054}, {16101, 44159}, {16167, 44312},
}

type libcryptResult struct {
	Key03, Key09 uint16 // key read from each copy
	Frames       []int  // modified sectors written to the .sbi (absolute frames), sorted
	SBI          []byte // .sbi file contents, nil when the disc has no LibCrypt
}

func subQAt(subs []byte, frames, frame int) ([12]byte, bool) {
	lba := frame - 150
	if lba < 0 || lba >= frames {
		return [12]byte{}, false
	}
	return qFromRaw(subs[lba*subLen : (lba+1)*subLen]), true
}

// brokenPair is true when both sectors of the pair starting at frame are on
// the disc and have a broken Q.
func brokenPair(subs []byte, frames, frame int) bool {
	a, ok1 := subQAt(subs, frames, frame)
	b, ok2 := subQAt(subs, frames, frame+5)
	return ok1 && ok2 && !qCRCOK(a) && !qCRCOK(b)
}

func libcryptFromSubchannel(subs []byte, frames int) libcryptResult {
	var r libcryptResult
	for bit, p := range libcryptPairs {
		mask := uint16(1) << uint(15-bit)
		for copyIdx, first := range p {
			if !brokenPair(subs, frames, first) {
				continue
			}
			if copyIdx == 0 {
				r.Key03 |= mask
			} else {
				r.Key09 |= mask
			}
			r.Frames = append(r.Frames, first, first+5)
		}
	}
	if len(r.Frames) == 0 {
		return r
	}
	sort.Ints(r.Frames)
	sbi := []byte{'S', 'B', 'I', 0}
	for _, f := range r.Frames {
		q, _ := subQAt(subs, frames, f)
		sbi = append(sbi, toBCD(f/75/60), toBCD(f/75%60), toBCD(f%75), 0x01)
		sbi = append(sbi, q[:10]...)
	}
	r.SBI = sbi
	return r
}

func (r libcryptResult) text() string {
	if r.SBI == nil {
		return "LibCrypt: not found (no modified LibCrypt sector pair)\n"
	}
	s := fmt.Sprintf("LibCrypt: key %04X (minute 03), %04X (minute 09 backup), %d sector(s) in the .sbi\n", r.Key03, r.Key09, len(r.Frames))
	if r.Key03 != r.Key09 {
		s += "  warning: the two copies of the key differ\n"
	}
	return s
}

// writeLibCryptSBI writes path when the rip has LibCrypt; it removes a stale
// file otherwise. It returns the result for the report.
func writeLibCryptSBI(path string, subs []byte, frames int) (libcryptResult, error) {
	r := libcryptFromSubchannel(subs, frames)
	if r.SBI == nil {
		_ = os.Remove(path)
		return r, nil
	}
	return r, os.WriteFile(path, r.SBI, 0644)
}
