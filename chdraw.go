package main

// Verification of an uncompressed CD CHD against the image it was made from.
//
// chdman verify has nothing to check in an uncompressed CHD ("No verification
// to be done; CHD is uncompressed"), so ULTRA FAST CHD compares every frame of
// the CHD, data and subchannel, with the native cdrdao BIN instead.

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"regexp"
	"strconv"
)

const (
	chdV5HeaderLen  = 124
	chdCDFrameBytes = 2448 // 2352 sector + 96 subchannel, the CHD unit of CD images
	chdTrackPadding = 4    // chdman pads every track to a multiple of 4 frames
)

var chdFramesRE = regexp.MustCompile(`FRAMES:(\d+)`)

type chdV5 struct {
	f           *os.File
	compressors [4]uint32
	logical     uint64
	mapOffset   uint64
	metaOffset  uint64
	hunkBytes   uint32
	unitBytes   uint32
}

func openCHDv5(path string) (*chdV5, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	h := make([]byte, chdV5HeaderLen)
	if _, err := io.ReadFull(f, h); err != nil {
		f.Close()
		return nil, fmt.Errorf("CHD header: %v", err)
	}
	if string(h[0:8]) != "MComprHD" || binary.BigEndian.Uint32(h[12:16]) != 5 {
		f.Close()
		return nil, errors.New("not a version 5 CHD")
	}
	c := &chdV5{f: f}
	for i := 0; i < 4; i++ {
		c.compressors[i] = binary.BigEndian.Uint32(h[16+i*4:])
	}
	c.logical = binary.BigEndian.Uint64(h[32:])
	c.mapOffset = binary.BigEndian.Uint64(h[40:])
	c.metaOffset = binary.BigEndian.Uint64(h[48:])
	c.hunkBytes = binary.BigEndian.Uint32(h[56:])
	c.unitBytes = binary.BigEndian.Uint32(h[60:])
	return c, nil
}

func (c *chdV5) Close() { c.f.Close() }

// trackFrames reads the frame count of every track from the CD metadata.
func (c *chdV5) trackFrames() ([]int, error) {
	var frames []int
	off := c.metaOffset
	for n := 0; off != 0 && n < 1000; n++ {
		hdr := make([]byte, 16)
		if _, err := c.f.ReadAt(hdr, int64(off)); err != nil {
			return nil, fmt.Errorf("CHD metadata: %v", err)
		}
		tag := string(hdr[0:4])
		length := binary.BigEndian.Uint32(hdr[4:8]) & 0xFFFFFF
		next := binary.BigEndian.Uint64(hdr[8:16])
		if tag == "CHT2" || tag == "CHTR" {
			data := make([]byte, length)
			if _, err := c.f.ReadAt(data, int64(off)+16); err != nil {
				return nil, fmt.Errorf("CHD metadata: %v", err)
			}
			m := chdFramesRE.FindSubmatch(data)
			if m == nil {
				return nil, errors.New("CHD track metadata without FRAMES")
			}
			v, _ := strconv.Atoi(string(m[1]))
			frames = append(frames, v)
		}
		off = next
	}
	if len(frames) == 0 {
		return nil, errors.New("CHD has no CD track metadata")
	}
	return frames, nil
}

// verifyUncompressedCHD compares every frame of an uncompressed CD CHD with
// the native BIN (2448 bytes per frame, tracks back to back).
func verifyUncompressedCHD(chdPath, binPath string, cancel <-chan struct{}, onBytes func(int64)) error {
	c, err := openCHDv5(chdPath)
	if err != nil {
		return err
	}
	defer c.Close()
	if c.compressors[0] != 0 {
		return errors.New("CHD is compressed")
	}
	if c.unitBytes != chdCDFrameBytes || c.hunkBytes == 0 || c.hunkBytes%chdCDFrameBytes != 0 {
		return fmt.Errorf("unexpected CHD layout (hunk %d, unit %d)", c.hunkBytes, c.unitBytes)
	}
	tracks, err := c.trackFrames()
	if err != nil {
		return err
	}
	fph := int(c.hunkBytes / chdCDFrameBytes)
	hunks := int((c.logical + uint64(c.hunkBytes) - 1) / uint64(c.hunkBytes))
	rawMap := make([]byte, hunks*4)
	if _, err := c.f.ReadAt(rawMap, int64(c.mapOffset)); err != nil {
		return fmt.Errorf("CHD map: %v", err)
	}

	bf, err := os.Open(binPath)
	if err != nil {
		return err
	}
	defer bf.Close()
	bin := bufio.NewReaderSize(bf, 1<<20)

	hunk := make([]byte, c.hunkBytes)
	curHunk := -1
	want := make([]byte, chdCDFrameBytes)
	chdFrame := 0
	for t, n := range tracks {
		for k := 0; k < n; k++ {
			if k%256 == 0 {
				select {
				case <-cancel:
					return errors.New("cancelled")
				default:
				}
			}
			if _, err := io.ReadFull(bin, want); err != nil {
				return fmt.Errorf("BIN shorter than the CHD (track %d): %v", t+1, err)
			}
			h := chdFrame / fph
			if h >= hunks {
				return errors.New("CHD shorter than its track list")
			}
			if h != curHunk {
				entry := binary.BigEndian.Uint32(rawMap[h*4:])
				if entry == 0 {
					for i := range hunk {
						hunk[i] = 0
					}
				} else if _, err := c.f.ReadAt(hunk, int64(entry)*int64(c.hunkBytes)); err != nil {
					return fmt.Errorf("CHD hunk %d: %v", h, err)
				}
				curHunk = h
			}
			got := hunk[(chdFrame%fph)*chdCDFrameBytes : (chdFrame%fph+1)*chdCDFrameBytes]
			if !bytes.Equal(got, want) {
				what := "data"
				if bytes.Equal(got[:2352], want[:2352]) {
					what = "subchannel"
				}
				return fmt.Errorf("CHD differs from the rip: track %d frame %d (%s)", t+1, k, what)
			}
			chdFrame++
			if onBytes != nil {
				onBytes(chdCDFrameBytes)
			}
		}
		// padding frames between tracks
		chdFrame += (chdTrackPadding - n%chdTrackPadding) % chdTrackPadding
	}
	if _, err := bin.ReadByte(); err != io.EOF {
		return errors.New("BIN longer than the CHD")
	}
	return nil
}
