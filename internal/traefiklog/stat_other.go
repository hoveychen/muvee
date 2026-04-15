//go:build !unix

package traefiklog

import "os"

func statInode(fi os.FileInfo) uint64 { return 0 }
