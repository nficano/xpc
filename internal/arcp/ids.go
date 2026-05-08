package arcp

import (
	"crypto/rand"
	"encoding/base32"
	"fmt"
	"strings"
	"time"
)

// IDPrefix is one of the recognized envelope-id prefixes per docs/PROTOCOL.md
// §3.2.
type IDPrefix string

// Recognized id prefixes.
const (
	PrefixMessage IDPrefix = "msg"
	PrefixSession IDPrefix = "sess"
	PrefixJob     IDPrefix = "job"
	PrefixStream  IDPrefix = "str"
	PrefixTrace   IDPrefix = "tr"
	PrefixSpan    IDPrefix = "sp"
)

// idEncoding is Crockford-base32 lowercase, padding stripped. Matches the
// look in ARCP RFC examples (e.g. "msg_01HABCDEF...").
var idEncoding = base32.NewEncoding("0123456789abcdefghjkmnpqrstvwxyz").WithPadding(base32.NoPadding)

// NewID generates a fresh envelope id under prefix. The result is
// "<prefix>_<26 base32 chars>" (16 random bytes encoded).
func NewID(prefix IDPrefix) (string, error) {
	if prefix == "" {
		return "", fmt.Errorf("arcp: empty id prefix")
	}
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", fmt.Errorf("arcp: rand: %w", err)
	}
	return string(prefix) + "_" + idEncoding.EncodeToString(raw[:]), nil
}

// MustNewID is NewID without the error. Panics on entropy failure.
func MustNewID(prefix IDPrefix) string {
	id, err := NewID(prefix)
	if err != nil {
		panic(err)
	}
	return id
}

// SplitID returns the prefix and base32 portion of an envelope id. Returns
// the empty strings if the id is malformed.
func SplitID(id string) (prefix, body string) {
	idx := strings.IndexByte(id, '_')
	if idx <= 0 || idx == len(id)-1 {
		return "", ""
	}
	return id[:idx], id[idx+1:]
}

// FormatTimestamp returns the RFC 3339 / ISO 8601 timestamp form expected on
// envelopes (UTC, microsecond precision, trailing Z).
//
// Example: "2026-05-08T18:21:00.000000Z"
func FormatTimestamp(t time.Time) string {
	return t.UTC().Format("2006-01-02T15:04:05.000000Z")
}
