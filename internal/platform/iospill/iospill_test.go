package iospill

import (
	"bytes"
	"io"
	"os"
	"testing"
)

func TestInMemoryBelowThreshold(t *testing.T) {
	b := New(1024)
	payload := []byte("small output")
	if _, err := b.Write(payload); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if b.Spilled() {
		t.Fatal("should not spill below threshold")
	}
	r, err := b.Bytes()
	if err != nil {
		t.Fatalf("Bytes: %v", err)
	}
	got, _ := io.ReadAll(r)
	if !bytes.Equal(got, payload) {
		t.Fatalf("read %q, want %q", got, payload)
	}
	if err := b.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

func TestSpillsPastThreshold(t *testing.T) {
	b := New(64)
	defer b.Close()

	var want bytes.Buffer
	chunk := []byte("0123456789abcdef") // 16 bytes
	for i := range 10 {                 // 160 bytes total
		if _, err := b.Write(chunk); err != nil {
			t.Fatalf("Write %d: %v", i, err)
		}
		want.Write(chunk)
	}

	if !b.Spilled() {
		t.Fatal("expected spill past threshold")
	}
	if b.Len() != int64(want.Len()) {
		t.Fatalf("Len = %d, want %d", b.Len(), want.Len())
	}
	for pass := range 2 { // Bytes must be re-readable
		r, err := b.Bytes()
		if err != nil {
			t.Fatalf("Bytes pass %d: %v", pass, err)
		}
		got, _ := io.ReadAll(r)
		if !bytes.Equal(got, want.Bytes()) {
			t.Fatalf("pass %d: content mismatch (%d vs %d bytes)", pass, len(got), want.Len())
		}
	}
}

func TestCloseRemovesSpillFile(t *testing.T) {
	b := New(4)
	if _, err := b.Write([]byte("more than four bytes")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	name := b.file.Name()
	if err := b.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if _, err := os.Stat(name); !os.IsNotExist(err) {
		t.Fatalf("spill file %s must be removed on Close", name)
	}
}
