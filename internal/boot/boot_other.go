// ABOUTME: Boot information for operating systems other than Linux, where
// ABOUTME: there is no boot_id or boottime clock to read.

//go:build !linux

package boot

// ID returns ErrUnsupportedPlatform.
func ID() (string, error) {
	return "", ErrUnsupportedPlatform
}

// Uptime returns ErrUnsupportedPlatform.
func Uptime() (float64, error) {
	return 0, ErrUnsupportedPlatform
}
