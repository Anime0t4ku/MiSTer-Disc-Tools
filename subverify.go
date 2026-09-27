package main

// Verification pass for the raw subchannel of a rip.
//
// A single continuous raw read is not an exact copy of the subchannel:
// - raw P-W is not error corrected by the drive, so a few hundred Q frames
//   per CD come back with a broken CRC although they are fine on the disc;
// - some drives restart a read command with a different subchannel phase,
//   so a whole block of sectors gets the Q of its neighbour: one Q is lost
//   at the start of the block and one is repeated at its end.
//
// Every suspicious sector (bad CRC, or a valid Q that names another sector)
// is read again from the disc, several times, each time starting a few
// sectors earlier so the drive is reading continuously when it reaches it.
// A sector gets a valid Q if any read returns the valid Q of that very
// sector; it keeps a broken Q (copy protection such as PSX LibCrypt, or a
// real defect of the disc) only when the reads agree on it.

import (
	"errors"
	"fmt"
)

const (
	verifyPreRoll   = 6  // sectors read before the first suspect of a window
	verifyPostRoll  = 4  // sectors read after the last suspect of a window
	verifyMaxWindow = 26 // sectors per read command (64 KiB limit of many USB bridges)
	verifyReads     = 3  // reads of every window
	verifyMaxReads  = 12 // reads of a window whose broken Q values do not agree yet
	// sectors that must go through the drive before a window is read again,
	// so the drive cannot answer from its read cache (a few MB on most drives)
	verifyCacheSectors = 4096
)

// subReader reads the raw P-W subchannel (96 bytes per sector) of count
// consecutive sectors starting at lba, in one command.
type subReader func(lba, count int) ([]byte, error)

type verifyResult struct {
	Suspects   int
	Repaired   int   // got the valid Q of the sector from a re-read
	Confirmed  []int // broken Q confirmed by all re-reads (protection or defect)
	Unresolved []int // re-reads disagreed; the most frequent broken Q was kept
}

// subSuspects lists the sectors whose Q is broken or names another sector.
func subSuspects(subs []byte, frames int) []int {
	var s []int
	for i := 0; i < frames; i++ {
		q := qFromRaw(subs[i*subLen : (i+1)*subLen])
		if !qCRCOK(q) {
			s = append(s, i)
			continue
		}
		if f, ok := qAbsFrame(q); ok && f != i+150 {
			s = append(s, i)
		}
	}
	return s
}

type verifyWindow struct {
	start, count int
	suspects     []int
}

func groupSuspects(suspects []int, frames int) []verifyWindow {
	var ws []verifyWindow
	maxSpan := verifyMaxWindow - verifyPreRoll - verifyPostRoll
	for i := 0; i < len(suspects); {
		first := suspects[i]
		j := i + 1
		for j < len(suspects) && suspects[j]-first < maxSpan {
			j++
		}
		last := suspects[j-1]
		start := first - verifyPreRoll
		if start < 0 {
			start = 0
		}
		end := last + verifyPostRoll
		if end >= frames {
			end = frames - 1
		}
		ws = append(ws, verifyWindow{start: start, count: end - start + 1, suspects: suspects[i:j]})
		i = j
	}
	return ws
}

// windowPhase finds d such that entry k of the window holds the Q of sector
// start+k+d, from the valid Q positions inside the window.
func windowPhase(raw []byte, start, count int) (int, bool) {
	votes := map[int]int{}
	for k := 0; k < count; k++ {
		q := qFromRaw(raw[k*subLen : (k+1)*subLen])
		if !qCRCOK(q) {
			continue
		}
		if f, ok := qAbsFrame(q); ok {
			if d := f - 150 - (start + k); d >= -maxSubOffset && d <= maxSubOffset {
				votes[d]++
			}
		}
	}
	best, bestVotes, total := 0, 0, 0
	for d, v := range votes {
		total += v
		if v > bestVotes {
			best, bestVotes = d, v
		}
	}
	return best, bestVotes >= 3 && bestVotes*2 > total
}

type subCandidate struct {
	raw [subLen]byte
	n   int
}

// verifySubchannel re-reads the suspicious sectors and repairs subs in place.
// onSector is called once per verified suspect.
func verifySubchannel(subs []byte, frames int, read subReader, cancel <-chan struct{}, onSector func()) (verifyResult, error) {
	var res verifyResult
	suspects := subSuspects(subs, frames)
	res.Suspects = len(suspects)
	windows := groupSuspects(suspects, frames)

	type windowState struct {
		verifyWindow
		cands    map[int][]subCandidate
		good     map[int][]byte
		reads    int
		readAt   int64 // value of sectorsRead when this window was last read
		finished bool
	}
	states := make([]*windowState, len(windows))
	for i, w := range windows {
		states[i] = &windowState{verifyWindow: w, cands: map[int][]subCandidate{}, good: map[int][]byte{}, readAt: -verifyCacheSectors}
	}

	// sectorsRead counts every sector requested from the drive. A window is
	// read again only after enough other sectors went through the drive to
	// push it out of the drive's read cache: a cached copy would repeat the
	// same read error and look like a real broken Q.
	var sectorsRead int64
	flushFar := func(ws *windowState) {
		far := (ws.start + frames/2) % frames
		for sectorsRead-ws.readAt < verifyCacheSectors {
			if far+verifyMaxWindow > frames {
				far = 0
			}
			_, _ = read(far, verifyMaxWindow)
			sectorsRead += verifyMaxWindow
			far += verifyMaxWindow
		}
	}

	readOnce := func(ws *windowState) {
		if sectorsRead-ws.readAt < verifyCacheSectors {
			flushFar(ws)
		}
		raw, err := read(ws.start, ws.count)
		sectorsRead += int64(ws.count)
		ws.readAt = sectorsRead
		ws.reads++
		if err != nil || len(raw) < ws.count*subLen {
			return
		}
		d, ok := windowPhase(raw, ws.start, ws.count)
		if !ok {
			return
		}
		for _, lba := range ws.suspects {
			if ws.good[lba] != nil {
				continue
			}
			k := lba - d - ws.start
			// skip the first entries of the command: some drives return
			// a stale Q right after the seek
			if k < 2 || k >= ws.count {
				continue
			}
			e := raw[k*subLen : (k+1)*subLen]
			q := qFromRaw(e)
			if qCRCOK(q) {
				if f, ok := qAbsFrame(q); ok && f == lba+150 {
					ws.good[lba] = append([]byte(nil), e...)
				}
				continue // a valid Q of another sector says nothing about this one
			}
			found := false
			for i := range ws.cands[lba] {
				if qFromRaw(ws.cands[lba][i].raw[:]) == q {
					ws.cands[lba][i].n++
					found = true
					break
				}
			}
			if !found {
				var c subCandidate
				copy(c.raw[:], e)
				c.n = 1
				ws.cands[lba] = append(ws.cands[lba], c)
			}
		}
	}

	finish := func(ws *windowState) {
		ws.finished = true
		for _, lba := range ws.suspects {
			dst := subs[lba*subLen : (lba+1)*subLen]
			if g := ws.good[lba]; g != nil {
				copy(dst, g)
				res.Repaired++
			} else if c := ws.cands[lba]; len(c) > 0 {
				best := c[0]
				for _, x := range c {
					if x.n > best.n {
						best = x
					}
				}
				copy(dst, best.raw[:])
				if clearMajority(c) {
					res.Confirmed = append(res.Confirmed, lba)
				} else {
					res.Unresolved = append(res.Unresolved, lba)
				}
			} else {
				// no usable read at all: keep what the rip had
				res.Unresolved = append(res.Unresolved, lba)
			}
			onSector()
		}
	}

	// Read in passes over all windows spread across the disc, so the same
	// window comes back only after hundreds of other reads.
	for pass := 0; pass < verifyMaxReads; pass++ {
		pending := 0
		for _, ws := range states {
			if ws.finished {
				continue
			}
			select {
			case <-cancel:
				return res, errors.New("cancelled")
			default:
			}
			readOnce(ws)
			if ws.reads >= verifyReads && windowSettled(ws.suspects, ws.good, ws.cands) {
				finish(ws)
			} else {
				pending++
			}
		}
		if pending == 0 {
			break
		}
	}
	for _, ws := range states {
		if !ws.finished {
			finish(ws)
		}
	}
	sortInts(res.Confirmed)
	sortInts(res.Unresolved)
	return res, nil
}

func sortInts(v []int) {
	for i := 1; i < len(v); i++ {
		for j := i; j > 0 && v[j] < v[j-1]; j-- {
			v[j], v[j-1] = v[j-1], v[j]
		}
	}
}

// clearMajority is true when one broken Q was returned at least 3 times and
// more than twice as often as any other value (random read errors of the
// raw subchannel produce scattered values, a real broken Q is repeatable).
func clearMajority(c []subCandidate) bool {
	first, second := 0, 0
	for _, x := range c {
		if x.n > first {
			first, second = x.n, first
		} else if x.n > second {
			second = x.n
		}
	}
	return first >= verifyReads && first > 2*second
}

// windowSettled is true when every suspect has either a valid Q or a broken Q
// with a clear majority.
func windowSettled(suspects []int, good map[int][]byte, cands map[int][]subCandidate) bool {
	for _, lba := range suspects {
		if good[lba] == nil && !clearMajority(cands[lba]) {
			return false
		}
	}
	return true
}

func (v verifyResult) text() string {
	s := fmt.Sprintf("Verification: %d suspicious sector(s) read again from the disc\n", v.Suspects)
	s += fmt.Sprintf("  repaired (read error in the first pass): %d\n", v.Repaired)
	s += fmt.Sprintf("  broken Q confirmed by every read (protection or disc defect): %d\n", len(v.Confirmed))
	for _, lba := range v.Confirmed {
		s += fmt.Sprintf("    LBA %6d  MSF %s\n", lba, lbaMSF(lba))
	}
	s += fmt.Sprintf("  unresolved (reads disagree, most frequent value kept): %d\n", len(v.Unresolved))
	for _, lba := range v.Unresolved {
		s += fmt.Sprintf("    LBA %6d  MSF %s\n", lba, lbaMSF(lba))
	}
	return s
}
