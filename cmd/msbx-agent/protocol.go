package main

import (
	"encoding/binary"
	"io"
	"sync"
)

const (
	frameStdin  = byte(1)
	frameResize = byte(2)
	frameOutput = byte(3)
	frameExit   = byte(4)
)

type guestFrameWriter struct {
	dst io.Writer
	mu  sync.Mutex
}

func (w *guestFrameWriter) write(frameType byte, payload []byte) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	return writeFrame(w.dst, frameType, payload)
}

func writeFrame(dst io.Writer, frameType byte, payload []byte) error {
	var header [5]byte
	header[0] = frameType
	binary.BigEndian.PutUint32(header[1:], uint32(len(payload)))
	if err := writeAll(dst, header[:]); err != nil {
		return err
	}
	return writeAll(dst, payload)
}

func writeAll(dst io.Writer, data []byte) error {
	for len(data) > 0 {
		n, err := dst.Write(data)
		if n > 0 {
			data = data[n:]
		}
		if err != nil {
			return err
		}
		if n == 0 {
			return io.ErrShortWrite
		}
	}
	return nil
}
