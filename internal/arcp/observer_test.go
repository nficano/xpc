package arcp

import (
	"bytes"
	"sync"
	"testing"
)

func TestObserverFiresOnSendAndRecv(t *testing.T) {
	t.Cleanup(func() { SetObserver(nil) })

	var (
		mu   sync.Mutex
		seen []Direction
	)
	SetObserver(func(dir Direction, e *Envelope) {
		if e == nil {
			t.Errorf("observer received nil envelope")
		}
		mu.Lock()
		defer mu.Unlock()
		seen = append(seen, dir)
	})

	env := New("msg-1", "tools.list", "2026-01-01T00:00:00.000000Z")
	env.SessionID = "sess-1"
	if err := Sign(env, []byte("psk-bytes")); err != nil {
		t.Fatalf("Sign: %v", err)
	}

	var buf bytes.Buffer
	if err := WriteFrame(&buf, env); err != nil {
		t.Fatalf("WriteFrame: %v", err)
	}
	if _, err := ReadFrame(&buf); err != nil {
		t.Fatalf("ReadFrame: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(seen) != 2 {
		t.Fatalf("expected 2 observations, got %d: %v", len(seen), seen)
	}
	if seen[0] != DirSend {
		t.Errorf("first observation: want %q, got %q", DirSend, seen[0])
	}
	if seen[1] != DirRecv {
		t.Errorf("second observation: want %q, got %q", DirRecv, seen[1])
	}
}

func TestObserverFastPathWhenUnset(t *testing.T) {
	SetObserver(nil)

	env := New("msg-2", "tools.list", "2026-01-01T00:00:00.000000Z")
	if err := Sign(env, []byte("psk-bytes")); err != nil {
		t.Fatalf("Sign: %v", err)
	}

	var buf bytes.Buffer
	if err := WriteFrame(&buf, env); err != nil {
		t.Fatalf("WriteFrame: %v", err)
	}
	if _, err := ReadFrame(&buf); err != nil {
		t.Fatalf("ReadFrame: %v", err)
	}
}
