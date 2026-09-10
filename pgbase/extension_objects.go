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
	"strings"

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

func extensionObjectKey(namespace, key string) (string, error) {
	namespace, key = strings.TrimSpace(namespace), strings.TrimSpace(key)
	if namespace == "" || key == "" || strings.ContainsAny(namespace+key, "\\\x00") {
		return "", errors.New("invalid extension object key")
	}
	if len(namespace) > 4096 || len(key) > 4096 {
		return "", errors.New("invalid extension object key")
	}
	namespaceDigest, keyDigest := sha256.Sum256([]byte(namespace)), sha256.Sum256([]byte(key))
	return extensionObjectPrefix + "/" + hex.EncodeToString(namespaceDigest[:]) + "/" + hex.EncodeToString(keyDigest[:]), nil
}
func (o *ExtensionObjects) Write(ctx context.Context, namespace, key string, data []byte) error {
	physical, err := extensionObjectKey(namespace, key)
	if err != nil {
		return err
	}
	if o == nil || o.store == nil {
		return errors.New("extension object storage unavailable")
	}
	if o.maximumBytes > 0 && int64(len(data)) > o.maximumBytes {
		return filestore.ErrTooLarge
	}
	event := &ExtensionObjectEvent{Namespace: namespace, Key: key, Operation: "write", Size: int64(len(data))}
	write := func() error {
		if err := o.store.Put(ctx, physical, bytes.NewReader(data)); err != nil {
			return err
		}
		parts := strings.Split(physical, "/")
		_, err := o.pool.Exec(ctx, `insert into pgbase_meta.extension_objects(namespace_hash,key_hash,object_key,size_bytes,updated_at) values($1,$2,$3,$4,CURRENT_TIMESTAMP) on conflict(namespace_hash,key_hash) do update set object_key=excluded.object_key,size_bytes=excluded.size_bytes,updated_at=excluded.updated_at`, parts[1], parts[2], physical, len(data))
		if err != nil {
			_ = o.store.Delete(context.WithoutCancel(ctx), physical)
		}
		return err
	}
	if o.hooks != nil {
		return o.hooks.Run(ctx, &HookEvent{Kind: HookObjectWrite, Data: event}, write)
	}
	return write()
}
func (o *ExtensionObjects) Read(ctx context.Context, namespace, key string) ([]byte, error) {
	physical, err := extensionObjectKey(namespace, key)
	if err != nil {
		return nil, err
	}
	if o == nil || o.store == nil {
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
	defer r.Close()
	limit := o.maximumBytes
	if limit <= 0 {
		limit = 5 << 20
	}
	data, err := io.ReadAll(io.LimitReader(r, limit+1))
	if err == nil && int64(len(data)) > limit {
		return nil, filestore.ErrTooLarge
	}
	return data, err
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
	var request struct{ Key, Data string }
	if err := decodeExtensionInput(raw, &request); err != nil {
		return nil, err
	}
	switch method {
	case "objects.write":
		data, err := base64.StdEncoding.DecodeString(request.Data)
		if err != nil {
			return nil, extensionError("invalid_object_request", err)
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
