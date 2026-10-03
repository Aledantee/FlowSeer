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
// trims one trailing newline, and refuses an empty file.
func ReadKeyFile(path string) (secret.Value, error) {
	fd, err := unix.Open(path, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
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

	if len(data) > 0 && data[len(data)-1] == '\n' {
		data = data[:len(data)-1]
		if len(data) > 0 && data[len(data)-1] == '\r' {
			data = data[:len(data)-1]
		}
	}

	if len(data) == 0 {
		return secret.Value{}, errs.New().Code(ErrCodeInvalidMaterial).Attr("path", path).Msg("key file is empty")
	}

	return secret.New(bytes.Clone(data)), nil
}
