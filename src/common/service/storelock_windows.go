//go:build windows

package service

import (
	"errors"
	"os"

	"golang.org/x/sys/windows"

	"go.aledante.io/FlowSeer/src/common/errs"
)

type storeLock interface {
	Close() error
}

type fileStoreLock struct {
	file       *os.File
	overlapped windows.Overlapped
}

func acquireStoreLock(path string) (storeLock, error) {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, busUnhealthy(err, "open local bus store lock")
	}
	lock := &fileStoreLock{file: file}
	err = windows.LockFileEx(
		windows.Handle(file.Fd()),
		windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY,
		0,
		1,
		0,
		&lock.overlapped,
	)
	if err != nil {
		_ = file.Close()
		if errors.Is(err, windows.ERROR_LOCK_VIOLATION) {
			return nil, errs.From(err).Code(errCodeBusStoreLocked).Msg("local bus store is already in use")
		}
		return nil, busUnhealthy(err, "lock local bus store")
	}
	return lock, nil
}

func restrictStoreDir(string) error { return nil }

func (l *fileStoreLock) Close() error {
	return errors.Join(
		windows.UnlockFileEx(windows.Handle(l.file.Fd()), 0, 1, 0, &l.overlapped),
		l.file.Close(),
	)
}
