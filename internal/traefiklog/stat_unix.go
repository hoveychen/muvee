//go:build unix

package traefiklog

import (
	"os"
	"syscall"
)

func statInode(fi os.FileInfo) uint64 {
	if st, ok := fi.Sys().(*syscall.Stat_t); ok {
		return st.Ino
	}
	return 0
}
