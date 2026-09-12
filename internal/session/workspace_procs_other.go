//go:build !linux && !darwin

package session

// scanCWDs has no implementation on this platform, so nothing is ever seen
// holding a workspace open. Callers already treat an empty result as "nothing
// seen" rather than a guarantee, and cope with the removal failing instead.
func scanCWDs() []procCWD { return nil }
