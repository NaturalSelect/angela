//go:build linux

package sandbox

import (
	"fmt"
	"os"
	"runtime"
	"syscall"
	"unsafe"

	"golang.org/x/sys/unix"
)

// blockedNetworkSyscalls lists the syscalls installChildNetworkFilter
// blocks: everything needed to open or use a network connection. This
// mirrors grok-build's xai-grok-sandbox child network filter, plus
// SYS_SENDMMSG, which grok-build's own list omits and which otherwise
// lets a program send UDP datagrams on an unconnected socket without
// ever calling SYS_SENDTO or SYS_SENDMSG. unix.SYS_ACCEPT is added to
// this list separately in child_net_linux_accept.go: it doesn't exist
// on linux/386, the one Linux architecture without a direct accept(2)
// syscall.
var blockedNetworkSyscalls = []uint32{
	unix.SYS_CONNECT,
	unix.SYS_BIND,
	unix.SYS_SENDTO,
	unix.SYS_SENDMSG,
	unix.SYS_SENDMMSG,
	unix.SYS_LISTEN,
	unix.SYS_ACCEPT4,
}

const (
	seccompRetAllow uint32 = 0x7fff0000
	seccompRetErrno uint32 = 0x00050000
)

// buildChildNetworkFilter assembles a classic-BPF seccomp program that
// returns EPERM for any syscall in blockedNetworkSyscalls and allows
// everything else.
func buildChildNetworkFilter() []unix.SockFilter {
	n := len(blockedNetworkSyscalls)
	filter := make([]unix.SockFilter, 0, n+2)
	// Load the syscall number: the first 4-byte field of seccomp_data.
	filter = append(filter, unix.SockFilter{Code: unix.BPF_LD | unix.BPF_W | unix.BPF_ABS, K: 0})
	for i, sys := range blockedNetworkSyscalls {
		remaining := n - i - 1
		filter = append(filter, unix.SockFilter{
			Code: unix.BPF_JMP | unix.BPF_JEQ | unix.BPF_K,
			Jt:   uint8(remaining) + 1,
			K:    sys,
		})
	}
	filter = append(filter, unix.SockFilter{Code: unix.BPF_RET | unix.BPF_K, K: seccompRetAllow})
	filter = append(filter, unix.SockFilter{Code: unix.BPF_RET | unix.BPF_K, K: seccompRetErrno | uint32(unix.EPERM)})
	return filter
}

// installChildNetworkFilter installs buildChildNetworkFilter on the
// current process via seccomp. It must run on a process about to exec
// into the command it's meant to restrict: the filter persists across
// exec and is inherited by every descendant that command spawns.
func installChildNetworkFilter() error {
	filter := buildChildNetworkFilter()
	prog := unix.SockFprog{
		Len:    uint16(len(filter)),
		Filter: &filter[0],
	}
	if err := unix.Prctl(unix.PR_SET_NO_NEW_PRIVS, 1, 0, 0, 0); err != nil {
		return fmt.Errorf("set no_new_privs: %w", err)
	}
	err := unix.Prctl(unix.PR_SET_SECCOMP, unix.SECCOMP_MODE_FILTER, uintptr(unsafe.Pointer(&prog)), 0, 0)
	runtime.KeepAlive(&prog)
	if err != nil {
		return fmt.Errorf("install seccomp filter: %w", err)
	}
	return nil
}

// runChildExecLauncher installs the outbound-network filter on the
// current process, then execs into rest[0] with rest[1:] as its argv,
// replacing this process. It never returns on success.
func runChildExecLauncher(rest []string) {
	if len(rest) == 0 {
		fmt.Fprintln(os.Stderr, "angela: sandbox child launcher: missing command")
		os.Exit(127)
	}

	// Lock to the current OS thread before touching seccomp: exec
	// only carries the calling thread's filter into the new program
	// image, and an unlocked goroutine could otherwise resume on a
	// different thread between installing the filter and calling
	// Exec below, leaving the real command unrestricted.
	runtime.LockOSThread()

	if err := installChildNetworkFilter(); err != nil {
		fmt.Fprintln(os.Stderr, "angela: sandbox child launcher: install network filter:", err)
		os.Exit(127)
	}

	err := syscall.Exec(rest[0], rest[1:], os.Environ())
	fmt.Fprintln(os.Stderr, "angela: sandbox child launcher: exec:", err)
	os.Exit(127)
}
