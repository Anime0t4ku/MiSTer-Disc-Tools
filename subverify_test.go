package main

import (
	"math/rand"
	"testing"
)

// A simulated drive with the behaviour seen on a real USB drive: subchannel
// one sector late, some read commands with the other phase, a stale Q right
// after the seek and random bit errors in the raw subchannel.
func TestVerifySubchannelRepairsFirstPass(t *testing.T) {
	const frames = 4000
	protected := map[int]bool{1000: true, 1005: true, 2500: true, 2505: true}
	truth := func(lba int) []byte {
		raw := make([]byte, subLen)
		q := testQ(lba)
		if protected[lba] {
			q[8] ^= 0x01 // broken CRC, like LibCrypt
		}
		qIntoRaw(raw, q)
		return raw
	}
	rng := rand.New(rand.NewSource(7))
	// read cache: a command for the same sectors is answered from the cache
	// (read errors included) until enough other sectors were read
	type cached struct {
		start, count int
		at           int
		data         []byte
	}
	var cache []cached
	readTotal := 0
	drive := func(lba, count int) ([]byte, error) {
		readTotal += count
		for _, c := range cache {
			if c.start == lba && c.count == count && readTotal-c.at < 3000 {
				return append([]byte(nil), c.data...), nil
			}
		}
		out := make([]byte, count*subLen)
		defer func() {
			cache = append(cache, cached{lba, count, readTotal, append([]byte(nil), out...)})
			if len(cache) > 64 {
				cache = cache[1:]
			}
		}()
		phase := -1
		if rng.Intn(5) == 0 {
			phase = 0
		}
		for k := 0; k < count; k++ {
			src := lba + k + phase
			if src < 0 {
				src = 0
			}
			copy(out[k*subLen:], truth(src))
			if k == 0 && rng.Intn(2) == 0 {
				copy(out[:subLen], truth(src-7))
			}
			if rng.Intn(60) == 0 {
				out[k*subLen+rng.Intn(96)] ^= 0x40
			}
		}
		return out, nil
	}

	// first pass: one continuous read, realigned, with the typical damage
	subs := make([]byte, frames*subLen)
	for l := 0; l < frames; l++ {
		copy(subs[l*subLen:], truth(l))
	}
	for l := 1200; l < 1252; l++ { // block with the other phase: Q of the next sector
		copy(subs[l*subLen:], truth(l+1))
	}
	copy(subs[2505*subLen:], truth(2506)) // protected sector lost
	for _, l := range []int{10, 700, 1001, 3999} {
		subs[l*subLen+30] ^= 0x40 // random read errors
	}

	res, err := verifySubchannel(subs, frames, drive, make(chan struct{}), func() {})
	if err != nil {
		t.Fatal(err)
	}
	for l := 0; l < frames; l++ {
		q := qFromRaw(subs[l*subLen : (l+1)*subLen])
		if want := qFromRaw(truth(l)); q != want {
			t.Fatalf("LBA %d: got % x want % x", l, q, want)
		}
	}
	if len(res.Confirmed) != len(protected) || len(res.Unresolved) != 0 {
		t.Fatalf("confirmed %v unresolved %v", res.Confirmed, res.Unresolved)
	}
}
