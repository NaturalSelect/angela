//go:build linux && (386 || mips || mipsle || ppc || ppc64 || ppc64le || s390x || sparc64)

package sandbox

import "golang.org/x/sys/unix"

// On these architectures libc can reach connect, bind, send, and every
// other socket operation through the legacy socketcall(2) multiplexer
// syscall instead of the direct syscalls in blockedNetworkSyscalls, so
// filtering only the direct numbers leaves it wide open here. Block
// the multiplexer syscall itself too, rather than trying to decode
// which subcommand it carries.
func init() {
	blockedNetworkSyscalls = append(blockedNetworkSyscalls, unix.SYS_SOCKETCALL)
}
