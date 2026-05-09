package cli

import (
	"net"
	"time"
)

// newTCPDialer returns a net.Dialer with the given timeout. Wrapped in a
// helper so tests can swap it out.
func newTCPDialer(timeout time.Duration) *net.Dialer {
	if timeout <= 0 {
		timeout = 5 * time.Second
	}
	return &net.Dialer{Timeout: timeout}
}
