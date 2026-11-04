package main

import "syscall"

// socketpair creates both ends close-on-exec atomically.
func socketpair() ([2]int, error) {
	return syscall.Socketpair(syscall.AF_UNIX, syscall.SOCK_STREAM|syscall.SOCK_CLOEXEC, 0)
}
