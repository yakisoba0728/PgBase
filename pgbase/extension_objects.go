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
	"os"
	"strings"
	"time"

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
	event := &ExtensionObjectEvent{Namespace: namespace, Key: key, Operation: "write", Size: int64(len(data))}
	write := func() error { _, err := o.WriteStream(ctx, namespace, key, bytes.NewReader(data)); return err }
	if o != nil && o.hooks != nil {
		return o.hooks.Run(ctx, &HookEvent{Kind: HookObjectWrite, Data: event}, write)
	}
	return write()
}

func (o *ExtensionObjects) Read(ctx context.Context, namespace, key string) ([]byte, error) {
	r, err := o.Open(ctx, namespace, key)
	if err != nil {
		return nil, err
	}
	defer r.Close()
	return io.ReadAll(r)
}

// Open streams a bounded object after checking its namespace metadata.
func (o *ExtensionObjects) Open(ctx context.Context, namespace, key string) (io.ReadCloser, error) {
	physical, err := extensionObjectKey(namespace, key)
	if err != nil {
		return nil, err
	}
	if o == nil || o.store == nil || o.pool == nil {
		return nil, errors.New("extension object storage unavailable")
	}
	parts := strings.Split(physical, "/")
	var exists bool
	if err := o.pool.QueryRow(ctx, `select exists(select 1 from pgbase_meta.extension_objects where namespace_hash=$1 and key_hash=$2 and object_key=$3)`, parts[1], parts[2], physical).Scan(&exists); err != nil {
		return nil, err
	}
	if !exists {
		return nil, filestore.ErrNotFound
	}
	r, err := o.store.Open(ctx, physical)
	if err != nil {
		return nil, err
	}
	limit := o.maximumBytes
	if limit <= 0 {
		limit = 5 << 20
	}
	return &extensionLimitedReader{ReadCloser: r, ctx: ctx, remaining: limit + 1}, nil
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
	physical, err := extensionObjectKey(namespace, key)
	if err != nil {
		return 0, err
	}
	if o == nil || o.store == nil || o.pool == nil {
		return 0, errors.New("extension object storage unavailable")
	}
	limit := o.maximumBytes
	if limit <= 0 {
		limit = 5 << 20
	}
	temporary, err := os.CreateTemp("", "pgbase-extension-object-*")
	if err != nil {
		return 0, err
	}
	temporaryName := temporary.Name()
	defer func() { _ = temporary.Close(); _ = os.Remove(temporaryName) }()
	written, copyErr := io.Copy(temporary, io.LimitReader(&extensionContextReader{ctx: ctx, reader: data}, limit+1))
	if copyErr != nil {
		return written, copyErr
	}
	if written > limit {
		return written, filestore.ErrTooLarge
	}
	if _, err := temporary.Seek(0, io.SeekStart); err != nil {
		return written, err
	}
	if sized, ok := o.store.(filestore.SizedObjectStore); ok {
		err = sized.PutSized(ctx, physical, temporary, written)
	} else {
		err = o.store.Put(ctx, physical, temporary)
	}
	if err != nil {
		return written, err
	}
	parts := strings.Split(physical, "/")
	_, err = o.pool.Exec(ctx, `insert into pgbase_meta.extension_objects(namespace_hash,key_hash,object_key,size_bytes,logical_namespace,logical_key,updated_at) values($1,$2,$3,$4,$5,$6,CURRENT_TIMESTAMP) on conflict(namespace_hash,key_hash) do update set object_key=excluded.object_key,size_bytes=excluded.size_bytes,logical_namespace=excluded.logical_namespace,logical_key=excluded.logical_key,updated_at=excluded.updated_at`, parts[1], parts[2], physical, written, namespace, key)
	if err != nil {
		_ = o.store.Delete(context.WithoutCancel(ctx), physical)
		return written, err
	}
	return written, nil
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
	rows, err := o.pool.Query(ctx, `select logical_key,size_bytes,updated_at from pgbase_meta.extension_objects where logical_namespace=$1 and logical_key like $2 and logical_key>$3 order by logical_key limit $4`, namespace, prefix+"%", cursor, limit+1)
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
	defer r.Close()
	return o.WriteStream(ctx, namespace, destination, r)
}

func (o *ExtensionObjects) Delete(ctx context.Context, namespace, key string) error {
	physical, err := extensionObjectKey(namespace, key)
	if err != nil {
		return err
	}
	if o == nil || o.store == nil {
		return errors.New("extension object storage unavailable")
	}
	event := &ExtensionObjectEvent{Namespace: namespace, Key: key, Operation: "delete"}
	parts := strings.Split(physical, "/")
	remove := func() error {
		_, err := o.pool.Exec(ctx, `delete from pgbase_meta.extension_objects where namespace_hash=$1 and key_hash=$2 and object_key=$3`, parts[1], parts[2], physical)
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
		return host.openObjectHandle(ctx, request.Key)
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
