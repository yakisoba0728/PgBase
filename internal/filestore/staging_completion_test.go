package filestore

import (
	"errors"
	"sync"
	"sync/atomic"
	"testing"
)

func TestStagedCopiesShareOneFinalOutcomeAndAdmissionRelease(t *testing.T) {
	operationErr := errors.New("transaction rolled back")
	var finishes, releases atomic.Int32
	file := StagedFile{completion: &stagedCompletion{
		finish: func(err error) {
			if !errors.Is(err, operationErr) {
				t.Errorf("first outcome lost: %v", err)
			}
			finishes.Add(1)
		},
		release: func() { releases.Add(1) },
	}}
	service := &Service{}
	service.CompleteStaged([]StagedFile{file}, operationErr)
	var callers sync.WaitGroup
	for range 16 {
		callers.Add(1)
		go func() {
			defer callers.Done()
			service.CompleteStaged([]StagedFile{file, file}, nil)
		}()
	}
	callers.Wait()
	if finishes.Load() != 1 || releases.Load() != 1 {
		t.Fatalf("completion/release = %d/%d", finishes.Load(), releases.Load())
	}
}

func TestStagedCompletionReleasesAdmissionEvenWhenHookPanics(t *testing.T) {
	released := false
	file := StagedFile{completion: &stagedCompletion{
		finish:  func(error) { panic("hook failed") },
		release: func() { released = true },
	}}
	func() {
		defer func() {
			if recover() != "hook failed" {
				t.Error("hook panic lost")
			}
		}()
		(&Service{}).CompleteStaged([]StagedFile{file}, nil)
	}()
	if !released {
		t.Fatal("hook panic leaked admission")
	}
}
