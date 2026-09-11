package pgbase

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/yakisoba0728/PgBase/internal/database"
	"github.com/yakisoba0728/PgBase/internal/filestore"
)

const extensionObjectPrefix = "extensions"

type ExtensionObjects struct {
	store        filestore.ObjectStore
	pool         *database.Pool
	maximumBytes int64
	hooks        *HookRegistry
}
type ExtensionObjectEvent struct {
	Namespace, Key, Operation string
	Size                      int64
}

type extensionObjectTransactionKey struct{}
type extensionObjectTransactionState struct {
	mu       sync.Mutex
	cond     *sync.Cond
	tx       pgx.Tx
	objects  *ExtensionObjects
	uploaded map[string]struct{}
	active   int
	closing  bool
}

func newExtensionObjectTransactionState(tx pgx.Tx, objects *ExtensionObjects) *extensionObjectTransactionState {
	s := &extensionObjectTransactionState{tx: tx, objects: objects, uploaded: map[string]struct{}{}}
	s.cond = sync.NewCond(&s.mu)
	return s
}

func (s *extensionObjectTransactionState) acquire() bool {
	if s == nil {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closing {
		return false
	}
	s.active++
	return true
}

func (s *extensionObjectTransactionState) release() {
	s.mu.Lock()
	s.active--
	if s.active == 0 {
		s.cond.Broadcast()
	}
	s.mu.Unlock()
}

func (s *extensionObjectTransactionState) register(key string) bool {
	if s == nil {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closing || s.uploaded == nil {
		return false
	}
	s.uploaded[key] = struct{}{}
	return true
}

func (s *extensionObjectTransactionState) unregister(key string) {
	if s == nil {
		return
	}
	s.mu.Lock()
	delete(s.uploaded, key)
	s.mu.Unlock()
}

func (s *extensionObjectTransactionState) finish(ctx context.Context, rolledBack bool) error {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	s.closing = true
	for s.active > 0 {
		s.cond.Wait()
	}
	keys := make([]string, 0, len(s.uploaded))
	for key := range s.uploaded {
		keys = append(keys, key)
	}
	s.uploaded = nil
	s.mu.Unlock()
	if !rolledBack || s.objects == nil || s.objects.store == nil {
		return nil
	}
	cleanupCtx, cancel := database.BoundedCleanupContext(ctx)
	defer cancel()
	var result error
	for _, key := range keys {
		result = errors.Join(result, s.objects.store.Delete(cleanupCtx, key))
	}
	return result
}

func (o *ExtensionObjects) querier(ctx context.Context) trustedQuerier {
	if state, ok := ctx.Value(extensionObjectTransactionKey{}).(*extensionObjectTransactionState); ok && state != nil && state.objects == o && state.tx != nil {
		return state.tx
	}
	return o.pool
}

// ExtensionObjectInfo describes a namespace-local object without exposing its
// backend key. Cursor is the last returned logical key.
type ExtensionObjectInfo struct {
	Key       string
	Size      int64
	UpdatedAt time.Time
}
type ExtensionObjectPage struct {
	Objects    []ExtensionObjectInfo
	NextCursor string
}

func (o *ExtensionObjects) Attributes(ctx context.Context, namespace, key string) (ExtensionObjectInfo, error) {
	identity, err := extensionObjectKey(namespace, key)
	if err != nil {
		return ExtensionObjectInfo{}, err
	}
	if o == nil || o.pool == nil {
		return ExtensionObjectInfo{}, errors.New("extension object storage unavailable")
	}
	parts := strings.Split(identity, "/")
	var info ExtensionObjectInfo
	err = o.querier(ctx).QueryRow(ctx, `select logical_key,size_bytes,updated_at from pgbase_meta.extension_objects where namespace_hash=$1 and key_hash=$2`, parts[1], parts[2]).Scan(&info.Key, &info.Size, &info.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return ExtensionObjectInfo{}, filestore.ErrNotFound
	}
	return info, err
}

func extensionObjectKey(namespace, key string) (string, error) {
	namespace, key = strings.TrimSpace(namespace), strings.TrimSpace(key)
	if namespace == "" || key == "" || strings.ContainsAny(namespace+key, "\\\x00") || invalidExtensionPath(namespace) || invalidExtensionPath(key) {
		return "", errors.New("invalid extension object key")
	}
	if len(namespace) > 4096 || len(key) > 4096 {
		return "", errors.New("invalid extension object key")
	}
	namespaceDigest, keyDigest := sha256.Sum256([]byte(namespace)), sha256.Sum256([]byte(key))
	return extensionObjectPrefix + "/" + hex.EncodeToString(namespaceDigest[:]) + "/" + hex.EncodeToString(keyDigest[:]), nil
}

func invalidExtensionPath(value string) bool {
	for _, segment := range strings.Split(value, "/") {
		if segment == "" || segment == "." || segment == ".." {
			return true
		}
	}
	return false
}
func (o *ExtensionObjects) Write(ctx context.Context, namespace, key string, data []byte) error {
	_, err := o.WriteStream(ctx, namespace, key, bytes.NewReader(data))
	return err
}

func (o *ExtensionObjects) Read(ctx context.Context, namespace, key string) ([]byte, error) {
	r, err := o.Open(ctx, namespace, key)
	if err != nil {
		return nil, err
	}
	defer func() { _ = r.Close() }()
	return io.ReadAll(r)
}

// Open streams a bounded object after checking its namespace metadata.
func (o *ExtensionObjects) Open(ctx context.Context, namespace, key string) (io.ReadCloser, error) {
	return o.open(ctx, ctx, namespace, key)
}

func (o *ExtensionObjects) open(metadataCtx, readerCtx context.Context, namespace, key string) (io.ReadCloser, error) {
	physical, err := o.physicalKey(metadataCtx, namespace, key)
	if err != nil {
		return nil, err
	}
	r, err := o.store.Open(readerCtx, physical)
	if err != nil {
		return nil, err
	}
	limit := o.maximumBytes
	if limit <= 0 {
		limit = 5 << 20
	}
	return &extensionLimitedReader{ReadCloser: r, ctx: readerCtx, remaining: limit + 1}, nil
}

func (o *ExtensionObjects) physicalKey(ctx context.Context, namespace, key string) (string, error) {
	identity, err := extensionObjectKey(namespace, key)
	if err != nil {
		return "", err
	}
	if o == nil || o.store == nil || o.pool == nil {
		return "", errors.New("extension object storage unavailable")
	}
	parts := strings.Split(identity, "/")
	var physical string
	if err := o.querier(ctx).QueryRow(ctx, `select object_key from pgbase_meta.extension_objects where namespace_hash=$1 and key_hash=$2`, parts[1], parts[2]).Scan(&physical); errors.Is(err, pgx.ErrNoRows) {
		return "", filestore.ErrNotFound
	} else if err != nil {
		return "", err
	}
	return physical, nil
}

type extensionLimitedReader struct {
	io.ReadCloser
	ctx       context.Context
	remaining int64
}

func (r *extensionLimitedReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	if r.remaining <= 0 {
		return 0, filestore.ErrTooLarge
	}
	if int64(len(p)) > r.remaining {
		p = p[:r.remaining]
	}
	n, err := r.ReadCloser.Read(p)
	r.remaining -= int64(n)
	return n, err
}

// WriteStream consumes data incrementally; it never buffers the object in memory.
func (o *ExtensionObjects) WriteStream(ctx context.Context, namespace, key string, data io.Reader) (int64, error) {
	identity, err := extensionObjectKey(namespace, key)
	if err != nil {
		return 0, err
	}
	if o == nil || o.store == nil || o.pool == nil {
		return 0, errors.New("extension object storage unavailable")
	}
	temporary, written, err := o.spool(ctx, data)
	if temporary != nil {
		defer func() { _ = temporary.Close(); _ = os.Remove(temporary.Name()) }()
	}
	if err != nil {
		return written, err
	}
	event := &ExtensionObjectEvent{Namespace: namespace, Key: key, Operation: "write", Size: written}
	publish := func() error { return o.publishStream(ctx, identity, namespace, key, temporary, written) }
	if o != nil && o.hooks != nil {
		err := o.hooks.Run(ctx, &HookEvent{Kind: HookObjectWrite, Data: event}, publish)
		return written, err
	}
	err = publish()
	return written, err
}

func (o *ExtensionObjects) spool(ctx context.Context, data io.Reader) (*os.File, int64, error) {
	limit := o.maximumBytes
	if limit <= 0 {
		limit = 5 << 20
	}
	temporary, err := os.CreateTemp("", "pgbase-extension-object-*")
	if err != nil {
		return nil, 0, err
	}
	written, copyErr := io.Copy(temporary, io.LimitReader(&extensionContextReader{ctx: ctx, reader: data}, limit+1))
	if copyErr != nil {
		return temporary, written, copyErr
	}
	if written > limit {
		return temporary, written, filestore.ErrTooLarge
	}
	if _, err := temporary.Seek(0, io.SeekStart); err != nil {
		return temporary, written, err
	}
	return temporary, written, nil
}

func (o *ExtensionObjects) publishStream(ctx context.Context, identity, namespace, key string, temporary *os.File, written int64) error {
	physical := identity + "/" + uuid.NewString()
	var err error
	if sized, ok := o.store.(filestore.SizedObjectStore); ok {
		err = sized.PutSized(ctx, physical, temporary, written)
	} else {
		err = o.store.Put(ctx, physical, temporary)
	}
	if err != nil {
		return err
	}
	parts := strings.Split(identity, "/")
	publish := func(tx trustedQuerier) error {
		if _, err := tx.Exec(ctx, `select pg_advisory_xact_lock(hashtextextended($1,0))`, parts[1]+"/"+parts[2]); err != nil {
			return err
		}
		var previous string
		err := tx.QueryRow(ctx, `select object_key from pgbase_meta.extension_objects where namespace_hash=$1 and key_hash=$2`, parts[1], parts[2]).Scan(&previous)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		if _, err = tx.Exec(ctx, `insert into pgbase_meta.extension_objects(namespace_hash,key_hash,object_key,size_bytes,logical_namespace,logical_key,updated_at) values($1,$2,$3,$4,$5,$6,CURRENT_TIMESTAMP) on conflict(namespace_hash,key_hash) do update set object_key=excluded.object_key,size_bytes=excluded.size_bytes,logical_namespace=excluded.logical_namespace,logical_key=excluded.logical_key,updated_at=excluded.updated_at`, parts[1], parts[2], physical, written, namespace, key); err != nil {
			return err
		}
		if previous != "" && previous != physical {
			_, err = tx.Exec(ctx, `insert into pgbase_meta.file_object_deletions(object_key,queued_at) values($1,CURRENT_TIMESTAMP) on conflict(object_key) do nothing`, previous)
		}
		return err
	}
	state, external := ctx.Value(extensionObjectTransactionKey{}).(*extensionObjectTransactionState)
	if external && state != nil && state.tx != nil && state.objects == o {
		if !state.register(physical) {
			_ = o.store.Delete(context.WithoutCancel(ctx), physical)
			return errors.New("extension object transaction is closing")
		}
		err = publish(state.tx)
	} else {
		err = o.pool.InTx(ctx, pgx.TxOptions{}, func(tx pgx.Tx) error { return publish(tx) })
	}
	if err != nil {
		_ = o.store.Delete(context.WithoutCancel(ctx), physical)
		if external {
			state.unregister(physical)
		}
		return err
	}
	return nil
}

type extensionContextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (r *extensionContextReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.reader.Read(p)
}

func (o *ExtensionObjects) List(ctx context.Context, namespace, prefix, cursor string, limit int) (ExtensionObjectPage, error) {
	if _, err := extensionObjectKey(namespace, "list"); err != nil {
		return ExtensionObjectPage{}, err
	}
	if prefix != "" && invalidExtensionPath(strings.TrimSuffix(prefix, "/")) {
		return ExtensionObjectPage{}, errors.New("invalid extension object key")
	}
	if limit < 1 || limit > 1000 {
		return ExtensionObjectPage{}, errors.New("invalid object page limit")
	}
	rows, err := o.querier(ctx).Query(ctx, `select logical_key,size_bytes,updated_at from pgbase_meta.extension_objects where logical_namespace=$1 and left(logical_key,length($2))=$2 and logical_key>$3 order by logical_key limit $4`, namespace, prefix, cursor, limit+1)
	if err != nil {
		return ExtensionObjectPage{}, err
	}
	defer rows.Close()
	out := ExtensionObjectPage{Objects: make([]ExtensionObjectInfo, 0, limit+1)}
	for rows.Next() {
		var v ExtensionObjectInfo
		if err := rows.Scan(&v.Key, &v.Size, &v.UpdatedAt); err != nil {
			return ExtensionObjectPage{}, err
		}
		out.Objects = append(out.Objects, v)
	}
	if err := rows.Err(); err != nil {
		return ExtensionObjectPage{}, err
	}
	if len(out.Objects) > limit {
		out.Objects = out.Objects[:limit]
		out.NextCursor = out.Objects[len(out.Objects)-1].Key
	}
	return out, nil
}
func (o *ExtensionObjects) Copy(ctx context.Context, namespace, source, destination string) (int64, error) {
	r, err := o.Open(ctx, namespace, source)
	if err != nil {
		return 0, err
	}
	defer func() { _ = r.Close() }()
	return o.WriteStream(ctx, namespace, destination, r)
}

func (o *ExtensionObjects) Delete(ctx context.Context, namespace, key string) error {
	identity, err := extensionObjectKey(namespace, key)
	if err != nil {
		return err
	}
	if o == nil || o.store == nil {
		return errors.New("extension object storage unavailable")
	}
	event := &ExtensionObjectEvent{Namespace: namespace, Key: key, Operation: "delete"}
	parts := strings.Split(identity, "/")
	remove := func() error {
		_, err := o.querier(ctx).Exec(ctx, `delete from pgbase_meta.extension_objects where namespace_hash=$1 and key_hash=$2`, parts[1], parts[2])
		return err
	}
	if o.hooks != nil {
		return o.hooks.Run(ctx, &HookEvent{Kind: HookObjectDelete, Data: event}, remove)
	}
	return remove()
}
func (host *extensionHost) callObjects(ctx context.Context, method string, raw json.RawMessage) (json.RawMessage, error) {
	if host.bridge.objects == nil || host.namespace == "" {
		return nil, extensionError("objects_unavailable", errors.New("extension object storage unavailable"))
	}
	switch method {
	case "objects.serve":
		var request struct{ Key, Name string }
		if err := decodeExtensionInput(raw, &request); err != nil {
			return nil, err
		}
		if request.Name == "" || len(request.Name) > 255 {
			return nil, extensionError("invalid_object_request", errors.New("invalid serve name"))
		}
		if _, err := host.bridge.objects.Attributes(ctx, host.namespace, request.Key); err != nil {
			return nil, extensionError("object_serve_failed", err)
		}
		host.objectMu.Lock()
		defer host.objectMu.Unlock()
		if host.servedObject != nil {
			return nil, extensionError("object_serve_failed", errors.New("response already selected"))
		}
		host.servedObject = &extensionServedObject{key: request.Key, name: request.Name}
		return json.RawMessage(`{"served":true}`), nil
	case "objects.createThumb":
		var request struct{ OriginalKey, ThumbKey, ThumbSize string }
		if err := decodeExtensionInput(raw, &request); err != nil {
			return nil, err
		}
		if host.bridge.files == nil {
			return nil, extensionError("thumbnail_unavailable", errors.New("file thumbnail service unavailable"))
		}
		reader, err := host.bridge.objects.Open(ctx, host.namespace, request.OriginalKey)
		if err != nil {
			return nil, extensionError("thumbnail_failed", err)
		}
		probe := make([]byte, 512)
		n, readErr := io.ReadFull(reader, probe)
		_ = reader.Close()
		if readErr != nil && !errors.Is(readErr, io.ErrUnexpectedEOF) && !errors.Is(readErr, io.EOF) {
			return nil, extensionError("thumbnail_failed", readErr)
		}
		contentType := strings.TrimSpace(strings.Split(http.DetectContentType(probe[:n]), ";")[0])
		physical, err := host.bridge.objects.physicalKey(ctx, host.namespace, request.OriginalKey)
		if err != nil {
			return nil, extensionError("thumbnail_failed", err)
		}
		data, err := host.bridge.files.RenderThumbnail(ctx, physical, contentType, request.ThumbSize)
		if err != nil {
			return nil, extensionError("thumbnail_failed", err)
		}
		if err := host.bridge.objects.Write(ctx, host.namespace, request.ThumbKey, data); err != nil {
			return nil, extensionError("thumbnail_failed", err)
		}
		return json.Marshal(map[string]any{"created": true, "size": len(data)})
	case "objects.attributes", "objects.exists":
		var request struct {
			Key string `json:"key"`
		}
		if err := decodeExtensionInput(raw, &request); err != nil {
			return nil, err
		}
		info, err := host.bridge.objects.Attributes(ctx, host.namespace, request.Key)
		if method == "objects.exists" {
			if errors.Is(err, filestore.ErrNotFound) {
				return json.RawMessage(`false`), nil
			}
			if err != nil {
				return nil, err
			}
			return json.RawMessage(`true`), nil
		}
		if err != nil {
			return nil, err
		}
		return json.Marshal(map[string]any{"key": info.Key, "size": info.Size, "modTime": info.UpdatedAt, "contentType": "application/octet-stream"})
	case "objects.deletePrefix":
		var request struct {
			Prefix string `json:"prefix"`
		}
		if err := decodeExtensionInput(raw, &request); err != nil {
			return nil, err
		}
		keys := []string{}
		cursor := ""
		for {
			page, err := host.bridge.objects.List(ctx, host.namespace, request.Prefix, cursor, 100)
			if err != nil {
				return nil, err
			}
			for _, item := range page.Objects {
				keys = append(keys, item.Key)
			}
			if page.NextCursor == "" {
				break
			}
			cursor = page.NextCursor
		}
		for _, key := range keys {
			if err := host.bridge.objects.Delete(ctx, host.namespace, key); err != nil {
				return nil, err
			}
		}
		return json.Marshal(map[string]any{"deleted": len(keys)})
	case "objects.readChunk":
		return host.readObjectChunk(raw)
	case "objects.close":
		return host.closeObjectHandle(raw)
	case "objects.beginWrite":
		return host.beginObjectWrite(raw)
	case "objects.writeChunk":
		return host.writeObjectChunk(raw)
	case "objects.commit":
		return host.commitObjectWrite(ctx, raw)
	case "objects.abort":
		return host.abortObjectWrite(raw)
	case "objects.fileFromMultipart":
		return host.fileFromMultipart(ctx, raw)
	case "objects.commitMultipart":
		return host.commitMultipart(ctx, raw)
	case "objects.list":
		var list struct {
			Prefix, Cursor string
			Limit          int
		}
		if err := decodeExtensionInput(raw, &list); err != nil {
			return nil, err
		}
		if list.Limit == 0 {
			list.Limit = 100
		}
		page, err := host.bridge.objects.List(ctx, host.namespace, list.Prefix, list.Cursor, list.Limit)
		if err != nil {
			return nil, extensionError("object_list_failed", err)
		}
		return json.Marshal(page)
	case "objects.copy":
		var copyRequest struct{ SourceKey, DestinationKey string }
		if err := decodeExtensionInput(raw, &copyRequest); err != nil {
			return nil, err
		}
		size, err := host.bridge.objects.Copy(ctx, host.namespace, copyRequest.SourceKey, copyRequest.DestinationKey)
		if err != nil {
			return nil, extensionError("object_copy_failed", err)
		}
		return json.Marshal(map[string]any{"copied": true, "size": size})
	}
	var request struct {
		Key        string `json:"key"`
		Data       string `json:"data"`
		Descriptor *struct {
			Kind string `json:"kind"`
			Name string `json:"name"`
			Size int64  `json:"size"`
		} `json:"descriptor"`
	}
	if err := decodeExtensionInput(raw, &request); err != nil {
		return nil, err
	}
	switch method {
	case "objects.open":
		return host.openObjectHandle(ctx, ctx, request.Key)
	case "objects.write":
		data, err := base64.StdEncoding.DecodeString(request.Data)
		if err != nil {
			return nil, extensionError("invalid_object_request", err)
		}
		if request.Descriptor != nil && (request.Descriptor.Kind != "inline" || request.Descriptor.Name == "" || request.Descriptor.Size < 0 || request.Descriptor.Size != int64(len(data))) {
			return nil, extensionError("invalid_object_request", errors.New("invalid inline file descriptor"))
		}
		err = host.bridge.objects.Write(ctx, host.namespace, request.Key, data)
		if err != nil {
			return nil, extensionError("object_write_failed", err)
		}
		return json.Marshal(map[string]any{"written": true, "size": len(data)})
	case "objects.read":
		data, err := host.bridge.objects.Read(ctx, host.namespace, request.Key)
		if err != nil {
			return nil, extensionError("object_read_failed", err)
		}
		return json.Marshal(map[string]any{"data": base64.StdEncoding.EncodeToString(data), "size": len(data)})
	case "objects.delete":
		err := host.bridge.objects.Delete(ctx, host.namespace, request.Key)
		if err != nil {
			return nil, extensionError("object_delete_failed", err)
		}
		return json.Marshal(map[string]bool{"deleted": true})

	}
	return nil, extensionError("unsupported_operation", errors.New("unsupported object operation"))
}
