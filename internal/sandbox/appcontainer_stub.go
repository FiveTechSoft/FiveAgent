//go:build !windows

package sandbox

import (
	"fmt"
	"time"
)

func newAppContainer(string, time.Duration, int) (Sandbox, error) {
	return nil, fmt.Errorf("sandbox: the appcontainer backend is Windows-only")
}
