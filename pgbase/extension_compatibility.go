package pgbase

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"github.com/google/uuid"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

const compatibilityMaximumBytes = 1 << 20

type extensionCompatibility struct {
	root                string
	origins             map[string]bool
	commands            map[string]string
	environment         map[string]string
	arguments           []string
	authMailCollections map[uuid.UUID]bool
	exit                func(int) error
	client              *http.Client
}

func newExtensionCompatibility(options PocketBaseCompatibilityOptions) *extensionCompatibility {
	c := &extensionCompatibility{root: options.FilesystemRoot, origins: map[string]bool{}, commands: options.Commands, environment: options.Environment, arguments: options.Arguments, exit: options.ProcessExit, authMailCollections: map[uuid.UUID]bool{}}
	for _, id := range options.AuthMailCollections {
		c.authMailCollections[id] = true
	}
	for _, origin := range options.HTTPAllowedOrigins {
		c.origins[origin] = true
	}
	c.client = &http.Client{Timeout: 30 * time.Second, CheckRedirect: func(request *http.Request, via []*http.Request) error {
		if len(via) >= 5 || !c.allowedURL(request.URL) {
			return errors.New("redirect target is not allowed")
		}
		return nil
	}}
	return c
}

func (c *extensionCompatibility) allowedURL(target *url.URL) bool {
	if target == nil || target.User != nil || (target.Scheme != "http" && target.Scheme != "https") {
		return false
	}
	return c.origins[target.Scheme+"://"+target.Host]
}

func (c *extensionCompatibility) call(ctx context.Context, method string, raw json.RawMessage) (json.RawMessage, error) {
	switch method {
	case "http.send":
		return c.send(ctx, raw)
	case "filesystem.fileFromURL":
		result, err := c.send(ctx, raw)
		if err != nil {
			return nil, err
		}
		var response struct {
			Body string `json:"body"`
		}
		if json.Unmarshal(result, &response) != nil {
			return nil, errors.New("invalid HTTP response")
		}
		return compatibilityInlineFile("", []byte(response.Body))
	case "filesystem.fileFromBytes":
		var request struct {
			Bytes any    `json:"bytes"`
			Name  string `json:"name"`
		}
		if err := decodeExtensionInput(raw, &request); err != nil {
			return nil, err
		}
		data, err := compatibilityBytes(request.Bytes)
		if err != nil {
			return nil, err
		}
		return compatibilityInlineFile(request.Name, data)
	case "filesystem.local":
		var request struct {
			Root string `json:"root"`
		}
		if err := decodeExtensionInput(raw, &request); err != nil {
			return nil, err
		}
		path, err := c.safePath(request.Root, false)
		if err != nil {
			return nil, err
		}
		return json.Marshal(map[string]string{"root": path})
	case "filesystem.fileFromPath":
		var request struct {
			Path string `json:"path"`
		}
		if err := decodeExtensionInput(raw, &request); err != nil {
			return nil, err
		}
		data, err := c.readFile(request.Path)
		if err != nil {
			return nil, err
		}
		return compatibilityInlineFile(filepath.Base(request.Path), data)
	case "os.getwd":
		if c.root == "" {
			return nil, errors.New("filesystem root is not configured")
		}
		return json.Marshal(c.root)
	case "os.args":
		return json.Marshal(c.arguments)
	case "os.getenv":
		var request struct {
			Name string `json:"name"`
		}
		if err := decodeExtensionInput(raw, &request); err != nil {
			return nil, err
		}
		return json.Marshal(c.environment[request.Name])
	case "os.exit":
		var request struct {
			Code int `json:"code"`
		}
		if err := decodeExtensionInput(raw, &request); err != nil {
			return nil, err
		}
		if c.exit == nil {
			return nil, errors.New("process control is not configured")
		}
		if err := c.exit(request.Code); err != nil {
			return nil, err
		}
		return json.Marshal(map[string]bool{"requested": true})
	case "os.stat":
		return c.stat(raw)
	case "os.readFile":
		var request struct {
			Path string `json:"path"`
		}
		if err := decodeExtensionInput(raw, &request); err != nil {
			return nil, err
		}
		data, err := c.readFile(request.Path)
		if err != nil {
			return nil, err
		}
		return json.Marshal(base64.StdEncoding.EncodeToString(data))
	case "os.writeFile":
		return c.writeFile(raw)
	case "os.readDir":
		return c.readDir(raw)
	case "os.glob":
		return c.glob(raw)
	case "os.walk", "os.walkDir":
		return c.walk(raw)
	case "os.truncate", "os.mkdir", "os.mkdirAll", "os.rename", "os.remove", "os.removeAll":
		return c.mutatePath(method, raw)
	case "os.cmd":
		return c.command(ctx, raw)
	default:
		return nil, errors.New("unsupported compatibility operation")
	}
}

func (c *extensionCompatibility) safePath(name string, allowMissing bool) (string, error) {
	if c.root == "" {
		return "", errors.New("filesystem root is not configured")
	}
	if filepath.IsAbs(name) {
		name = strings.TrimPrefix(filepath.Clean(name), filepath.VolumeName(name)+string(filepath.Separator))
	}
	path := filepath.Join(c.root, name)
	rel, err := filepath.Rel(c.root, path)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", errors.New("path escapes filesystem root")
	}
	check := path
	if allowMissing {
		check = filepath.Dir(path)
	}
	real, err := filepath.EvalSymlinks(check)
	if err != nil {
		return "", err
	}
	rel, err = filepath.Rel(c.root, real)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", errors.New("symlink escapes filesystem root")
	}
	return path, nil
}
func (c *extensionCompatibility) glob(raw json.RawMessage) (json.RawMessage, error) {
	var q struct {
		Path string `json:"path"`
	}
	if err := decodeExtensionInput(raw, &q); err != nil {
		return nil, err
	}
	pattern, err := c.safePath(q.Path, true)
	if err != nil {
		return nil, err
	}
	values, err := filepath.Glob(pattern)
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(values))
	for _, value := range values {
		logical, err := filepath.Rel(c.root, value)
		if err != nil {
			return nil, err
		}
		safe, err := c.safePath(logical, false)
		if err != nil {
			return nil, err
		}
		rel, err := filepath.Rel(c.root, safe)
		if err != nil {
			return nil, err
		}
		out = append(out, filepath.ToSlash(rel))
	}
	return json.Marshal(out)
}
func (c *extensionCompatibility) walk(raw json.RawMessage) (json.RawMessage, error) {
	var request struct {
		Path string `json:"path"`
	}
	if err := decodeExtensionInput(raw, &request); err != nil {
		return nil, err
	}
	root, err := c.safePath(request.Path, false)
	if err != nil {
		return nil, err
	}
	values := []string{}
	err = filepath.WalkDir(root, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, e := filepath.Rel(root, path)
		if e != nil {
			return e
		}
		if rel != "." {
			values = append(values, filepath.ToSlash(rel))
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return json.Marshal(values)
}

func (c *extensionCompatibility) readFile(name string) ([]byte, error) {
	path, err := c.safePath(name, false)
	if err != nil {
		return nil, err
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, compatibilityMaximumBytes+1))
	if len(data) > compatibilityMaximumBytes {
		return nil, errors.New("file exceeds compatibility limit")
	}
	return data, err
}
func (c *extensionCompatibility) stat(raw json.RawMessage) (json.RawMessage, error) {
	var request struct {
		Path string `json:"path"`
	}
	if err := decodeExtensionInput(raw, &request); err != nil {
		return nil, err
	}
	path, err := c.safePath(request.Path, false)
	if err != nil {
		return nil, err
	}
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	return json.Marshal(map[string]any{"name": info.Name(), "size": info.Size(), "mode": uint32(info.Mode()), "modTime": info.ModTime(), "isDir": info.IsDir()})
}
func (c *extensionCompatibility) writeFile(raw json.RawMessage) (json.RawMessage, error) {
	var request struct {
		Path string      `json:"path"`
		Data any         `json:"data"`
		Mode os.FileMode `json:"mode"`
	}
	if err := decodeExtensionInput(raw, &request); err != nil {
		return nil, err
	}
	data, err := compatibilityBytes(request.Data)
	if err != nil {
		return nil, err
	}
	if len(data) > compatibilityMaximumBytes {
		return nil, errors.New("file exceeds compatibility limit")
	}
	path, err := c.safePath(request.Path, true)
	if err != nil {
		return nil, err
	}
	if request.Mode == 0 {
		request.Mode = 0600
	}
	if err = os.WriteFile(path, data, request.Mode&0777); err != nil {
		return nil, err
	}
	return json.Marshal(map[string]bool{"written": true})
}
func (c *extensionCompatibility) readDir(raw json.RawMessage) (json.RawMessage, error) {
	var request struct {
		Path string `json:"path"`
	}
	if err := decodeExtensionInput(raw, &request); err != nil {
		return nil, err
	}
	path, err := c.safePath(request.Path, false)
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(path)
	if err != nil {
		return nil, err
	}
	if len(entries) > 1000 {
		return nil, errors.New("directory exceeds compatibility limit")
	}
	result := make([]map[string]any, len(entries))
	for i, e := range entries {
		result[i] = map[string]any{"name": e.Name(), "isDir": e.IsDir(), "type": uint32(e.Type())}
	}
	return json.Marshal(result)
}
func (c *extensionCompatibility) mutatePath(method string, raw json.RawMessage) (json.RawMessage, error) {
	var request struct {
		Path, OldPath, NewPath string
		Size                   int64
		Mode                   os.FileMode
	}
	if err := decodeExtensionInput(raw, &request); err != nil {
		return nil, err
	}
	var err error
	switch method {
	case "os.truncate":
		var p string
		p, err = c.safePath(request.Path, false)
		if err == nil {
			err = os.Truncate(p, request.Size)
		}
	case "os.mkdir", "os.mkdirAll":
		var p string
		p, err = c.safePath(request.Path, true)
		if request.Mode == 0 {
			request.Mode = 0700
		}
		if err == nil && method == "os.mkdir" {
			err = os.Mkdir(p, request.Mode&0777)
		} else if err == nil {
			err = os.MkdirAll(p, request.Mode&0777)
		}
	case "os.rename":
		var a, b string
		a, err = c.safePath(request.OldPath, false)
		if err == nil {
			b, err = c.safePath(request.NewPath, true)
		}
		if err == nil {
			err = os.Rename(a, b)
		}
	case "os.remove", "os.removeAll":
		var p string
		p, err = c.safePath(request.Path, false)
		if err == nil && method == "os.remove" {
			err = os.Remove(p)
		} else if err == nil {
			err = os.RemoveAll(p)
		}
	}
	if err != nil {
		return nil, err
	}
	return json.Marshal(map[string]bool{"ok": true})
}
func (c *extensionCompatibility) command(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
	var request struct {
		Name  string   `json:"name"`
		Args  []string `json:"args"`
		Input string   `json:"input"`
	}
	if err := decodeExtensionInput(raw, &request); err != nil {
		return nil, err
	}
	binary := c.commands[request.Name]
	if binary == "" {
		return nil, errors.New("command is not allowed")
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, binary, request.Args...)
	cmd.Dir = c.root
	cmd.Env = []string{}
	for k, v := range c.environment {
		cmd.Env = append(cmd.Env, k+"="+v)
	}
	cmd.Stdin = strings.NewReader(request.Input)
	var output bytes.Buffer
	cmd.Stdout = &limitedWriter{writer: &output, remaining: compatibilityMaximumBytes}
	cmd.Stderr = &limitedWriter{writer: &output, remaining: compatibilityMaximumBytes}
	err := cmd.Run()
	return json.Marshal(map[string]any{"output": output.String(), "success": err == nil, "error": errorString(err)})
}

type limitedWriter struct {
	writer    io.Writer
	remaining int64
}

func (w *limitedWriter) Write(p []byte) (int, error) {
	if int64(len(p)) > w.remaining {
		return 0, errors.New("process output exceeds compatibility limit")
	}
	n, err := w.writer.Write(p)
	w.remaining -= int64(n)
	return n, err
}
func errorString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}
func compatibilityBytes(value any) ([]byte, error) {
	switch v := value.(type) {
	case string:
		if data, err := base64.StdEncoding.DecodeString(v); err == nil {
			return data, nil
		}
		return []byte(v), nil
	case []any:
		data := make([]byte, len(v))
		for i, n := range v {
			number, ok := n.(float64)
			if !ok || number < 0 || number > 255 {
				return nil, errors.New("invalid byte array")
			}
			data[i] = byte(number)
		}
		return data, nil
	default:
		return nil, errors.New("invalid bytes")
	}
}

func (c *extensionCompatibility) send(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
	var request struct {
		URL, Method, Body string
		Headers           map[string]string
		Timeout           int
	}
	if err := decodeExtensionInput(raw, &request); err != nil {
		return nil, err
	}
	target, err := url.Parse(request.URL)
	if err != nil || !c.allowedURL(target) {
		return nil, errors.New("HTTP origin is not allowed")
	}
	method := request.Method
	if method == "" {
		method = http.MethodGet
	}
	req, err := http.NewRequestWithContext(ctx, method, request.URL, strings.NewReader(request.Body))
	if err != nil {
		return nil, err
	}
	for k, v := range request.Headers {
		req.Header.Set(k, v)
	}
	response, err := c.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, compatibilityMaximumBytes+1))
	if err != nil {
		return nil, err
	}
	if len(body) > compatibilityMaximumBytes {
		return nil, errors.New("HTTP response exceeds compatibility limit")
	}
	headers := map[string]string{}
	for k, v := range response.Header {
		headers[k] = strings.Join(v, ", ")
	}
	return json.Marshal(map[string]any{"statusCode": response.StatusCode, "headers": headers, "body": string(body)})
}

// compatibilityInlineFile is the only inline descriptor accepted by the
// filesystem write bridge. It is intentionally small; larger data uses the
// opaque streaming writer handles.
func compatibilityInlineFile(name string, data []byte) (json.RawMessage, error) {
	return json.Marshal(map[string]any{"kind": "inline", "name": name, "data": base64.StdEncoding.EncodeToString(data), "size": len(data)})
}
