package pgbase

import "testing"

func TestBackupS3OptionsRequireDurableDirectoryAndCompleteIdentity(t *testing.T) {
	if err := validateBackupOptions(BackupOptions{S3: BackupS3Options{Bucket: "backups", Region: "us-east-1"}}); err == nil {
		t.Fatal("S3 without durable local directory accepted")
	}
	if err := validateBackupOptions(BackupOptions{Directory: t.TempDir(), S3: BackupS3Options{Bucket: "backups"}}); err == nil {
		t.Fatal("S3 without region accepted")
	}
	if err := validateBackupOptions(BackupOptions{Directory: t.TempDir(), S3: BackupS3Options{Bucket: "backups", Region: "us-east-1", AccessKey: "only"}}); err == nil {
		t.Fatal("unpaired S3 credentials accepted")
	}
	if err := validateBackupOptions(BackupOptions{Directory: t.TempDir(), S3: BackupS3Options{Bucket: "backups", Region: "us-east-1"}}); err != nil {
		t.Fatal(err)
	}
}
