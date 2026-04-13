package livestate

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/otherjamesbrown/cobuild/internal/store"
)

type stubPipelineRunLister struct {
	project string
	runs    []store.PipelineRunStatus
	err     error
}

func (s *stubPipelineRunLister) ListRuns(_ context.Context, project string) ([]store.PipelineRunStatus, error) {
	s.project = project
	if s.err != nil {
		return nil, s.err
	}
	return s.runs, nil
}

func TestCollectPipelinesMapsRunStatusRows(t *testing.T) {
	lastProgress := time.Date(2026, 4, 13, 10, 30, 0, 0, time.UTC)
	lister := &stubPipelineRunLister{
		runs: []store.PipelineRunStatus{
			{
				DesignID:     "cb-25b0a4",
				Project:      "cobuild",
				Phase:        "implement",
				Status:       "active",
				TaskTotal:    4,
				TaskDone:     2,
				TaskBlocked:  1,
				LastProgress: lastProgress,
			},
		},
	}

	got, err := CollectPipelines(context.Background(), lister)
	if err != nil {
		t.Fatalf("CollectPipelines() error = %v", err)
	}

	want := []PipelineInfo{
		{
			DesignID:     "cb-25b0a4",
			Project:      "cobuild",
			Phase:        "implement",
			Status:       "active",
			TaskTotal:    4,
			TaskDone:     2,
			TaskBlocked:  1,
			LastProgress: lastProgress,
		},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("CollectPipelines() = %#v, want %#v", got, want)
	}
	if lister.project != "" {
		t.Fatalf("ListRuns project = %q, want empty string for all projects", lister.project)
	}
}

func TestCollectPipelinesReturnsStoreError(t *testing.T) {
	lister := &stubPipelineRunLister{err: errors.New("db offline")}

	_, err := CollectPipelines(context.Background(), lister)
	if err == nil {
		t.Fatal("CollectPipelines() error = nil, want error")
	}
	if got, want := err.Error(), "list runs: db offline"; got != want {
		t.Fatalf("CollectPipelines() error = %q, want %q", got, want)
	}
}
