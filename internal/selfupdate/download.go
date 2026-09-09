package selfupdate

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	DefaultTimeout           = 2 * time.Minute
	DefaultArtifactMax int64 = 512 << 20
	MinimumArtifactMax int64 = 1 << 20
	MaximumArtifactMax int64 = 2 << 30
	maximumRedirects         = 3
)

// FetchOptions contains only per-invocation inputs. A caller may supply a
// dedicated TLS client for a local test CA; no TLS state is persisted.
type FetchOptions struct {
	Source                string
	SignaturePath         string // local-only detached signature override
	TrustKey              []byte
	Current               Build
	Channel               string
	Requested             string
	AllowDowngrade        bool
	AllowInsecureLoopback bool
	Timeout               time.Duration
	MaxArtifactBytes      int64
	HTTPClient            *http.Client
}

// Fetched is authenticated release metadata plus the source policy required
// when downloading its selected artifact.
type Fetched struct {
	ManifestRaw  []byte
	SignatureRaw []byte
	Candidate    Candidate
	remote       *remoteSource
	local        bool
}

type remoteSource struct {
	origin origin
	client *http.Client
	allow  bool
}

type origin struct {
	scheme, host, port string
}

// Fetch retrieves and verifies a manifest and detached signature, then makes
// a deterministic platform/version choice. It does not download an artifact
// or write an installation.
func Fetch(ctx context.Context, options FetchOptions) (Fetched, error) {
	if len(options.TrustKey) == 0 || options.Source == "" {
		return Fetched{}, ErrInvalidInput
	}
	if options.Timeout == 0 {
		options.Timeout = DefaultTimeout
	}
	if options.Timeout <= 0 {
		return Fetched{}, ErrInvalidInput
	}
	if options.MaxArtifactBytes == 0 {
		options.MaxArtifactBytes = DefaultArtifactMax
	}
	if options.MaxArtifactBytes < MinimumArtifactMax || options.MaxArtifactBytes > MaximumArtifactMax {
		return Fetched{}, ErrInvalidInput
	}
	parsed, err := url.Parse(options.Source)
	if err != nil {
		return Fetched{}, ErrInvalidInput
	}
	if parsed.Scheme == "" {
		return fetchLocal(options, parsed.Path)
	}
	if err := validateRemoteURL(parsed, options.AllowInsecureLoopback); err != nil {
		return Fetched{}, err
	}
	client := cloneClient(options.HTTPClient, options.Timeout, parsed, options.AllowInsecureLoopback)
	requestContext, cancel := context.WithTimeout(ctx, options.Timeout)
	defer cancel()
	manifestRaw, err := readRemote(requestContext, client, parsed, ManifestMaxBytes)
	if err != nil {
		return Fetched{}, err
	}
	signatureURL := *parsed
	signatureURL.Path += ".ed25519"
	signatureRaw, err := readRemote(requestContext, client, &signatureURL, SignatureMaxBytes)
	if err != nil {
		return Fetched{}, err
	}
	manifest, err := VerifyManifest(manifestRaw, signatureRaw, options.TrustKey)
	if err != nil {
		return Fetched{}, err
	}
	candidate, err := Select(manifest, Selection{Current: options.Current, Channel: options.Channel, Requested: options.Requested, AllowDowngrade: options.AllowDowngrade})
	if err != nil {
		return Fetched{}, err
	}
	if candidate.Artifact.Size > options.MaxArtifactBytes {
		return Fetched{}, ErrDownloadLimit
	}
	artifactURL, err := url.Parse(candidate.Artifact.URL)
	if err != nil || !sameOrigin(parsed, artifactURL) || validateRemoteURL(artifactURL, options.AllowInsecureLoopback) != nil {
		return Fetched{}, ErrUntrustedManifest
	}
	return Fetched{ManifestRaw: manifestRaw, SignatureRaw: signatureRaw, Candidate: candidate, remote: &remoteSource{origin: originOf(parsed), client: client, allow: options.AllowInsecureLoopback}}, nil
}

func fetchLocal(options FetchOptions, source string) (Fetched, error) {
	if source == "" || !filepath.IsAbs(source) {
		return Fetched{}, ErrInvalidInput
	}
	manifestRaw, err := readRegularFile(source, ManifestMaxBytes)
	if err != nil {
		return Fetched{}, err
	}
	signaturePath := options.SignaturePath
	if signaturePath == "" {
		signaturePath = source + ".ed25519"
	}
	if !filepath.IsAbs(signaturePath) {
		return Fetched{}, ErrInvalidInput
	}
	signatureRaw, err := readRegularFile(signaturePath, SignatureMaxBytes)
	if err != nil {
		return Fetched{}, err
	}
	manifest, err := VerifyManifest(manifestRaw, signatureRaw, options.TrustKey)
	if err != nil {
		return Fetched{}, err
	}
	candidate, err := Select(manifest, Selection{Current: options.Current, Channel: options.Channel, Requested: options.Requested, AllowDowngrade: options.AllowDowngrade})
	if err != nil {
		return Fetched{}, err
	}
	if candidate.Artifact.Size > options.MaxArtifactBytes {
		return Fetched{}, ErrDownloadLimit
	}
	artifactURL, err := url.Parse(candidate.Artifact.URL)
	if err != nil || artifactURL.Scheme != "file" || artifactURL.Host != "" || !filepath.IsAbs(artifactURL.Path) {
		return Fetched{}, ErrUntrustedManifest
	}
	return Fetched{ManifestRaw: manifestRaw, SignatureRaw: signatureRaw, Candidate: candidate, local: true}, nil
}

// DownloadArtifact writes one verified candidate artifact to destination. The
// destination must be a new regular file owned by the caller's install state.
func (f Fetched) DownloadArtifact(ctx context.Context, destination string) (string, error) {
	if destination == "" || f.Candidate.Artifact.Size <= 0 {
		return "", ErrInvalidInput
	}
	artifactURL, err := url.Parse(f.Candidate.Artifact.URL)
	if err != nil {
		return "", ErrUntrustedManifest
	}
	var reader io.ReadCloser
	if f.local {
		if artifactURL.Scheme != "file" || artifactURL.Host != "" {
			return "", ErrUntrustedManifest
		}
		reader, err = openRegularFile(artifactURL.Path)
	} else {
		if f.remote == nil || !sameOriginValue(f.remote.origin, artifactURL) || validateRemoteURL(artifactURL, f.remote.allow) != nil {
			return "", ErrUntrustedManifest
		}
		reader, err = openRemote(ctx, f.remote.client, artifactURL, f.Candidate.Artifact.Size)
	}
	if err != nil {
		return "", err
	}
	defer reader.Close()
	file, err := os.OpenFile(destination, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return "", fmt.Errorf("%w: stage", ErrReplaceFailed)
	}
	written, digest, copyErr := copyBounded(ctx, file, reader, f.Candidate.Artifact.Size)
	closeErr := file.Close()
	if copyErr != nil || closeErr != nil || written != f.Candidate.Artifact.Size || digest != f.Candidate.Artifact.SHA256 {
		_ = os.Remove(destination)
		if errors.Is(copyErr, ErrDownloadLimit) || written > f.Candidate.Artifact.Size {
			return "", ErrDownloadLimit
		}
		if copyErr != nil && errors.Is(copyErr, context.Canceled) {
			return "", ErrDownloadInterrupted
		}
		if copyErr != nil || closeErr != nil || written != f.Candidate.Artifact.Size || digest != f.Candidate.Artifact.SHA256 {
			return "", ErrDownloadInterrupted
		}
	}
	return digest, nil
}

func cloneClient(source *http.Client, timeout time.Duration, expected *url.URL, allow bool) *http.Client {
	if source == nil {
		source = &http.Client{}
	}
	clone := *source
	clone.Timeout = timeout
	clone.CheckRedirect = func(request *http.Request, via []*http.Request) error {
		if len(via) > maximumRedirects || !sameOrigin(expected, request.URL) || validateRemoteURL(request.URL, allow) != nil {
			return http.ErrUseLastResponse
		}
		return nil
	}
	return &clone
}

func readRemote(ctx context.Context, client *http.Client, value *url.URL, limit int64) ([]byte, error) {
	reader, err := openRemote(ctx, client, value, limit)
	if err != nil {
		return nil, err
	}
	defer reader.Close()
	data, err := io.ReadAll(io.LimitReader(reader, limit+1))
	if int64(len(data)) > limit {
		return nil, ErrDownloadLimit
	}
	if err != nil {
		return nil, ErrDownloadInterrupted
	}
	return data, nil
}

func openRemote(ctx context.Context, client *http.Client, value *url.URL, limit int64) (io.ReadCloser, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, value.String(), nil)
	if err != nil {
		return nil, ErrInvalidInput
	}
	response, err := client.Do(request)
	if err != nil {
		return nil, classifyDownloadError(err)
	}
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices || response.ContentLength > limit {
		_ = response.Body.Close()
		if response.ContentLength > limit {
			return nil, ErrDownloadLimit
		}
		return nil, ErrDownloadInterrupted
	}
	return response.Body, nil
}

func classifyDownloadError(err error) error {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return ErrDownloadInterrupted
	}
	return ErrDownloadInterrupted
}

func readRegularFile(path string, limit int64) ([]byte, error) {
	reader, err := openRegularFile(path)
	if err != nil {
		return nil, ErrInvalidInput
	}
	defer reader.Close()
	data, err := io.ReadAll(io.LimitReader(reader, limit+1))
	if err != nil || int64(len(data)) > limit {
		return nil, ErrInvalidInput
	}
	return data, nil
}

func openRegularFile(path string) (*os.File, error) {
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return nil, os.ErrNotExist
	}
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return nil, ErrInvalidInput
	}
	return os.Open(path)
}

func copyBounded(ctx context.Context, destination io.Writer, source io.Reader, size int64) (int64, string, error) {
	digest := sha256.New()
	reader := io.LimitReader(source, size+1)
	written, err := io.Copy(io.MultiWriter(destination, digest), contextReader{ctx: ctx, reader: reader})
	if written > size {
		return written, "", ErrDownloadLimit
	}
	if err != nil {
		return written, "", err
	}
	return written, hex.EncodeToString(digest.Sum(nil)), nil
}

type contextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (r contextReader) Read(buffer []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.reader.Read(buffer)
}

func validateRemoteURL(value *url.URL, allowLoopback bool) error {
	if value == nil || value.User != nil || value.Fragment != "" || value.RawQuery != "" || value.Opaque != "" || value.Host == "" {
		return ErrInvalidInput
	}
	if value.Scheme == "https" {
		return nil
	}
	if value.Scheme == "http" && allowLoopback && isLoopbackHost(value.Hostname()) {
		return nil
	}
	return ErrInvalidInput
}

func isLoopbackHost(host string) bool {
	parsed := net.ParseIP(host)
	return parsed != nil && parsed.IsLoopback()
}

func originOf(value *url.URL) origin {
	port := value.Port()
	if port == "" {
		if value.Scheme == "https" {
			port = "443"
		} else {
			port = "80"
		}
	}
	return origin{scheme: value.Scheme, host: strings.ToLower(value.Hostname()), port: port}
}
func sameOrigin(left, right *url.URL) bool {
	return left != nil && right != nil && originOf(left) == originOf(right)
}
func sameOriginValue(left origin, right *url.URL) bool { return left == originOf(right) }
