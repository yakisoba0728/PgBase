package filestore

import (
	"context"
	"errors"
	"io"
)

var ErrRangeNotSatisfiable = errors.New("range not satisfiable")

// ByteRange describes one RFC 9110 byte range. A negative Start denotes a
// suffix of -Start bytes; End == -1 denotes an open-ended range.
type ByteRange struct{ Start, End int64 }

type RangeRead struct {
	Body             io.ReadCloser
	Start, End, Size int64
	Prefix           []byte
}

type ObjectRangeStore interface {
	OpenRange(context.Context, string, ByteRange) (RangeRead, error)
}

func ResolveByteRange(value ByteRange, size int64) (start, end int64, err error) {
	if size < 0 || value.Start == 0 && value.End < -1 {
		return 0, 0, ErrRangeNotSatisfiable
	}
	if value.Start < 0 {
		suffix := -value.Start
		if suffix <= 0 || size == 0 {
			return 0, 0, ErrRangeNotSatisfiable
		}
		if suffix > size {
			suffix = size
		}
		return size - suffix, size - 1, nil
	}
	if value.Start >= size {
		return 0, 0, ErrRangeNotSatisfiable
	}
	end = value.End
	if end < 0 || end >= size {
		end = size - 1
	}
	if end < value.Start {
		return 0, 0, ErrRangeNotSatisfiable
	}
	return value.Start, end, nil
}
