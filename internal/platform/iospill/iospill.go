// Package iospill provides a bounded-memory output capture: bytes are held
// in memory up to a threshold and spill to a temporary file beyond it, so a
// wrapped command with gigabytes of output cannot exhaust memory.
package iospill

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"sync"
)

// Buffer implements exec.Spill. It is safe for a single writer; readers must
// only call Bytes after writing has finished.
type Buffer struct {
	mu        sync.Mutex
	threshold int64
	mem       bytes.Buffer
	file      *os.File
	size      int64
}

// New returns a Buffer that spills to a temp file once more than threshold
// bytes have been written.
func New(threshold int64) *Buffer {
	return &Buffer{threshold: threshold}
}

// Write implements io.Writer.
func (b *Buffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	if b.file == nil && b.size+int64(len(p)) > b.threshold {
		f, err := os.CreateTemp("", "sctx-spill-*")
		if err != nil {
			return 0, fmt.Errorf("creating spill file: %w", err)
		}
		if _, err := f.Write(b.mem.Bytes()); err != nil {
			f.Close()
			os.Remove(f.Name())
			return 0, fmt.Errorf("writing spill file: %w", err)
		}
		b.mem.Reset()
		b.file = f
	}

	var n int
	var err error
	if b.file != nil {
		n, err = b.file.Write(p)
	} else {
		n, err = b.mem.Write(p)
	}
	b.size += int64(n)
	return n, err
}

// Bytes returns a reader over everything written so far, positioned at the
// start. It may be called multiple times.
func (b *Buffer) Bytes() (io.Reader, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	if b.file != nil {
		if _, err := b.file.Seek(0, io.SeekStart); err != nil {
			return nil, fmt.Errorf("rewinding spill file: %w", err)
		}
		return b.file, nil
	}
	return bytes.NewReader(b.mem.Bytes()), nil
}

// Len returns the total number of bytes written.
func (b *Buffer) Len() int64 {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.size
}

// Spilled reports whether the buffer overflowed to disk.
func (b *Buffer) Spilled() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.file != nil
}

// Close removes the spill file, if any.
func (b *Buffer) Close() error {
	b.mu.Lock()
	defer b.mu.Unlock()

	if b.file == nil {
		return nil
	}
	name := b.file.Name()
	err := b.file.Close()
	if rmErr := os.Remove(name); err == nil {
		err = rmErr
	}
	b.file = nil
	return err
}
