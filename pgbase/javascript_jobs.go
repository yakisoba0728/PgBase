package pgbase

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/yakisoba0728/PgBase/internal/identity"
	"github.com/yakisoba0728/PgBase/internal/jsvm"
)

type javaScriptJobRun struct {
	JobID           string `json:"jobId"`
	Name            string `json:"name"`
	FenceGeneration int64  `json:"fenceGeneration"`
	RunKey          string `json:"runKey"`
	ScheduledAt     string `json:"scheduledAt"`
	OccurrenceKey   string `json:"occurrenceKey"`
}

func installJavaScriptJobs(options *Options, runtime *javaScriptRuntime) error {
	if runtime == nil {
		return nil
	}
	descriptors := runtime.bundle.Manifest().Jobs
	if len(descriptors) == 0 {
		return nil
	}
	if !options.JobsEnabled {
		return errors.New("JavaScript jobs require JobsEnabled")
	}
	jobs := append([]ScheduledJob(nil), options.Jobs...)
	for _, descriptor := range descriptors {
		descriptor := descriptor
		subject := runtime.options.JobServiceSubjects[descriptor.Name]
		if subject == [16]byte{} {
			return fmt.Errorf("JavaScript job %q requires JobServiceSubjects entry", descriptor.Name)
		}
		jobs = append(jobs, ScheduledJob{Name: descriptor.Name, Schedule: descriptor.Schedule, Timezone: descriptor.Timezone,
			Handler: func(ctx context.Context, run JobRun) error {
				input, err := json.Marshal(javaScriptJobRun{JobID: run.JobID.String(), Name: run.Name,
					FenceGeneration: run.FenceGeneration, RunKey: run.RunKey(), ScheduledAt: run.ScheduledAt.UTC().Format(time.RFC3339Nano), OccurrenceKey: run.OccurrenceKey()})
				if err != nil {
					return err
				}
				_, err = runtime.engine.Invoke(ctx, jsvm.Invocation{Handler: descriptor.Handler, Capabilities: descriptor.Capabilities,
					Input: input, Host: namespacedHost(runtime.bridge.forSubject(identity.Subject{Kind: identity.Service, ID: subject}, fmt.Sprintf("job-%s-%d", run.JobID, run.FenceGeneration)), descriptor.Handler.Module)})
				return err
			},
		})
	}
	if err := validateScheduledJobs(jobs); err != nil {
		return fmt.Errorf("JavaScript jobs: %w", err)
	}
	options.Jobs = jobs
	return nil
}
