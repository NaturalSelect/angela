//go:build !linux || android

package clipboard

// isWSL always reports false outside Linux (and on Android, which
// Go's linux build tag excludes): WSL only exists on Linux kernels.
func isWSL() bool { return false }

// readImageWSL is unreachable outside Linux; isWSL always returns
// false there, so callers never invoke it.
func readImageWSL() ([]byte, error) { return nil, ErrUnsupported }
