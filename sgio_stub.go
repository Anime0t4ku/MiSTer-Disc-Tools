//go:build !linux || !cgo

package main

import "errors"

type rawSubDevice struct{}

func openRawSubReader(dev string, audio func(lba int) bool) (*rawSubDevice, error) {
	return nil, errors.New("raw subchannel re-reading needs linux and cgo")
}

func (d *rawSubDevice) Close() {}

func (d *rawSubDevice) read(lba, count int) ([]byte, error) {
	return nil, errors.New("not supported")
}
