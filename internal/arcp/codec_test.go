package arcp

import (
	"bytes"
	"encoding/binary"
	"errors"
	"io"
	"testing"
)

func TestWriteFrameThenReadFrame(t *testing.T) {
	t.Parallel()
	e := newSampleEnvelope()
	if err := Sign(e, testPSK); err != nil {
		t.Fatalf("Sign: %v", err)
	}
	var buf bytes.Buffer
	if err := WriteFrame(&buf, e); err != nil {
		t.Fatalf("WriteFrame: %v", err)
	}

	got, err := ReadFrame(&buf)
	if err != nil {
		t.Fatalf("ReadFrame: %v", err)
	}
	if got.ID != e.ID || got.Type != e.Type || got.Auth.Sig != e.Auth.Sig {
		t.Fatalf("decoded mismatch:\n got %+v\nwant %+v", got, e)
	}
}

func TestWriteRawAndReadRawSymmetric(t *testing.T) {
	t.Parallel()
	body := []byte(`{"k":"v"}`)
	var buf bytes.Buffer
	if err := WriteRaw(&buf, body); err != nil {
		t.Fatalf("WriteRaw: %v", err)
	}

	// Verify header bytes are big-endian length.
	wireBytes := buf.Bytes()
	if len(wireBytes) < 4 {
		t.Fatalf("frame too short: %d", len(wireBytes))
	}
	got := binary.BigEndian.Uint32(wireBytes[:4])
	if got != uint32(len(body)) {
		t.Fatalf("length header = %d; want %d", got, len(body))
	}

	out, err := ReadRaw(&buf)
	if err != nil {
		t.Fatalf("ReadRaw: %v", err)
	}
	if !bytes.Equal(out, body) {
		t.Fatalf("body mismatch:\n got %s\nwant %s", out, body)
	}
}

func TestReadFrameRejectsOverlength(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	hdr := []byte{0xFF, 0xFF, 0xFF, 0xFF}
	buf.Write(hdr)
	_, err := ReadFrame(&buf)
	if !errors.Is(err, ErrEnvelopeTooLarge) {
		t.Fatalf("err = %v; want ErrEnvelopeTooLarge", err)
	}
}

func TestWriteFrameRejectsOverlength(t *testing.T) {
	t.Parallel()
	body := bytes.Repeat([]byte{'x'}, MaxEnvelopeBytes+1)
	if err := WriteRaw(io.Discard, body); !errors.Is(err, ErrEnvelopeTooLarge) {
		t.Fatalf("err = %v; want ErrEnvelopeTooLarge", err)
	}
}

func TestReadFrameReturnsEOFOnEmptyReader(t *testing.T) {
	t.Parallel()
	_, err := ReadFrame(bytes.NewReader(nil))
	if !errors.Is(err, io.EOF) {
		t.Fatalf("err = %v; want io.EOF", err)
	}
}

// TestPartialReadHandled simulates a flaky reader that delivers bytes one at
// a time. The codec must loop until the body is fully read.
func TestPartialReadHandled(t *testing.T) {
	t.Parallel()
	e := newSampleEnvelope()
	if err := Sign(e, testPSK); err != nil {
		t.Fatalf("Sign: %v", err)
	}
	var buf bytes.Buffer
	if err := WriteFrame(&buf, e); err != nil {
		t.Fatalf("WriteFrame: %v", err)
	}

	got, err := ReadFrame(&oneByteReader{src: buf.Bytes()})
	if err != nil {
		t.Fatalf("ReadFrame on one-byte reader: %v", err)
	}
	if got.ID != e.ID {
		t.Fatalf("id mismatch")
	}
}

type oneByteReader struct {
	src []byte
	pos int
}

func (r *oneByteReader) Read(p []byte) (int, error) {
	if r.pos >= len(r.src) {
		return 0, io.EOF
	}
	if len(p) == 0 {
		return 0, nil
	}
	p[0] = r.src[r.pos]
	r.pos++
	return 1, nil
}
