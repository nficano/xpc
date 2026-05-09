package arcp

import "sync/atomic"

// Direction indicates whether an envelope is being sent or received.
type Direction string

const (
	DirSend Direction = "send"
	DirRecv Direction = "recv"
)

// Observer is invoked for every envelope read or written through the
// codec. Implementations must be safe for concurrent calls and should not
// block; the codec calls them inline on the I/O path.
type Observer func(dir Direction, e *Envelope)

// observer is read on every frame; use atomic.Pointer so the fast path
// (no observer set) is a single atomic load.
var observer atomic.Pointer[Observer]

// SetObserver installs fn as the global ARCP observer. Pass nil to remove.
func SetObserver(fn Observer) {
	if fn == nil {
		observer.Store(nil)
		return
	}
	observer.Store(&fn)
}

func emit(dir Direction, e *Envelope) {
	o := observer.Load()
	if o == nil || e == nil {
		return
	}
	(*o)(dir, e)
}
