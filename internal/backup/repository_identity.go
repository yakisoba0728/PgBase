package backup

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
)

type identifiedRepository interface{ RepositoryIdentity() string }
type repositoryIdentity struct {
	Identity string `json:"identity"`
}

func ensureRepositoryIdentity(directory string, repository Repository) error {
	identified, ok := repository.(identifiedRepository)
	if !ok {
		return nil
	}
	path := filepath.Join(directory, "repository.json")
	raw, err := os.ReadFile(path)
	if err == nil {
		var stored repositoryIdentity
		if json.Unmarshal(raw, &stored) != nil || stored.Identity != identified.RepositoryIdentity() {
			return errors.New("backup repository configuration differs from the durable workspace")
		}
		return nil
	}
	if !os.IsNotExist(err) {
		return err
	}
	matches, _ := filepath.Glob(filepath.Join(directory, "*.json"))
	if len(matches) > 0 && identified.RepositoryIdentity() != "local" {
		return errors.New("backup repository cannot change while the durable workspace contains backups or jobs")
	}
	return writeAtomic(path, repositoryIdentity{Identity: identified.RepositoryIdentity()})
}
