//go:build linux || darwin

package service

import (
	"errors"
	"os"

	"golang.org/x/sys/unix"

	"go.aledante.io/FlowSeer/src/common/errs"
)

type fileStoreLock struct {
	file *os.File
}

// acquireStoreLock takes a non-blocking exclusive lock on path. Contention maps
// to errCodeBusStoreLocked; the caller must close the returned lock.
func acquireStoreLock(path string) (*fileStoreLock, error) {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, busUnhealthy(err, "open local bus store lock")
	}
	if err := unix.Flock(int(file.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		_ = file.Close()
		if errors.Is(err, unix.EWOULDBLOCK) || errors.Is(err, unix.EAGAIN) {
			return nil, errs.From(err).Code(errCodeBusStoreLocked).Msg("local bus store is already in use")
		}
		return nil, busUnhealthy(err, "lock local bus store")
	}
	return &fileStoreLock{file: file}, nil
}

func restrictStoreDir(path string) error {
	return os.Chmod(path, 0o700)
}

func (l *fileStoreLock) Close() error {
	return errors.Join(unix.Flock(int(l.file.Fd()), unix.LOCK_UN), l.file.Close())
}
