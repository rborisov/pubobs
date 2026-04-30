//go:build !windows

package gitcache

import "syscall"

func diskUsage(path string) (freeBytes int64, freePct float64, err error) {
	var stat syscall.Statfs_t
	if err = syscall.Statfs(path, &stat); err != nil {
		return
	}
	freeBytes = int64(stat.Bavail) * int64(stat.Bsize)
	total := int64(stat.Blocks) * int64(stat.Bsize)
	if total > 0 {
		freePct = float64(freeBytes) / float64(total) * 100
	}
	return
}
