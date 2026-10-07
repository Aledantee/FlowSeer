"""Hold an exclusive lock on a file while appending to it."""

import contextlib
import os
import sys
import time
from collections.abc import Iterator
from pathlib import Path

# Seconds a Windows lock waits in all. LK_LOCK gives up after 10 attempts one
# second apart and raises OSError. The errno it sets is unverified, so any
# OSError is retried until this deadline and then re-raised.
WINDOWS_DEADLINE = 60.0


@contextlib.contextmanager
def exclusive(path: Path | str) -> Iterator[int]:
    """Yield a descriptor on path, open for appending, while holding its lock.

    On POSIX the lock is flock on the descriptor itself, so a process that
    locks the same file with flock excludes this one. On Windows the lock is
    byte 0 of a sidecar `<path>.lock`, since a locked region may block a
    reader of those bytes and the log is read without the lock.
    """
    flags = os.O_RDWR | os.O_CREAT | os.O_APPEND | getattr(os, "O_BINARY", 0)
    descriptor = os.open(str(path), flags, 0o600)
    try:
        if sys.platform == "win32":
            sidecar = os.open(f"{path}.lock", os.O_RDWR | os.O_CREAT | os.O_BINARY, 0o600)
            try:
                _lock_windows(sidecar)
                try:
                    yield descriptor
                finally:
                    _unlock_windows(sidecar)
            finally:
                os.close(sidecar)
        else:
            import fcntl

            fcntl.flock(descriptor, fcntl.LOCK_EX)
            try:
                yield descriptor
            finally:
                fcntl.flock(descriptor, fcntl.LOCK_UN)
    finally:
        os.close(descriptor)


def _lock_windows(descriptor: int) -> None:
    import msvcrt

    deadline = time.monotonic() + WINDOWS_DEADLINE
    while True:
        os.lseek(descriptor, 0, os.SEEK_SET)
        try:
            msvcrt.locking(descriptor, msvcrt.LK_LOCK, 1)
            return
        except OSError:
            if time.monotonic() >= deadline:
                raise


def _unlock_windows(descriptor: int) -> None:
    import msvcrt

    os.lseek(descriptor, 0, os.SEEK_SET)
    msvcrt.locking(descriptor, msvcrt.LK_UNLCK, 1)
