//go:build unix

package android

import (
	"io/fs"
	"syscall"
)

// blocksUsed is the disk a file takes: the blocks it was given, which is less than its length when
// it is sparse.
func blocksUsed(info fs.FileInfo) int64 {
	if st, ok := info.Sys().(*syscall.Stat_t); ok {
		return int64(st.Blocks) * 512
	}
	return info.Size()
}
