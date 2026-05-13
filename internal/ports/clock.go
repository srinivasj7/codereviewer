package ports

import "time"

// Clock returns the current wall-clock time. Injected for testability;
// tests use a fake that returns a fixed value.
type Clock interface {
	Now() time.Time
}
