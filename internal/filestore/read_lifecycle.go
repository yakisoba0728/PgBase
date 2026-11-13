package filestore

import (
	"context"
	"errors"
	"io"
	"sync"
)

type lifecycleReadCloser struct {
	ctx       context.Context
	reader    io.ReadCloser
	finish    func(error)
	once      sync.Once
	closeOnce sync.Once
	closeErr  error
	mu        sync.Mutex
	stop      func() bool
}

func newLifecycleReadCloser(ctx context.Context, reader io.ReadCloser, finish func(error)) io.ReadCloser {
	r := &lifecycleReadCloser{ctx: ctx, reader: reader, finish: finish}
	r.mu.Lock()
	r.stop = context.AfterFunc(ctx, func() { _ = r.Close() })
	r.mu.Unlock()
	return r
}

func (r *lifecycleReadCloser) complete(err error) {
	r.once.Do(func() {
		r.mu.Lock()
		r.stop()
		r.mu.Unlock()
		r.finish(err)
	})
}

func (r *lifecycleReadCloser) Read(buffer []byte) (int, error) {
	n, err := r.reader.Read(buffer)
	if err != nil {
		if errors.Is(err, io.EOF) {
			r.complete(r.ctx.Err())
		} else {
			r.complete(errOrContext(r.ctx, err))
		}
	}
	return n, err
}

func (r *lifecycleReadCloser) Close() error {
	r.closeOnce.Do(func() { r.closeErr = r.reader.Close() })
	closeErr := r.closeErr
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
