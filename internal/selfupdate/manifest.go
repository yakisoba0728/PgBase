package selfupdate

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

const (
	ManifestMaxBytes  int64 = 1 << 20
	SignatureMaxBytes int64 = 4 << 10
)

// VerifyManifest authenticates the exact supplied JSON bytes before parsing
// them. Callers retain those same bytes in the journal.
func VerifyManifest(raw, encodedSignature, publicKey []byte) (Manifest, error) {
	if len(raw) == 0 || int64(len(raw)) > ManifestMaxBytes || len(publicKey) != ed25519.PublicKeySize {
		return Manifest{}, ErrInvalidInput
	}
	signatureText := bytes.TrimSuffix(bytes.TrimSpace(encodedSignature), []byte("\n"))
	signature, err := base64.StdEncoding.DecodeString(string(signatureText))
	if err != nil || len(signature) != ed25519.SignatureSize || !ed25519.Verify(ed25519.PublicKey(publicKey), raw, signature) {
		return Manifest{}, ErrUntrustedManifest
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var manifest Manifest
	if err := decoder.Decode(&manifest); err != nil || decoder.Decode(&struct{}{}) != io.EOF {
		return Manifest{}, ErrUntrustedManifest
	}
	if err := validateManifest(manifest); err != nil {
		return Manifest{}, err
	}
	return manifest, nil
}

func validateManifest(manifest Manifest) error {
	if manifest.SchemaVersion != 1 || !validChannel(manifest.Channel) || len(manifest.Releases) == 0 {
		return ErrUntrustedManifest
	}
	releases := make(map[string]struct{}, len(manifest.Releases))
	for _, release := range manifest.Releases {
		if _, ok := releases[release.Version]; ok || !validVersion(release.Version) || !validCommit(release.Commit) || !validVersion(release.MinUpdaterVersion) || release.Schema.Min < 0 || release.Schema.Max < release.Schema.Min || len(release.Artifacts) == 0 {
			return ErrUntrustedManifest
		}
		releases[release.Version] = struct{}{}
		artifacts := make(map[string]struct{}, len(release.Artifacts))
		for _, artifact := range release.Artifacts {
			key := artifact.GOOS + "/" + artifact.GOARCH
			if _, ok := artifacts[key]; ok || !validIdentifier(artifact.GOOS) || !validIdentifier(artifact.GOARCH) || artifact.Size <= 0 || !validSHA256(artifact.SHA256) {
				return ErrUntrustedManifest
			}
			artifacts[key] = struct{}{}
		}
	}
	return nil
}

func validChannel(value string) bool {
	return value == "stable" || value == "beta" || value == "nightly"
}

func validIdentifier(value string) bool {
	if value == "" {
		return false
	}
	for _, c := range value {
		if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '_' || c == '-') {
			return false
		}
	}
	return true
}

func validCommit(value string) bool {
	if len(value) != 40 {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

func validSHA256(value string) bool {
	if len(value) != sha256.Size*2 {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil && strings.ToLower(value) == value
}

// ParseTrustKey accepts exactly a base64-encoded Ed25519 public key. The CLI
// ensures the source was a regular non-symlink file before passing its bytes.
func ParseTrustKey(raw []byte) ([]byte, error) {
	text := bytes.TrimSuffix(raw, []byte("\n"))
	if bytes.ContainsAny(text, "\r\t ") {
		return nil, ErrInvalidInput
	}
	key, err := base64.StdEncoding.DecodeString(string(text))
	if err != nil || len(key) != ed25519.PublicKeySize {
		return nil, ErrInvalidInput
	}
	return key, nil
}

func manifestFingerprint(raw []byte) string {
	digest := sha256.Sum256(raw)
	return hex.EncodeToString(digest[:])
}

func formatManifestError(err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("%w: manifest", ErrUntrustedManifest)
}
