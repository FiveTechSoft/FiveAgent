//go:build !windows

package sandbox

import (
	"fmt"
	"time"
)

func newJobObject(string, time.Duration, int) (Sandbox, error) {
	return nil, fmt.Errorf("sandbox: the jobobject backend is Windows-only")
}
