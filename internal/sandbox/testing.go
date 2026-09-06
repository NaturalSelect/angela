package sandbox

// SetRestrictChildNetworkForTest overrides the state
// ShouldRestrictChildNetwork reports, for tests in other packages
// (the shell tool) that need to exercise the child-network-wrapping
// path without a real, process-wide EnterSandbox call. It returns a
// restore func that must be called to put the original value back.
func SetRestrictChildNetworkForTest(v bool) (restore func()) {
	orig := restrictChildNetwork.Swap(v)
	return func() { restrictChildNetwork.Store(orig) }
}
