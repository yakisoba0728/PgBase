package selfupdate

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"time"
)

const journalName = "journal.json"

type journal struct {
	SchemaVersion    int       `json:"schemaVersion"`
	Phase            string    `json:"phase"`
	CreatedAt        time.Time `json:"createdAt"`
	Target           string    `json:"target"`
	Candidate        string    `json:"candidate"`
	Backup           string    `json:"backup"`
	Old              Build     `json:"old"`
	New              Build     `json:"new"`
	OldSHA256        string    `json:"oldSha256"`
	NewSHA256        string    `json:"newSha256"`
	ManifestSHA256   string    `json:"manifestSha256"`
	SignatureSHA256  string    `json:"signatureSha256"`
	TrustFingerprint string    `json:"trustFingerprint"`
}

func loadJournal(stateDir string) (journal, error) {
	path := filepath.Join(stateDir, journalName)
	file, err := openRegularFile(path)
	if os.IsNotExist(err) {
		return journal{}, os.ErrNotExist
	}
	if err != nil {
		return journal{}, ErrRecoveryRequired
	}
	defer func() { _ = file.Close() }()
	decoder := json.NewDecoder(file)
	decoder.DisallowUnknownFields()
	var result journal
	if err := decoder.Decode(&result); err != nil || decoder.Decode(&struct{}{}) != io.EOF || !validJournal(result) {
		return journal{}, ErrRecoveryRequired
	}
	return result, nil
}

func validJournal(value journal) bool {
	if value.SchemaVersion != 1 || value.Phase == "" || filepath.Base(value.Target) != value.Target || filepath.Base(value.Candidate) != value.Candidate || filepath.Base(value.Backup) != value.Backup || !validSHA256(value.OldSHA256) || !validSHA256(value.ManifestSHA256) || !validSHA256(value.SignatureSHA256) || !validSHA256(value.TrustFingerprint) {
		return false
	}
	return value.Phase == "manifest_verified" || validSHA256(value.NewSHA256)
}

func writeJournal(stateDir string, value journal) error {
	encoded, err := json.Marshal(value)
	if err != nil {
		return errors.Join(ErrReplaceFailed, err)
	}
	temporary, err := os.CreateTemp(stateDir, ".journal-")
	if err != nil {
		return ErrReplaceFailed
	}
	temporaryName := temporary.Name()
	defer func() { _ = os.Remove(temporaryName) }()
	if _, err = temporary.Write(encoded); err == nil {
		err = temporary.Sync()
	}
	if closeErr := temporary.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return ErrReplaceFailed
	}
	if err = replaceFile(temporaryName, filepath.Join(stateDir, journalName)); err != nil {
		return ErrReplaceFailed
	}
	return syncDirectory(stateDir)
}

func writeEvidence(stateDir, suffix string, manifest, signature []byte) (string, string, error) {
	for _, item := range []struct {
		name string
		data []byte
	}{{"manifest-" + suffix + ".json", manifest}, {"manifest-" + suffix + ".ed25519", signature}} {
		path := filepath.Join(stateDir, item.name)
		if info, err := os.Lstat(path); err == nil {
			if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
				return "", "", ErrRecoveryRequired
			}
			existing, readErr := readRegularFile(path, ManifestMaxBytes)
			if readErr != nil || !bytes.Equal(existing, item.data) {
				return "", "", ErrRecoveryRequired
			}
			continue
		} else if !os.IsNotExist(err) {
			return "", "", ErrRecoveryRequired
		}
		if err := writeRegular(path, item.data, 0600); err != nil {
			return "", "", ErrReplaceFailed
		}
	}
	return checksumBytes(manifest), checksumBytes(signature), nil
}

func writeRegular(path string, data []byte, mode os.FileMode) error {
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
	if err != nil {
		return err
	}
	if _, err = file.Write(data); err == nil {
		err = file.Sync()
	}
	if closeErr := file.Close(); err == nil {
		err = closeErr
	}
	return err
}

func checksumBytes(data []byte) string {
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:])
}
