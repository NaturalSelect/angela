//go:build linux && !386

package sandbox

import "golang.org/x/sys/unix"

// linux/386 is the only Linux architecture without a direct accept(2)
// syscall number; add it everywhere else.
func init() {
	blockedNetworkSyscalls = append(blockedNetworkSyscalls, unix.SYS_ACCEPT)
}
