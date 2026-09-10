package pgbase

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"

	"github.com/google/uuid"
)

const maximumExtensionObjectChunk = 64 << 10
const maximumExtensionObjectBytes = 5 << 20

func (h *extensionHost) openObjectHandle(ctx context.Context, key string) (json.RawMessage, error) {
	r, e := h.bridge.objects.Open(ctx, h.namespace, key)
	if e != nil {
		return nil, e
	}
	id := uuid.New()
	h.objectMu.Lock()
	if h.objectHandles == nil {
		h.objectHandles = map[uuid.UUID]io.ReadCloser{}
	}
	if len(h.objectHandles) > 0 {
		h.objectMu.Unlock()
		_ = r.Close()
		return nil, errors.New("close active object stream first")
	}
	h.objectHandles[id] = r
	h.objectMu.Unlock()
	return json.Marshal(map[string]string{"handle": id.String()})
}
func (h *extensionHost) readObjectChunk(raw json.RawMessage) (json.RawMessage, error) {
	var q struct {
		Handle       string `json:"handle"`
		MaximumBytes int    `json:"maximumBytes"`
	}
	if e := json.Unmarshal(raw, &q); e != nil {
		return nil, e
	}
	id, e := uuid.Parse(q.Handle)
	if e != nil {
		return nil, e
	}
	n := q.MaximumBytes
	if n == 0 {
		n = maximumExtensionObjectChunk
	}
	if n < 1 || n > maximumExtensionObjectChunk {
		return nil, errors.New("invalid chunk size")
	}
	h.objectMu.Lock()
	defer h.objectMu.Unlock()
	r := h.objectHandles[id]
	if r == nil {
		return nil, errors.New("object handle is closed")
	}
	if h.objectBytesRead+int64(n) > maximumExtensionObjectBytes {
		n = int(maximumExtensionObjectBytes - h.objectBytesRead)
	}
	if n <= 0 {
		return nil, errors.New("object stream limit reached")
	}
	b := make([]byte, n)
	got, er := r.Read(b)
	h.objectBytesRead += int64(got)
	eof := er == io.EOF
	if er != nil && !eof {
		_ = r.Close()
		delete(h.objectHandles, id)
		return nil, er
	}
	return json.Marshal(map[string]any{"data": base64.StdEncoding.EncodeToString(b[:got]), "eof": eof})
}
func (h *extensionHost) closeObjectHandle(raw json.RawMessage) (json.RawMessage, error) {
	var q struct {
		Handle string `json:"handle"`
	}
	if e := json.Unmarshal(raw, &q); e != nil {
		return nil, e
	}
	id, e := uuid.Parse(q.Handle)
	if e != nil {
		return nil, e
	}
	h.objectMu.Lock()
	defer h.objectMu.Unlock()
	r := h.objectHandles[id]
	if r == nil {
		return nil, errors.New("object handle is closed")
	}
	delete(h.objectHandles, id)
	return json.RawMessage(`{"closed":true}`), r.Close()
}
func (h *extensionHost) closeObjectHandles() error {
	h.objectMu.Lock()
	defer h.objectMu.Unlock()
	var out error
	for id, r := range h.objectHandles {
		out = errors.Join(out, r.Close())
		delete(h.objectHandles, id)
	}
	return out
}
