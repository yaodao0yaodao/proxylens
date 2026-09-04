//go:build windows

package web

import (
	"path/filepath"

	"golang.org/x/sys/windows"
)

func partitionFree(path string) (int64, error) {
	root := filepath.VolumeName(path) + `\`
	pointer, err := windows.UTF16PtrFromString(root)
	if err != nil {
		return 0, err
	}
	var available uint64
	if err = windows.GetDiskFreeSpaceEx(pointer, &available, nil, nil); err != nil {
		return 0, err
	}
	return int64(available), nil
}
