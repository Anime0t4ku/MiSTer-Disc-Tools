//go:build linux && cgo

package main

/*
#include <stdlib.h>
#include <string.h>
#include <fcntl.h>
#include <unistd.h>
#include <sys/ioctl.h>
#include <scsi/sg.h>
#include <linux/cdrom.h>

static int dt_select_speed(int fd, int speed)
{
	return ioctl(fd, CDROM_SELECT_SPEED, speed);
}

// READ CD (0xBE) of count sectors from lba with raw P-W subchannel.
// Returns 0 on success; buf receives count * (2352 + 96) bytes.
static int dt_read_cd_raw_sub(int fd, int lba, int count, int audio, unsigned char *buf, int buflen)
{
	unsigned char cmd[12], sense[32];
	sg_io_hdr_t io;

	memset(cmd, 0, sizeof(cmd));
	cmd[0] = 0xBE;
	cmd[2] = (lba >> 24) & 0xff;
	cmd[3] = (lba >> 16) & 0xff;
	cmd[4] = (lba >> 8) & 0xff;
	cmd[5] = lba & 0xff;
	cmd[6] = (count >> 16) & 0xff;
	cmd[7] = (count >> 8) & 0xff;
	cmd[8] = count & 0xff;
	cmd[9] = audio ? 0x10 : 0xF8; // full 2352-byte sector
	cmd[10] = 0x01;               // raw P-W subchannel

	memset(&io, 0, sizeof(io));
	io.interface_id = 'S';
	io.dxfer_direction = SG_DXFER_FROM_DEV;
	io.cmd_len = sizeof(cmd);
	io.cmdp = cmd;
	io.mx_sb_len = sizeof(sense);
	io.sbp = sense;
	io.dxfer_len = buflen;
	io.dxferp = buf;
	io.timeout = 20000;

	if (ioctl(fd, SG_IO, &io) < 0) return -1;
	if ((io.info & SG_INFO_OK_MASK) != SG_INFO_OK) return -2;
	return 0;
}
*/
import "C"

import (
	"fmt"
	"os"
	"syscall"
	"unsafe"
)

type rawSubDevice struct {
	f     *os.File
	audio func(lba int) bool
	buf   []byte
}

// openRawSubReader opens the drive for READ CD with raw subchannel.
// audio tells whether a sector belongs to an audio track.
func openRawSubReader(dev string, audio func(lba int) bool) (*rawSubDevice, error) {
	f, err := os.OpenFile(dev, os.O_RDWR|syscall.O_NONBLOCK, 0)
	if err != nil {
		f, err = os.OpenFile(dev, os.O_RDONLY|syscall.O_NONBLOCK, 0)
		if err != nil {
			return nil, err
		}
	}
	return &rawSubDevice{f: f, audio: audio, buf: make([]byte, verifyMaxWindow*rawSubStride)}, nil
}

func (d *rawSubDevice) Close() { d.f.Close() }

// setSpeed selects the read speed (1 = 176 kB/s; 0 = drive maximum).
func (d *rawSubDevice) setSpeed(x int) error {
	if C.dt_select_speed(C.int(d.f.Fd()), C.int(x)) != 0 {
		return fmt.Errorf("speed selection not supported")
	}
	return nil
}

func (d *rawSubDevice) read(lba, count int) ([]byte, error) {
	if count <= 0 || count > verifyMaxWindow {
		return nil, fmt.Errorf("invalid read size %d", count)
	}
	audio := 0
	if d.audio != nil && d.audio(lba) {
		audio = 1
	}
	n := count * rawSubStride
	if r := C.dt_read_cd_raw_sub(C.int(d.f.Fd()), C.int(lba), C.int(count), C.int(audio),
		(*C.uchar)(unsafe.Pointer(&d.buf[0])), C.int(n)); r != 0 {
		return nil, fmt.Errorf("READ CD failed at LBA %d (%d)", lba, int(r))
	}
	out := make([]byte, count*subLen)
	for k := 0; k < count; k++ {
		copy(out[k*subLen:(k+1)*subLen], d.buf[k*rawSubStride+rawSectorLen:(k+1)*rawSubStride])
	}
	return out, nil
}
