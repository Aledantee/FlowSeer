package syslog

import (
	"bytes"
	"errors"
	"io"
	"os"
	"strconv"
	"time"
)

// Framing chooses stream message boundaries independently of payload grammar.
type Framing string

const (
	// Auto accepts octet lengths or LF-delimited payloads starting with '<'.
	Auto Framing = "auto"
	// OctetCounting uses RFC 6587 lengths in bytes, excluding the prefix.
	OctetCounting Framing = "octet"
	// LF terminates on LF. A preceding CR remains part of the payload.
	LF Framing = "lf"
	// CRLF terminates on the two-byte CRLF sequence.
	CRLF Framing = "crlf"
	// NUL terminates on a zero byte.
	NUL Framing = "nul"
)

func validFraming(f Framing) bool {
	return f == Auto || f == OctetCounting || f == LF || f == CRLF || f == NUL
}

func framingDefault(f Framing, transport Transport) Framing {
	if f != "" {
		return f
	}
	if transport == TLS {
		return OctetCounting
	}
	return Auto
}

type streamReader struct {
	reader        io.Reader
	buffer        []byte
	start, end    int
	observed      time.Time
	pending       error
	deadline      func(time.Time) error
	idle, frame   time.Duration
	frameStarted  bool
	frameDeadline time.Time
}

func (r *streamReader) next() (byte, time.Time, error) {
	if r.start == r.end {
		if r.pending != nil {
			return 0, time.Time{}, r.pending
		}
		n, err := r.reader.Read(r.buffer)
		r.observed = time.Now()
		r.pending = err
		r.start = 0
		r.end = n
		if n == 0 {
			if err == nil {
				err = io.ErrNoProgress
			}
			return 0, time.Time{}, err
		}
	}
	b := r.buffer[r.start]
	r.start++
	if !r.frameStarted {
		r.frameStarted = true
		if r.deadline != nil {
			r.frameDeadline = time.Now().Add(r.frame)
			if err := r.deadline(r.frameDeadline); err != nil {
				return 0, time.Time{}, err
			}
		}
	}
	return b, r.observed, nil
}

// beginFrame waits in fixed scratch so idle peers do not reserve payload storage.
func (r *streamReader) beginFrame() error {
	r.frameStarted = false
	if r.deadline != nil {
		if err := r.deadline(time.Now().Add(r.idle)); err != nil {
			return err
		}
	}
	if _, _, err := r.next(); err != nil {
		return err
	}
	r.start--
	return nil
}

func readFrame(r *streamReader, mode Framing, storage []byte) (payload []byte, observed time.Time, err error) {
	if !r.frameStarted {
		if err := r.beginFrame(); err != nil {
			return nil, time.Time{}, err
		}
	}
	defer func() { r.frameStarted = false }()
	if !r.frameDeadline.IsZero() && !time.Now().Before(r.frameDeadline) {
		return nil, time.Time{}, os.ErrDeadlineExceeded
	}
	b, at, err := r.next()
	if err != nil {
		return nil, at, err
	}
	defer func() {
		if errors.Is(err, io.EOF) {
			err = io.ErrUnexpectedEOF
		}
	}()
	if mode == Auto {
		switch {
		case b == '<':
			mode = LF
		case b >= '0' && b <= '9':
			mode = OctetCounting
		default:
			return nil, at, ErrFraming
		}
	}
	if mode == OctetCounting {
		if b < '1' || b > '9' {
			return nil, at, ErrFraming
		}
		size, count := int(b-'0'), 1
		for {
			b, at, err = r.next()
			if err != nil {
				return nil, at, err
			}
			if b == ' ' {
				break
			}
			if b < '0' || b > '9' || count >= 10 {
				return nil, at, ErrFraming
			}
			digit := int(b - '0')
			if size > (len(storage)-digit)/10 {
				return nil, at, ErrLimit
			}
			size = size*10 + digit
			count++
		}
		if size > len(storage) {
			return nil, at, ErrLimit
		}
		for i := range size {
			storage[i], at, err = r.next()
			if err != nil {
				return nil, at, err
			}
		}
		return storage[:size], at, nil
	}
	if mode != LF && mode != CRLF && mode != NUL {
		return nil, at, ErrFraming
	}
	n := 0
	pendingCR := false
	for {
		if mode == CRLF {
			if pendingCR {
				if b == '\n' {
					return storage[:n], at, nil
				}
				if n == len(storage) {
					return nil, at, ErrLimit
				}
				storage[n] = '\r'
				n++
				pendingCR = false
			}
			if b == '\r' {
				pendingCR = true
			} else {
				if n == len(storage) {
					return nil, at, ErrLimit
				}
				storage[n] = b
				n++
			}
		} else {
			if mode == LF && b == '\n' || mode == NUL && b == 0 {
				return storage[:n], at, nil
			}
			if n == len(storage) {
				return nil, at, ErrLimit
			}
			storage[n] = b
			n++
		}
		b, at, err = r.next()
		if err != nil {
			return nil, at, err
		}
	}
}

func frameOutput(payload []byte, mode Framing) ([]byte, error) {
	switch mode {
	case Auto, OctetCounting:
		if len(payload) == 0 {
			return nil, ErrFraming
		}
		prefix := strconv.Itoa(len(payload)) + " "
		framed := make([]byte, 0, len(prefix)+len(payload))
		framed = append(framed, prefix...)
		return append(framed, payload...), nil
	case LF, CRLF, NUL:
		delimiter := []byte{'\n'}
		if mode == CRLF {
			delimiter = []byte{'\r', '\n'}
		}
		if mode == NUL {
			delimiter = []byte{0}
		}
		if bytes.Contains(payload, delimiter) {
			return nil, ErrUnrepresentable
		}
		framed := make([]byte, 0, len(payload)+len(delimiter))
		framed = append(framed, payload...)
		return append(framed, delimiter...), nil
	default:
		return nil, ErrFraming
	}
}
