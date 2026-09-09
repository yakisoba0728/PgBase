package backup

import (
	"context"
	"io"
)

// Repository stores immutable backup archives and publishes their metadata.
// Implementations must make an entry visible to List only after its archive is
// readable.
type Repository interface {
	Publish(context.Context, Entry, io.Reader) error
	Open(context.Context, string) (io.ReadCloser, error)
	List(context.Context) ([]Entry, error)
	Get(context.Context, string) (Entry, error)
	Delete(context.Context, string) error
}
