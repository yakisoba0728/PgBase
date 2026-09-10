package pgbase

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"os"

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
	for id, w := range h.multipartFiles {
		out = errors.Join(out, w.file.Close(), os.Remove(w.path))
		delete(h.multipartFiles, id)
	}
	for id, w := range h.objectWriters {
		out = errors.Join(out, w.file.Close(), os.Remove(w.path))
		delete(h.objectWriters, id)
	}
	return out
}

type extensionObjectWriter struct {
	key, path string
	file      *os.File
	size      int64
}

func (h *extensionHost) beginObjectWrite(raw json.RawMessage) (json.RawMessage, error) {
	var q struct {
		Key string `json:"key"`
	}
	if e := json.Unmarshal(raw, &q); e != nil {
		return nil, e
	}
	if _, e := extensionObjectKey(h.namespace, q.Key); e != nil {
		return nil, e
	}
	f, e := os.CreateTemp("", "pgbase-extension-writer-*")
	if e != nil {
		return nil, e
	}
	id := uuid.New()
	h.objectMu.Lock()
	defer h.objectMu.Unlock()
	if len(h.objectHandles) > 0 || len(h.objectWriters) > 0 {
		_ = f.Close()
		_ = os.Remove(f.Name())
		return nil, errors.New("close active object stream first")
	}
	if h.objectWriters == nil {
		h.objectWriters = map[uuid.UUID]extensionObjectWriter{}
	}
	h.objectWriters[id] = extensionObjectWriter{key: q.Key, path: f.Name(), file: f}
	return json.Marshal(map[string]string{"handle": id.String()})
}
func (h *extensionHost) writeObjectChunk(raw json.RawMessage) (json.RawMessage, error) {
	var q struct{ Handle, Data string }
	if e := json.Unmarshal(raw, &q); e != nil {
		return nil, e
	}
	id, e := uuid.Parse(q.Handle)
	if e != nil {
		return nil, e
	}
	b, e := base64.StdEncoding.DecodeString(q.Data)
	if e != nil {
		return nil, e
	}
	if len(b) > maximumExtensionObjectChunk {
		return nil, errors.New("object chunk too large")
	}
	h.objectMu.Lock()
	defer h.objectMu.Unlock()
	w, ok := h.objectWriters[id]
	if !ok {
		return nil, errors.New("object writer is closed")
	}
	if w.size+int64(len(b)) > maximumExtensionObjectBytes {
		return nil, errors.New("object stream limit reached")
	}
	n, e := w.file.Write(b)
	w.size += int64(n)
	h.objectWriters[id] = w
	if e != nil {
		return nil, e
	}
	return json.Marshal(map[string]any{"written": n, "size": w.size})
}
func (h *extensionHost) commitObjectWrite(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
	var q struct{ Handle string }
	if e := json.Unmarshal(raw, &q); e != nil {
		return nil, e
	}
	id, e := uuid.Parse(q.Handle)
	if e != nil {
		return nil, e
	}
	h.objectMu.Lock()
	w, ok := h.objectWriters[id]
	if ok {
		delete(h.objectWriters, id)
	}
	h.objectMu.Unlock()
	if !ok {
		return nil, errors.New("object writer is closed")
	}
	defer os.Remove(w.path)
	if e := w.file.Close(); e != nil {
		return nil, e
	}
	f, e := os.Open(w.path)
	if e != nil {
		return nil, e
	}
	defer f.Close()
	n, e := h.bridge.objects.WriteStream(ctx, h.namespace, w.key, f)
	if e != nil {
		return nil, e
	}
	return json.Marshal(map[string]any{"written": true, "size": n})
}
func (h *extensionHost) abortObjectWrite(raw json.RawMessage) (json.RawMessage, error) {
	var q struct{ Handle string }
	if e := json.Unmarshal(raw, &q); e != nil {
		return nil, e
	}
	id, e := uuid.Parse(q.Handle)
	if e != nil {
		return nil, e
	}
	h.objectMu.Lock()
	w, ok := h.objectWriters[id]
	if ok {
		delete(h.objectWriters, id)
	}
	h.objectMu.Unlock()
	if !ok {
		return nil, errors.New("object writer is closed")
	}
	return json.RawMessage(`{"aborted":true}`), errors.Join(w.file.Close(), os.Remove(w.path))
}
func (h *extensionHost) fileFromMultipart(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
	var q struct{ Field string }
	if e := json.Unmarshal(raw, &q); e != nil {
		return nil, e
	}
	if h.multipart == nil {
		return nil, errors.New("multipart source unavailable")
	}
	name, ct, size, r, e := h.multipart.OpenMultipartField(ctx, q.Field)
	if e != nil {
		return nil, e
	}
	defer r.Close()
	if size < 0 || size > maximumExtensionObjectBytes {
		return nil, errors.New("multipart object too large")
	}
	f, e := os.CreateTemp("", "pgbase-extension-multipart-*")
	if e != nil {
		return nil, e
	}
	path := f.Name()
	n, e := io.Copy(f, io.LimitReader(r, maximumExtensionObjectBytes+1))
	if e != nil || n > maximumExtensionObjectBytes {
		_ = f.Close()
		_ = os.Remove(path)
		if e != nil {
			return nil, e
		}
		return nil, errors.New("multipart object too large")
	}
	id := uuid.New()
	h.objectMu.Lock()
	if h.multipartFiles == nil {
		h.multipartFiles = map[uuid.UUID]extensionObjectWriter{}
	}
	h.multipartFiles[id] = extensionObjectWriter{path: path, file: f, size: n}
	h.objectMu.Unlock()
	return json.Marshal(map[string]any{"name": name, "contentType": ct, "size": n, "handle": id.String()})
}
func (h *extensionHost) commitMultipart(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
	var q struct{ Handle, Key string }
	if e := json.Unmarshal(raw, &q); e != nil {
		return nil, e
	}
	id, e := uuid.Parse(q.Handle)
	if e != nil {
		return nil, e
	}
	if _, e = extensionObjectKey(h.namespace, q.Key); e != nil {
		return nil, e
	}
	h.objectMu.Lock()
	w, ok := h.multipartFiles[id]
	if ok {
		delete(h.multipartFiles, id)
	}
	h.objectMu.Unlock()
	if !ok {
		return nil, errors.New("multipart handle is closed")
	}
	defer os.Remove(w.path)
	if e := w.file.Close(); e != nil {
		return nil, e
	}
	f, e := os.Open(w.path)
	if e != nil {
		return nil, e
	}
	defer f.Close()
	n, e := h.bridge.objects.WriteStream(ctx, h.namespace, q.Key, f)
	if e != nil {
		return nil, e
	}
	return json.Marshal(map[string]any{"written": true, "size": n})
}
