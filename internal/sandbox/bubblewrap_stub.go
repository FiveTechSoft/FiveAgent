//go:build !linux

package sandbox

import (
	"fmt"
	"time"
)

func newBubblewrap(string, time.Duration) (Sandbox, error) {
	return nil, fmt.Errorf("sandbox: the bubblewrap backend is Linux-only")
}
