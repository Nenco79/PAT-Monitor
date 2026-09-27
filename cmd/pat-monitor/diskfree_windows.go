package main

import "golang.org/x/sys/windows"

// diskFree asks Windows how many bytes this user may still write on the disk
// that holds dir.
//
// **The figure available to the caller, not the disk's total free**, which is
// what `GetDiskFreeSpaceEx` gives first: a disk quota makes the two differ, and
// the one that decides whether a clip fits is this user's.
func diskFree(dir string) (uint64, error) {
	p, err := windows.UTF16PtrFromString(dir)
	if err != nil {
		return 0, err
	}
	var free uint64
	if err := windows.GetDiskFreeSpaceEx(p, &free, nil, nil); err != nil {
		return 0, err
	}
	return free, nil
}
