// Package clocksystem wraps time.Now as a ports.Clock implementation.
package clocksystem

import (
	"time"

	"codereviewer/internal/ports"
)

// Clock returns the OS time.
type Clock struct{}

// New returns a Clock.
func New() ports.Clock { return Clock{} }

// Now returns time.Now in the local timezone.
func (Clock) Now() time.Time { return time.Now() }
