package main

import "syscall"

// socketpair creates the pair under ForkLock and marks both ends
// close-on-exec, since macOS has no SOCK_CLOEXEC; a fork from a thread that
// Go does not know of can still copy them in between.
func socketpair() ([2]int, error) {
	syscall.ForkLock.RLock()
	defer syscall.ForkLock.RUnlock()
	fds, err := syscall.Socketpair(syscall.AF_UNIX, syscall.SOCK_STREAM, 0)
	if err == nil {
		syscall.CloseOnExec(fds[0])
		syscall.CloseOnExec(fds[1])
	}
	return fds, err
}
