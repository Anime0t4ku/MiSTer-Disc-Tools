package main

// Raw subchannel post-processing for rips made with
// `cdrdao read-cd --read-raw --read-subchan rw_raw`.
//
// cdrdao stores the 96 bytes of raw, interleaved P-W subchannel returned by
// the drive right after every 2352-byte sector. The Q channel carries the
// position of each sector and, on some discs, deliberate errors: PSX
// LibCrypt protection is a set of sectors whose Q has a broken CRC. Keeping
// the raw subchannel preserves those sectors in the BIN/TOC and in the CHD.
//
// Many drives return the subchannel of a neighbouring sector (typically one
// sector late). processRawSubchannel measures that constant offset from the
// valid Q positions, realigns the subchannel in place, writes a CloneCD style
// .sub file (P..W de-interleaved, 12 bytes each) for the CUE/BIN output and
// reports the sectors whose Q CRC is bad.

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
)

const (
	rawSectorLen   = 2352
	subLen         = 96
	rawSubStride   = rawSectorLen + subLen
	subChunkFrames = 512
	maxSubOffset   = 2
)

type subchannelReport struct {
	Frames      int
	ValidQ      int
	Offset      int // before correction, the sector at index i held the Q of LBA i+Offset
	Corrected   bool
	BadCRCLBAs  []int
	OffsetVotes map[int]int
}

// qFromRaw extracts the 12 Q bytes from 96 bytes of raw interleaved P-W.
func qFromRaw(raw []byte) [12]byte {
	var q [12]byte
	for i := 0; i < 96; i++ {
		if raw[i]&0x40 != 0 {
			q[i>>3] |= 0x80 >> uint(i&7)
		}
	}
	return q
}

// qIntoRaw replaces the Q bits of raw interleaved P-W with q.
func qIntoRaw(raw []byte, q [12]byte) {
	for i := 0; i < 96; i++ {
		raw[i] &^= 0x40
		if q[i>>3]&(0x80>>uint(i&7)) != 0 {
			raw[i] |= 0x40
		}
	}
}

// cookedFromRaw de-interleaves raw P-W into 8 channels of 12 bytes (P, Q, R..W).
func cookedFromRaw(raw []byte, out []byte) {
	for i := range out[:96] {
		out[i] = 0
	}
	for ch := 0; ch < 8; ch++ {
		bit := byte(0x80 >> uint(ch))
		for i := 0; i < 96; i++ {
			if raw[i]&bit != 0 {
				out[ch*12+(i>>3)] |= 0x80 >> uint(i&7)
			}
		}
	}
}

func qCRC(q [12]byte) uint16 {
	var crc uint16
	for i := 0; i < 10; i++ {
		crc ^= uint16(q[i]) << 8
		for b := 0; b < 8; b++ {
			if crc&0x8000 != 0 {
				crc = crc<<1 ^ 0x1021
			} else {
				crc <<= 1
			}
		}
	}
	return ^crc
}

func qCRCOK(q [12]byte) bool {
	return qCRC(q) == uint16(q[10])<<8|uint16(q[11])
}

func bcd(b byte) (int, bool) {
	hi, lo := int(b>>4), int(b&0x0f)
	if hi > 9 || lo > 9 {
		return 0, false
	}
	return hi*10 + lo, true
}

func toBCD(v int) byte { return byte((v/10)<<4 | v%10) }

// qAbsFrame returns the absolute frame (LBA + 150) of a valid mode-1 Q.
func qAbsFrame(q [12]byte) (int, bool) {
	if q[0]&0x0f != 1 {
		return 0, false
	}
	m, ok1 := bcd(q[7])
	s, ok2 := bcd(q[8])
	f, ok3 := bcd(q[9])
	if !ok1 || !ok2 || !ok3 || s > 59 || f > 74 {
		return 0, false
	}
	return (m*60+s)*75 + f, true
}

func setMSF(q *[12]byte, at int, frames int) {
	if frames < 0 {
		frames = 0
	}
	q[at] = toBCD(frames / 75 / 60)
	q[at+1] = toBCD(frames / 75 % 60)
	q[at+2] = toBCD(frames % 75)
}

// stepQ builds the Q of a sector n frames after (or before, n < 0) the
// sector described by q. Used only for the few sectors at the very ends of
// the image that have no source after the offset correction.
func stepQ(q [12]byte, n int) [12]byte {
	abs, ok := qAbsFrame(q)
	if !ok {
		return q
	}
	rm, _ := bcd(q[3])
	rs, _ := bcd(q[4])
	rf, _ := bcd(q[5])
	rel := (rm*60+rs)*75 + rf
	if q[2] == 0 { // pregap: relative time counts down
		rel -= n
	} else {
		rel += n
	}
	setMSF(&q, 3, rel)
	setMSF(&q, 7, abs+n)
	crc := qCRC(q)
	q[10] = byte(crc >> 8)
	q[11] = byte(crc)
	return q
}

// tocHasRawSubchannel reports whether a cdrdao TOC describes tracks with
// raw R-W subchannel data interleaved in the data file.
func tocHasRawSubchannel(toc string) bool {
	b, err := os.ReadFile(toc)
	if err != nil {
		return false
	}
	for _, line := range strings.Split(string(b), "\n") {
		f := strings.Fields(line)
		if len(f) >= 3 && f[0] == "TRACK" && f[2] == "RW_RAW" {
			return true
		}
	}
	return false
}

func readSubchannel(bin string, frames int, cancel <-chan struct{}, onBytes func(int64)) ([]byte, error) {
	in, err := os.Open(bin)
	if err != nil {
		return nil, err
	}
	defer in.Close()
	subs := make([]byte, frames*subLen)
	r := bufio.NewReaderSize(in, subChunkFrames*rawSubStride)
	sector := make([]byte, rawSubStride)
	for i := 0; i < frames; i++ {
		if i%subChunkFrames == 0 {
			select {
			case <-cancel:
				return nil, errors.New("cancelled")
			default:
			}
		}
		if _, err := io.ReadFull(r, sector); err != nil {
			return nil, err
		}
		copy(subs[i*subLen:], sector[rawSectorLen:])
		onBytes(rawSubStride)
	}
	return subs, nil
}

// measureSubOffset finds the constant distance between the sector index and
// the position written in its Q (subchannel found for LBA x+offset belongs to x).
func measureSubOffset(subs []byte, frames int, rep *subchannelReport) {
	rep.OffsetVotes = map[int]int{}
	for i := 0; i < frames; i++ {
		q := qFromRaw(subs[i*subLen : (i+1)*subLen])
		if !qCRCOK(q) {
			continue
		}
		f, ok := qAbsFrame(q)
		if !ok {
			continue
		}
		rep.ValidQ++
		d := f - (i + 150)
		if d >= -maxSubOffset && d <= maxSubOffset {
			rep.OffsetVotes[d]++
		}
	}
	best, bestVotes := 0, -1
	for d, v := range rep.OffsetVotes {
		if v > bestVotes {
			best, bestVotes = d, v
		}
	}
	// Only trust a clear majority; otherwise leave the data untouched.
	if rep.ValidQ > 0 && bestVotes*10 >= rep.ValidQ*9 {
		rep.Offset = best
	}
}

// realignSubchannel shifts the subchannel so that entry x holds the Q of LBA x
// (offset as measured by measureSubOffset: index i held the Q of LBA i+offset).
func realignSubchannel(subs []byte, frames, offset int) []byte {
	out := make([]byte, len(subs))
	var missing []int
	for j := 0; j < frames; j++ {
		src := j - offset
		if src < 0 || src >= frames {
			missing = append(missing, j)
			continue
		}
		copy(out[j*subLen:(j+1)*subLen], subs[src*subLen:(src+1)*subLen])
	}
	for _, j := range missing {
		n := j + 1
		if j >= frames/2 {
			n = j - 1
		}
		for n >= 0 && n < frames && contains(missing, n) {
			if j >= frames/2 {
				n--
			} else {
				n++
			}
		}
		if n < 0 || n >= frames {
			continue
		}
		tmpl := out[n*subLen : (n+1)*subLen]
		q := stepQ(qFromRaw(tmpl), j-n)
		dst := out[j*subLen : (j+1)*subLen]
		copy(dst, tmpl)
		qIntoRaw(dst, q)
	}
	return out
}

func contains(s []int, v int) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}

func writeSubchannelBack(bin string, subs []byte, frames int, cancel <-chan struct{}, onBytes func(int64)) error {
	f, err := os.OpenFile(bin, os.O_RDWR, 0)
	if err != nil {
		return err
	}
	defer f.Close()
	for i := 0; i < frames; i++ {
		if i%subChunkFrames == 0 {
			select {
			case <-cancel:
				return errors.New("cancelled")
			default:
			}
		}
		if _, err := f.WriteAt(subs[i*subLen:(i+1)*subLen], int64(i)*rawSubStride+rawSectorLen); err != nil {
			return err
		}
		onBytes(rawSubStride)
	}
	return f.Sync()
}

func writeCookedSub(path string, subs []byte, frames int) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	w := bufio.NewWriterSize(f, 1<<20)
	cooked := make([]byte, subLen)
	for i := 0; i < frames; i++ {
		cookedFromRaw(subs[i*subLen:(i+1)*subLen], cooked)
		if _, err := w.Write(cooked); err != nil {
			f.Close()
			return err
		}
	}
	if err := w.Flush(); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

func lbaMSF(lba int) string {
	f := lba + 150
	return fmt.Sprintf("%02d:%02d:%02d", f/75/60, f/75%60, f%75)
}

// processRawSubchannel realigns the raw subchannel of a cdrdao BIN in place,
// writes subPath (CloneCD .sub) when not empty and returns a report.
// onBytes receives progress over 2x the BIN size (read pass + write pass).
func processRawSubchannel(bin, subPath string, cancel <-chan struct{}, onBytes func(int64)) (subchannelReport, error) {
	var rep subchannelReport
	info, err := os.Stat(bin)
	if err != nil {
		return rep, err
	}
	if info.Size()%rawSubStride != 0 {
		return rep, fmt.Errorf("BIN size is not a multiple of %d bytes (raw sector + subchannel)", rawSubStride)
	}
	frames := int(info.Size() / rawSubStride)
	rep.Frames = frames

	subs, err := readSubchannel(bin, frames, cancel, onBytes)
	if err != nil {
		return rep, err
	}
	measureSubOffset(subs, frames, &rep)
	if rep.Offset != 0 {
		subs = realignSubchannel(subs, frames, rep.Offset)
		if err := writeSubchannelBack(bin, subs, frames, cancel, onBytes); err != nil {
			return rep, err
		}
		rep.Corrected = true
	} else {
		onBytes(info.Size())
	}

	for i := 0; i < frames; i++ {
		if !qCRCOK(qFromRaw(subs[i*subLen : (i+1)*subLen])) {
			rep.BadCRCLBAs = append(rep.BadCRCLBAs, i)
		}
	}

	if subPath != "" {
		if err := writeCookedSub(subPath, subs, frames); err != nil {
			return rep, err
		}
	}
	return rep, nil
}

func (r subchannelReport) text() string {
	var b strings.Builder
	fmt.Fprintf(&b, "Sectors: %d\n", r.Frames)
	fmt.Fprintf(&b, "Sectors with a valid Q position: %d\n", r.ValidQ)
	if r.Corrected {
		fmt.Fprintf(&b, "Drive subchannel offset: %+d sector(s), corrected\n", r.Offset)
	} else {
		fmt.Fprintf(&b, "Drive subchannel offset: none detected\n")
	}
	fmt.Fprintf(&b, "Sectors with a bad Q CRC: %d\n", len(r.BadCRCLBAs))
	for _, lba := range r.BadCRCLBAs {
		fmt.Fprintf(&b, "  LBA %6d  MSF %s\n", lba, lbaMSF(lba))
	}
	return b.String()
}
