package selfupdate

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/yakisoba0728/PgBase/internal/durablefile"
)

const probeLimit = 64 << 10

// ApplyOptions contains only a task-owned target and verified downloader. It
// never opens a database or discovers a server process.
type ApplyOptions struct {
	Target        string
	StateDir      string
	Fetched       Fetched
	TrustKey      []byte
	Current       Build
	Probe         func(context.Context, string) (Build, error)
	WindowsLaunch func(WindowsHelperRequest) error
	BeforePhase   func(string) error // test-only crash/fault seam
}

// Status is the durable state exposed by `update status --json`.
type Status struct {
	Phase     string `json:"phase"`
	Current   Build  `json:"current"`
	Candidate Build  `json:"candidate,omitempty"`
	Previous  Build  `json:"previous,omitempty"`
	BackupSHA string `json:"backupSha256,omitempty"`
	Recovered bool   `json:"-"`
}

// Apply downloads a signed selected artifact into the target's own directory,
// verifies it, preserves a verified backup, atomically publishes it, probes
// the canonical path, and rolls back on a failed probe.
func Apply(ctx context.Context, options ApplyOptions) (Status, error) {
	if len(options.Fetched.ManifestRaw) == 0 || len(options.Fetched.SignatureRaw) == 0 || len(options.TrustKey) != 32 || options.Fetched.Candidate.Artifact.Size <= 0 {
		return Status{}, ErrInvalidInput
	}
	state, target, lock, err := prepareInstallation(options.Target, options.StateDir)
	if err != nil {
		return Status{}, err
	}
	defer func() { _ = lock.Close() }()
	if err = acquireLock(lock); err != nil {
		return Status{}, err
	}
	defer func() { _ = releaseLock(lock) }()
	executableLock, err := acquireUpdateExecutableLock(target)
	if err != nil {
		return Status{}, err
	}
	defer func() { _ = closeExecutableLock(executableLock) }()
	manifest, err := VerifyManifest(options.Fetched.ManifestRaw, options.Fetched.SignatureRaw, options.TrustKey)
	if err != nil {
		return Status{}, err
	}
	if !candidateInManifest(manifest, options.Fetched.Candidate) {
		return Status{}, ErrUntrustedManifest
	}
	if existing, err := loadJournal(state); err == nil {
		if existing.Target != filepath.Base(target) {
			return Status{}, ErrRecoveryRequired
		}
		status, recoverErr := recoverLocked(ctx, state, target, existing, options.Probe)
		if recoverErr != nil {
			return Status{}, recoverErr
		}
		if status.Phase != "aborted" && status.Phase != "rolled_back" && status.Phase != "committed" {
			return status, nil
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return Status{}, err
	}
	if err = ensureRegular(target); err != nil {
		return Status{}, err
	}
	targetIdentity, err := os.Lstat(target)
	if err != nil || !validInstallationTarget(targetIdentity) {
		return Status{}, ErrInvalidInput
	}
	probe := options.Probe
	if probe == nil {
		probe = ProbeBinary
	}
	oldBuild, err := probe(ctx, target)
	if err != nil || !sameBuild(oldBuild, options.Current) {
		return Status{}, ErrProbeFailed
	}
	targetDigest, targetMode, err := fileDigestAndMode(target)
	if err != nil {
		return Status{}, ErrReplaceFailed
	}
	suffix := targetDigest[:12]
	manifestSHA, signatureSHA, err := writeEvidence(state, suffix, options.Fetched.ManifestRaw, options.Fetched.SignatureRaw)
	if err != nil {
		return Status{}, err
	}
	keyFingerprint := checksumBytes(options.TrustKey)
	candidateName := "." + filepath.Base(target) + ".pgbase-candidate-" + suffix
	candidatePath := filepath.Join(filepath.Dir(target), candidateName)
	if _, err = os.Lstat(candidatePath); !os.IsNotExist(err) {
		return Status{}, ErrRecoveryRequired
	}
	entry := journal{
		SchemaVersion: 1, Phase: "manifest_verified", CreatedAt: time.Now().UTC(),
		Target: filepath.Base(target), Candidate: candidateName, Backup: "backup-" + suffix,
		Old: oldBuild, New: Build{Version: options.Fetched.Candidate.Release.Version, Commit: options.Fetched.Candidate.Release.Commit, GOOS: options.Fetched.Candidate.Artifact.GOOS, GOARCH: options.Fetched.Candidate.Artifact.GOARCH, SchemaMin: strconv.FormatInt(options.Fetched.Candidate.Release.Schema.Min, 10), SchemaMax: strconv.FormatInt(options.Fetched.Candidate.Release.Schema.Max, 10)},
		OldSHA256: targetDigest, ManifestSHA256: manifestSHA, SignatureSHA256: signatureSHA, TrustFingerprint: keyFingerprint,
	}
	if err = writeJournal(state, entry); err != nil {
		return Status{}, err
	}
	if err = runBeforePhase(options.BeforePhase, entry.Phase); err != nil {
		return Status{}, err
	}
	newDigest, err := options.Fetched.DownloadArtifact(ctx, candidatePath)
	if err != nil {
		return Status{}, err
	}
	if err = os.Chmod(candidatePath, targetMode.Perm()); err != nil {
		_ = os.Remove(candidatePath)
		return Status{}, ErrReplaceFailed
	}
	if err = syncFile(candidatePath); err != nil {
		_ = os.Remove(candidatePath)
		return Status{}, ErrReplaceFailed
	}
	entry.NewSHA256 = newDigest
	entry.Phase = "staged"
	if err = writeJournal(state, entry); err != nil {
		return Status{}, err
	}
	if err = runBeforePhase(options.BeforePhase, entry.Phase); err != nil {
		return Status{}, err
	}
	if candidateBuild, probeErr := probe(ctx, candidatePath); probeErr != nil || !sameBuild(candidateBuild, entry.New) {
		_ = os.Remove(candidatePath)
		return Status{}, ErrProbeFailed
	}
	backupPath := filepath.Join(state, entry.Backup)
	if _, statErr := os.Lstat(backupPath); os.IsNotExist(statErr) {
		if err = copyRegular(target, backupPath, targetMode.Perm()); err != nil {
			return Status{}, ErrReplaceFailed
		}
	} else if statErr != nil {
		return Status{}, ErrReplaceFailed
	}
	backupDigest, _, err := fileDigestAndMode(backupPath)
	if err != nil || backupDigest != entry.OldSHA256 {
		return Status{}, ErrReplaceFailed
	}
	if backupBuild, probeErr := probe(ctx, backupPath); probeErr != nil || !sameBuild(backupBuild, entry.Old) {
		return Status{}, ErrProbeFailed
	}
	entry.Phase = "backup_created"
	if err = writeJournal(state, entry); err != nil {
		return Status{}, err
	}
	if err = runBeforePhase(options.BeforePhase, entry.Phase); err != nil {
		return Status{}, err
	}
	entry.Phase = "replacement_started"
	if err = writeJournal(state, entry); err != nil {
		return Status{}, err
	}
	if err = runBeforePhase(options.BeforePhase, entry.Phase); err != nil {
		return Status{}, err
	}
	if !sameTargetIdentity(target, targetIdentity) {
		return Status{}, ErrRecoveryRequired
	}
	if runtime.GOOS == "windows" {
		if options.WindowsLaunch == nil {
			return Status{}, ErrReplaceFailed
		}
		if err = options.WindowsLaunch(WindowsHelperRequest{ParentPID: uint32(os.Getpid()), StateDir: state, Target: target, Candidate: entry.Candidate}); err != nil {
			return Status{}, ErrReplaceFailed
		}
		return statusFromJournal(entry), nil
	}
	if err = replaceFile(candidatePath, target); err != nil {
		return Status{}, ErrReplaceFailed
	}
	entry.Phase = "replacement_visible"
	if err = writeJournal(state, entry); err != nil {
		return Status{}, err
	}
	if err = runBeforePhase(options.BeforePhase, entry.Phase); err != nil {
		return Status{}, err
	}
	if digest, _, digestErr := fileDigestAndMode(target); digestErr != nil || digest != entry.NewSHA256 {
		if _, rollbackErr := rollbackLocked(ctx, state, target, entry, probe); rollbackErr != nil {
			return Status{}, rollbackErr
		}
		return Status{}, ErrProbeFailed
	}
	if candidateBuild, probeErr := probe(ctx, target); probeErr != nil || !sameBuild(candidateBuild, entry.New) {
		if _, rollbackErr := rollbackLocked(ctx, state, target, entry, probe); rollbackErr != nil {
			return Status{}, rollbackErr
		}
		return Status{}, ErrProbeFailed
	}
	entry.Phase = "committed"
	if err = writeJournal(state, entry); err != nil {
		return Status{}, err
	}
	return statusFromJournal(entry), nil
}

func candidateInManifest(manifest Manifest, candidate Candidate) bool {
	for _, release := range manifest.Releases {
		if !reflect.DeepEqual(release, candidate.Release) {
			continue
		}
		for _, artifact := range release.Artifacts {
			if reflect.DeepEqual(artifact, candidate.Artifact) {
				return true
			}
		}
	}
	return false
}

// Rollback explicitly restores the verified original backup. It never makes a
// database decision; callers must keep schema compatibility separate.
func Rollback(ctx context.Context, target, stateDir string, probe func(context.Context, string) (Build, error)) (Status, error) {
	state, canonical, lock, err := prepareInstallation(target, stateDir)
	if err != nil {
		return Status{}, err
	}
	defer func() { _ = lock.Close() }()
	if err = acquireLock(lock); err != nil {
		return Status{}, err
	}
	defer func() { _ = releaseLock(lock) }()
	executableLock, err := acquireUpdateExecutableLock(canonical)
	if err != nil {
		return Status{}, err
	}
	defer func() { _ = closeExecutableLock(executableLock) }()
	entry, err := loadJournal(state)
	if err != nil {
		return Status{}, ErrRecoveryRequired
	}
	if entry.Target != filepath.Base(canonical) {
		return Status{}, ErrRecoveryRequired
	}
	if entry.Phase != "committed" {
		return recoverLocked(ctx, state, canonical, entry, probe)
	}
	return rollbackLocked(ctx, state, canonical, entry, probe)
}

func Recover(ctx context.Context, target, stateDir string, probe func(context.Context, string) (Build, error)) (Status, error) {
	state, canonical, lock, err := prepareInstallation(target, stateDir)
	if err != nil {
		return Status{}, err
	}
	defer func() { _ = lock.Close() }()
	if err = acquireLock(lock); err != nil {
		return Status{}, err
	}
	defer func() { _ = releaseLock(lock) }()
	executableLock, err := acquireUpdateExecutableLock(canonical)
	if err != nil {
		return Status{}, err
	}
	defer func() { _ = closeExecutableLock(executableLock) }()
	entry, err := loadJournal(state)
	if errors.Is(err, os.ErrNotExist) {
		return Status{Phase: "idle"}, nil
	}
	if err != nil || entry.Target != filepath.Base(canonical) {
		return Status{}, ErrRecoveryRequired
	}
	status, err := recoverLocked(ctx, state, canonical, entry, probe)
	if err == nil && entry.Phase != "committed" && entry.Phase != "rolled_back" && entry.Phase != "aborted" {
		status.Recovered = true
	}
	return status, err
}

// ReadStatus is mutation-free and intentionally does not take an updater lock.
func ReadStatus(target, stateDir string) (Status, error) {
	state, canonical, err := validateInstallation(target, stateDir, false)
	if err != nil {
		return Status{}, err
	}
	entry, err := loadJournal(state)
	if errors.Is(err, os.ErrNotExist) {
		return Status{Phase: "idle", Current: Build{}}, nil
	}
	if err != nil {
		return Status{}, err
	}
	if entry.Target != filepath.Base(canonical) {
		return Status{}, ErrRecoveryRequired
	}
	status := statusFromJournal(entry)
	if current, probeErr := ProbeBinary(context.Background(), canonical); probeErr == nil {
		status.Current = current
	}
	return status, nil
}

func recoverLocked(ctx context.Context, state, target string, entry journal, probe func(context.Context, string) (Build, error)) (Status, error) {
	if probe == nil {
		probe = ProbeBinary
	}
	switch entry.Phase {
	case "committed", "rolled_back", "aborted":
		return statusFromJournal(entry), nil
	case "manifest_verified", "staged", "backup_created":
		_ = os.Remove(filepath.Join(filepath.Dir(target), entry.Candidate))
		entry.Phase = "aborted"
		if err := writeJournal(state, entry); err != nil {
			return Status{}, err
		}
		return statusFromJournal(entry), nil
	case "replacement_started", "replacement_visible":
		digest, _, err := fileDigestAndMode(target)
		if err == nil && digest == entry.NewSHA256 {
			if current, probeErr := probe(ctx, target); probeErr == nil && sameBuild(current, entry.New) {
				entry.Phase = "committed"
				if err = writeJournal(state, entry); err != nil {
					return Status{}, err
				}
				return statusFromJournal(entry), nil
			}
			return rollbackLocked(ctx, state, target, entry, probe)
		}
		if err == nil && digest == entry.OldSHA256 {
			_ = os.Remove(filepath.Join(filepath.Dir(target), entry.Candidate))
			entry.Phase = "rolled_back"
			if err = writeJournal(state, entry); err != nil {
				return Status{}, err
			}
			return statusFromJournal(entry), nil
		}
		return Status{}, ErrRecoveryRequired
	case "rollback_started":
		return recoverRollbackStarted(ctx, state, target, entry, probe)
	default:
		return Status{}, ErrRecoveryRequired
	}
}

// recoverRollbackStarted handles a crash at every rollback publication edge.
// The target is either still the verified new binary (retry after removing a
// verified rollback stage) or already the verified old binary (finish only
// the durable journal transition). Any third state is intentionally retained
// for operator recovery.
func recoverRollbackStarted(ctx context.Context, state, target string, entry journal, probe func(context.Context, string) (Build, error)) (Status, error) {
	digest, _, err := fileDigestAndMode(target)
	if err != nil {
		return Status{}, ErrRecoveryRequired
	}
	if digest == entry.OldSHA256 {
		if restored, probeErr := probe(ctx, target); probeErr != nil || !sameBuild(restored, entry.Old) {
			return Status{}, ErrRollbackFailed
		}
		entry.Phase = "rolled_back"
		if err = writeJournal(state, entry); err != nil {
			return Status{}, err
		}
		return statusFromJournal(entry), nil
	}
	if digest != entry.NewSHA256 {
		return Status{}, ErrRecoveryRequired
	}
	stage := filepath.Join(filepath.Dir(target), "."+filepath.Base(target)+".pgbase-rollback")
	if err = removeVerifiedRollbackStage(stage, entry.OldSHA256); err != nil {
		return Status{}, err
	}
	return rollbackLocked(ctx, state, target, entry, probe)
}

func removeVerifiedRollbackStage(path, expectedDigest string) error {
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return ErrRecoveryRequired
	}
	digest, _, err := fileDigestAndMode(path)
	if err != nil || digest != expectedDigest {
		return ErrRecoveryRequired
	}
	if err = os.Remove(path); err != nil {
		return ErrRecoveryRequired
	}
	return nil
}

func rollbackLocked(ctx context.Context, state, target string, entry journal, probe func(context.Context, string) (Build, error)) (Status, error) {
	if probe == nil {
		probe = ProbeBinary
	}
	backup := filepath.Join(state, entry.Backup)
	digest, mode, err := fileDigestAndMode(backup)
	if err != nil || digest != entry.OldSHA256 || !mode.IsRegular() {
		return Status{}, ErrRollbackFailed
	}
	if original, err := probe(ctx, backup); err != nil || !sameBuild(original, entry.Old) {
		return Status{}, ErrRollbackFailed
	}
	entry.Phase = "rollback_started"
	if err = writeJournal(state, entry); err != nil {
		return Status{}, err
	}
	stage := filepath.Join(filepath.Dir(target), "."+filepath.Base(target)+".pgbase-rollback")
	if _, statErr := os.Lstat(stage); !os.IsNotExist(statErr) {
		return Status{}, ErrRollbackFailed
	}
	if err = copyRegular(backup, stage, mode.Perm()); err != nil {
		return Status{}, ErrRollbackFailed
	}
	if err = replaceFile(stage, target); err != nil {
		_ = os.Remove(stage)
		return Status{}, ErrRollbackFailed
	}
	if restored, probeErr := probe(ctx, target); probeErr != nil || !sameBuild(restored, entry.Old) {
		return Status{}, ErrRollbackFailed
	}
	entry.Phase = "rolled_back"
	if err = writeJournal(state, entry); err != nil {
		return Status{}, err
	}
	return statusFromJournal(entry), nil
}

func statusFromJournal(entry journal) Status {
	current := entry.Old
	if entry.Phase == "committed" {
		current = entry.New
	}
	return Status{Phase: entry.Phase, Current: current, Candidate: entry.New, Previous: entry.Old, BackupSHA: entry.OldSHA256}
}

func prepareInstallation(target, stateDir string) (string, string, *os.File, error) {
	state, canonical, err := validateInstallation(target, stateDir, true)
	if err != nil {
		return "", "", nil, err
	}
	lock, err := openStateLock(filepath.Join(state, "lock"))
	if err != nil {
		return "", "", nil, err
	}
	return state, canonical, lock, nil
}

func openStateLock(path string) (*os.File, error) {
	lock, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0600)
	if err == nil {
		if !validPrivateStateFile(lock) {
			_ = lock.Close()
			return nil, ErrInvalidInput
		}
		return lock, nil
	}
	if !os.IsExist(err) {
		return nil, ErrReplaceFailed
	}
	lock, err = openExistingRegularFile(path, os.O_RDWR)
	if err != nil {
		return nil, ErrInvalidInput
	}
	if !validPrivateStateFile(lock) {
		_ = lock.Close()
		return nil, ErrInvalidInput
	}
	return lock, nil
}

func validateInstallation(target, stateDir string, createState bool) (string, string, error) {
	if !filepath.IsAbs(target) || !filepath.IsAbs(stateDir) {
		return "", "", ErrInvalidInput
	}
	// Check the requested leaf before resolving the macOS /var -> /private/var
	// alias. A user-visible executable symlink is rejected, while filesystem
	// canonicalization of its containing volume remains supported.
	if err := ensureRegular(target); err != nil {
		return "", "", err
	}
	canonical, err := filepath.EvalSymlinks(target)
	if err != nil {
		return "", "", ErrInvalidInput
	}
	if err = ensureRegular(canonical); err != nil {
		return "", "", err
	}
	targetInfo, targetErr := os.Lstat(canonical)
	if targetErr != nil || !validInstallationTarget(targetInfo) {
		return "", "", ErrInvalidInput
	}
	directory := filepath.Dir(canonical)
	directoryInfo, statErr := os.Lstat(directory)
	if statErr != nil || !directoryInfo.IsDir() || directoryInfo.Mode()&os.ModeSymlink != 0 || !validInstallationDirectory(directoryInfo) {
		return "", "", ErrInvalidInput
	}
	requestedParent, err := os.Stat(filepath.Dir(stateDir))
	if err != nil || !os.SameFile(requestedParent, directoryInfo) || filepath.Base(stateDir) == "." || filepath.Base(stateDir) == string(filepath.Separator) {
		return "", "", ErrInvalidInput
	}
	stateDir = filepath.Join(directory, filepath.Base(stateDir))
	info, err := os.Lstat(stateDir)
	if os.IsNotExist(err) {
		if !createState {
			return stateDir, canonical, nil
		}
		if err = os.Mkdir(stateDir, 0700); err != nil {
			return "", "", ErrReplaceFailed
		}
	} else if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || !validPrivateStateDirectory(info) {
		return "", "", ErrInvalidInput
	}
	return stateDir, canonical, nil
}

func sameTargetIdentity(path string, expected os.FileInfo) bool {
	current, err := os.Lstat(path)
	return err == nil && current.Mode().IsRegular() && current.Mode()&os.ModeSymlink == 0 && os.SameFile(expected, current)
}

func ensureRegular(path string) error {
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return ErrInvalidInput
	}
	return nil
}

func fileDigestAndMode(path string) (string, os.FileMode, error) {
	file, err := openRegularFile(path)
	if err != nil {
		return "", 0, err
	}
	defer func() { _ = file.Close() }()
	info, err := file.Stat()
	if err != nil {
		return "", 0, err
	}
	digest := sha256.New()
	if _, err = io.Copy(digest, file); err != nil {
		return "", 0, err
	}
	return hex.EncodeToString(digest.Sum(nil)), info.Mode(), nil
}

func copyRegular(source, target string, mode os.FileMode) error {
	from, err := openRegularFile(source)
	if err != nil {
		return err
	}
	defer func() { _ = from.Close() }()
	return durablefile.WriteExclusive(target, mode, func(destination *os.File) error {
		_, err := io.Copy(destination, from)
		return err
	}, func(_ string, err error) error { return err })
}

func syncFile(path string) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	return errors.Join(file.Sync(), file.Close())
}

func runBeforePhase(hook func(string) error, phase string) error {
	if hook == nil {
		return nil
	}
	return hook(phase)
}

func sameBuild(left, right Build) bool {
	return left.Version == right.Version && left.Commit == right.Commit && left.GOOS == right.GOOS && left.GOARCH == right.GOARCH &&
		left.SchemaMin == right.SchemaMin && left.SchemaMax == right.SchemaMax
}

// ProbeBinary executes only the requested path's network-free version command.
func ProbeBinary(ctx context.Context, path string) (Build, error) {
	probeContext, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	command := exec.CommandContext(probeContext, path, "version", "--json")
	stdout := &limitedBuffer{limit: probeLimit}
	command.Stdout, command.Stderr = stdout, io.Discard
	if err := command.Run(); err != nil || stdout.exceeded {
		return Build{}, ErrProbeFailed
	}
	decoder := json.NewDecoder(strings.NewReader(stdout.String()))
	decoder.DisallowUnknownFields()
	var build Build
	if err := decoder.Decode(&build); err != nil || decoder.Decode(&struct{}{}) != io.EOF || !validBuild(build) {
		return Build{}, ErrProbeFailed
	}
	return build, nil
}

func validBuild(build Build) bool {
	if build.Version == "dev" && build.Commit == "unknown" {
		return validIdentifier(build.GOOS) && validIdentifier(build.GOARCH)
	}
	return validVersion(build.Version) && validCommit(build.Commit) && validIdentifier(build.GOOS) && validIdentifier(build.GOARCH)
}

type limitedBuffer struct {
	bytes    []byte
	limit    int
	exceeded bool
}

func (b *limitedBuffer) Write(value []byte) (int, error) {
	if len(b.bytes)+len(value) > b.limit {
		b.exceeded = true
		return 0, fmt.Errorf("probe output exceeded limit")
	}
	b.bytes = append(b.bytes, value...)
	return len(value), nil
}
func (b *limitedBuffer) String() string { return string(b.bytes) }
