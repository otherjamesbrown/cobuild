package cmd

import (
	"testing"
	"time"

	"github.com/otherjamesbrown/cobuild/internal/store"
)

func TestStatusHealthFor(t *testing.T) {
	now := time.Now()
	tests := []struct {
		name         string
		status       string
		lastProgress time.Time
		want         string
	}{
		{"completed run not shown", "completed", now, "-"},
		{"cancelled run not shown", "cancelled", now, "-"},
		{"active fresh", "active", now.Add(-2 * time.Minute), "ACTIVE"},
		{"active right at stale boundary", "active", now.Add(-(statusStaleAfter + time.Second)), "STALE"},
		{"active between stale and dead", "active", now.Add(-30 * time.Minute), "STALE"},
		{"active past dead boundary", "active", now.Add(-(statusDeadAfter + time.Second)), "DEAD"},
		{"active with unknown last progress", "active", time.Time{}, "UNKNOWN"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := statusHealthFor(tc.status, tc.lastProgress)
			if got != tc.want {
				t.Fatalf("health = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestStatusFilterAndSortRunsActiveFilter(t *testing.T) {
	now := time.Date(2026, 4, 15, 12, 0, 0, 0, time.UTC)
	runs := []store.PipelineRunStatus{
		{
			DesignID:      "cb-completed-recent",
			Status:        "completed",
			LastProgress:  now.Add(-72 * time.Hour),
			LastSessionAt: now.Add(-2 * time.Hour),
		},
		{
			DesignID:      "cb-active-old",
			Status:        "active",
			LastProgress:  now.Add(-72 * time.Hour),
			LastSessionAt: now.Add(-72 * time.Hour),
		},
		{
			DesignID:      "cb-completed-stale",
			Status:        "completed",
			LastProgress:  now.Add(-1 * time.Hour),
			LastSessionAt: now.Add(-48 * time.Hour),
		},
		{
			DesignID:      "cb-in-progress",
			Status:        "in_progress",
			LastProgress:  now.Add(-30 * time.Minute),
			LastSessionAt: now.Add(-30 * time.Minute),
		},
	}

	got := statusFilterAndSortRuns(runs, true, 24*time.Hour, now)
	if len(got) != 3 {
		t.Fatalf("filtered len = %d, want 3", len(got))
	}
	wantOrder := []string{"cb-in-progress", "cb-completed-recent", "cb-active-old"}
	for i, want := range wantOrder {
		if got[i].DesignID != want {
			t.Fatalf("filtered[%d] = %q, want %q (full=%v)", i, got[i].DesignID, want, []string{got[0].DesignID, got[1].DesignID, got[2].DesignID})
		}
	}
}

func TestStatusFilterAndSortRunsOrdersByLatestActivity(t *testing.T) {
	now := time.Date(2026, 4, 15, 12, 0, 0, 0, time.UTC)
	runs := []store.PipelineRunStatus{
		{
			DesignID:      "cb-old-updated-recent-session",
			Status:        "completed",
			LastProgress:  now.Add(-48 * time.Hour),
			LastSessionAt: now.Add(-90 * time.Minute),
		},
		{
			DesignID:      "cb-recently-updated",
			Status:        "completed",
			LastProgress:  now.Add(-2 * time.Hour),
			LastSessionAt: now.Add(-72 * time.Hour),
		},
		{
			DesignID:      "cb-ancient",
			Status:        "completed",
			LastProgress:  now.Add(-96 * time.Hour),
			LastSessionAt: now.Add(-96 * time.Hour),
		},
	}

	got := statusFilterAndSortRuns(runs, false, 24*time.Hour, now)
	wantOrder := []string{"cb-old-updated-recent-session", "cb-recently-updated", "cb-ancient"}
	for i, want := range wantOrder {
		if got[i].DesignID != want {
			t.Fatalf("sorted[%d] = %q, want %q", i, got[i].DesignID, want)
		}
	}
}
