//go:build linux || darwin

package credential

import (
	"bytes"
	"errors"
	"io"
	"os"

	"golang.org/x/sys/unix"

	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/common/secret"
)

// ReadKeyFile reads secret material from path. It opens path with O_NOFOLLOW,
// verifies that it resolves to a regular file with mode 0600 or stricter,
// trims one trailing "\n" (a "\r" before it stays), and refuses an empty file.
func ReadKeyFile(path string) (secret.Value, error) {
	// O_NONBLOCK keeps the open of a FIFO from waiting for a writer, so
	// checkSecure refuses it; it has no effect on a regular file.
	fd, err := unix.Open(path, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_CLOEXEC|unix.O_NONBLOCK, 0)
	if err != nil {
		switch {
		case errors.Is(err, unix.ELOOP):
			return secret.Value{}, errs.From(err).Code(ErrCodeSymlinkRefused).Attr("path", path).Msg("key file is a symlink")
		case errors.Is(err, unix.ENOENT):
			return secret.Value{}, errs.From(err).Code(ErrCodeNotFound).Attr("path", path).Msg("key file does not exist")
		default:
			return secret.Value{}, errs.From(err).Code(ErrCodeReadFailed).Attr("path", path).Msg("open key file")
		}
	}
	f := os.NewFile(uintptr(fd), path)
	defer func() { _ = f.Close() }()

	if _, err := checkSecure(f, path); err != nil {
		return secret.Value{}, err
	}

	data, err := io.ReadAll(f)
	if err != nil {
		return secret.Value{}, errs.From(err).Code(ErrCodeReadFailed).Attr("path", path).Msg("read key file")
	}

	data = bytes.TrimSuffix(data, []byte{'\n'})

	if len(data) == 0 {
		return secret.Value{}, errs.New().Code(ErrCodeInvalidMaterial).Attr("path", path).Msg("key file is empty")
	}

	return secret.New(bytes.Clone(data)), nil
}
