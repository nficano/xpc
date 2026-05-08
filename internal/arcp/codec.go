package arcp

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
)

// ErrEnvelopeTooLarge is returned when a frame's declared length exceeds
// MaxEnvelopeBytes. Per docs/PROTOCOL.md §2 the connection should be closed.
var ErrEnvelopeTooLarge = errors.New("arcp: envelope exceeds MaxEnvelopeBytes")

// WriteFrame serializes the envelope (signed if needed by the caller) and
// writes it to w with a 4-byte big-endian length prefix.
func WriteFrame(w io.Writer, e *Envelope) error {
	body, err := e.Marshal()
	if err != nil {
		return err
	}
	return WriteRaw(w, body)
}

// WriteRaw is a lower-level helper that frames already-encoded bytes. Useful
// when writing pre-marshaled envelope bytes (e.g. test corpus replay).
func WriteRaw(w io.Writer, body []byte) error {
	if len(body) > MaxEnvelopeBytes {
		return ErrEnvelopeTooLarge
	}
	var hdr [4]byte
	binary.BigEndian.PutUint32(hdr[:], uint32(len(body)))
	if _, err := w.Write(hdr[:]); err != nil {
		return fmt.Errorf("arcp: write header: %w", err)
	}
	if _, err := w.Write(body); err != nil {
		return fmt.Errorf("arcp: write body: %w", err)
	}
	return nil
}

// ReadFrame reads a length-prefixed envelope from r and returns the parsed
// Envelope. On clean EOF before the header is read it returns io.EOF.
func ReadFrame(r io.Reader) (*Envelope, error) {
	body, err := ReadRaw(r)
	if err != nil {
		return nil, err
	}
	return Unmarshal(body)
}

// ReadRaw reads the framing header and the body bytes; it does not parse
// JSON.
func ReadRaw(r io.Reader) ([]byte, error) {
	var hdr [4]byte
	if _, err := io.ReadFull(r, hdr[:]); err != nil {
		return nil, err
	}
	length := binary.BigEndian.Uint32(hdr[:])
	if length > MaxEnvelopeBytes {
		return nil, ErrEnvelopeTooLarge
	}
	body := make([]byte, length)
	if _, err := io.ReadFull(r, body); err != nil {
		return nil, fmt.Errorf("arcp: read body: %w", err)
	}
	return body, nil
}
