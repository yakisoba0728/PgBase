package filestore

import (
	"context"
	"errors"
	"io"
	"sync"
)

type lifecycleReadCloser struct {
	ctx    context.Context
	reader io.ReadCloser
	finish func(error)
	once   sync.Once
}

func newLifecycleReadCloser(ctx context.Context, reader io.ReadCloser, finish func(error)) io.ReadCloser {
	return &lifecycleReadCloser{ctx: ctx, reader: reader, finish: finish}
}

func (r *lifecycleReadCloser) complete(err error) { r.once.Do(func() { r.finish(err) }) }

func (r *lifecycleReadCloser) Read(buffer []byte) (int, error) {
	n, err := r.reader.Read(buffer)
	if err != nil {
		if errors.Is(err, io.EOF) {
			r.complete(nil)
		} else {
			r.complete(errOrContext(r.ctx, err))
		}
	}
	return n, err
}

func (r *lifecycleReadCloser) Close() error {
	closeErr := r.reader.Close()
	outcome := closeErr
	if outcome == nil {
		outcome = r.ctx.Err()
	}
	if outcome == nil {
		outcome = io.ErrUnexpectedEOF
	}
	r.complete(outcome)
	return closeErr
}

func errOrContext(ctx context.Context, err error) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	return err
}
