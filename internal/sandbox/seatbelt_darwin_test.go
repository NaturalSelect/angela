//go:build darwin

package sandbox

// runPlatformHelperIfRequested reports whether this process's
// environment names a darwin-only helper. There are none anymore:
// the Seatbelt scenarios that used to live in this file now run
// through the shared enforce_test.go table alongside Landlock's, so
// the same Config is proven to mean the same thing on both
// platforms. This stub only exists so main_test.go's TestMain can
// call the same two functions on every platform without a build-tag
// switch of its own.
func runPlatformHelperIfRequested() (int, bool) {
	return 0, false
}
