//go:build !unix

package android

import "io/fs"

func blocksUsed(info fs.FileInfo) int64 { return info.Size() }
