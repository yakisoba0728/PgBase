package pgbase

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/webisnoteasy/PgBase/internal/identity"
	"github.com/webisnoteasy/PgBase/internal/jsvm"
)

type javaScriptJobRun struct {
	JobID           string `json:"jobId"`
	Name            string `json:"name"`
	FenceGeneration int64  `json:"fenceGeneration"`
	RunKey          string `json:"runKey"`
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
					FenceGeneration: run.FenceGeneration, RunKey: run.RunKey()})
				if err != nil {
					return err
				}
				_, err = runtime.engine.Invoke(ctx, jsvm.Invocation{Handler: descriptor.Handler, Capabilities: descriptor.Capabilities,
					Input: input, Host: runtime.bridge.forSubject(identity.Subject{Kind: identity.Service, ID: subject}, fmt.Sprintf("job-%s-%d", run.JobID, run.FenceGeneration))})
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
