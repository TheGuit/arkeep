package api

import (
	"context"
	"net/http"
	"testing"

	"github.com/google/uuid"

	"github.com/arkeep-io/arkeep/server/internal/db"
)

// TestJobHandler_CancelReleasesBusyGate reproduces issue #290: a retention
// sweep holding a destination's busy gate is cancelled from the GUI. The gate
// must be released at once, and the destination API must show it free —
// before the fix the cancelled job kept it forever and every later backup to
// the destination was skipped.
func TestJobHandler_CancelReleasesBusyGate(t *testing.T) {
	e := newTestEnv(t)
	ctx := context.Background()

	agent := createDBAgent(t, e.deps, "retention-agent")
	dest := createDBDestination(t, e.deps, "r2", "local")
	job := &db.Job{AgentID: agent.ID, Type: "retention", Status: "running"}
	if err := e.deps.jobs.Create(ctx, job); err != nil {
		t.Fatalf("create job: %v", err)
	}
	if acquired, err := e.deps.dests.TryAcquireBusyAll(ctx, []uuid.UUID{dest.ID}, job.ID, db.LockExclusive); err != nil || !acquired {
		t.Fatalf("TryAcquireBusy: acquired=%v err=%v", acquired, err)
	}

	locks := func() []destinationLockResponse {
		t.Helper()
		resp := e.get(t, "/api/v1/destinations/"+dest.ID.String(), e.adminToken(t))
		assertStatus(t, resp, http.StatusOK)
		var d struct {
			Locks []destinationLockResponse `json:"locks"`
		}
		decodeData(t, resp, &d)
		return d.Locks
	}

	if got := locks(); len(got) != 1 || got[0].JobID != job.ID.String() || got[0].Mode != db.LockExclusive || got[0].Since == "" {
		t.Fatalf("locks before cancel = %+v, want the sweep's exclusive hold", got)
	}

	resp := e.post(t, "/api/v1/jobs/"+job.ID.String()+"/cancel", e.adminToken(t), nil)
	assertStatus(t, resp, http.StatusOK)

	if got := locks(); len(got) != 0 {
		t.Errorf("locks after cancel = %+v, want the destination released", got)
	}
}

// TestDestinationHandler_ListShowsLocks: the list reports every job holding a
// destination — several backups at once since issue #319 — and an empty list,
// not null, for a free one.
func TestDestinationHandler_ListShowsLocks(t *testing.T) {
	e := newTestEnv(t)
	ctx := context.Background()

	agent := createDBAgent(t, e.deps, "agent")
	busy := createDBDestination(t, e.deps, "busy", "local")
	free := createDBDestination(t, e.deps, "free", "local")
	for range 2 {
		job := &db.Job{AgentID: agent.ID, Type: "backup", Status: "running"}
		if err := e.deps.jobs.Create(ctx, job); err != nil {
			t.Fatalf("create job: %v", err)
		}
		if acquired, err := e.deps.dests.TryAcquireBusyAll(ctx, []uuid.UUID{busy.ID}, job.ID, db.LockShared); err != nil || !acquired {
			t.Fatalf("TryAcquireBusyAll: acquired=%v err=%v", acquired, err)
		}
	}

	resp := e.get(t, "/api/v1/destinations", e.adminToken(t))
	assertStatus(t, resp, http.StatusOK)
	var list struct {
		Items []struct {
			ID    string                     `json:"id"`
			Locks *[]destinationLockResponse `json:"locks"`
		} `json:"items"`
	}
	decodeData(t, resp, &list)
	for _, item := range list.Items {
		switch item.ID {
		case busy.ID.String():
			if item.Locks == nil || len(*item.Locks) != 2 || (*item.Locks)[0].Mode != db.LockShared {
				t.Errorf("busy destination locks = %+v, want two shared holds", item.Locks)
			}
		case free.ID.String():
			if item.Locks == nil || len(*item.Locks) != 0 {
				t.Errorf("free destination locks = %+v, want an empty list", item.Locks)
			}
		}
	}
}

// TestJobHandler_CancelWaitingJob: a job queued behind a busy destination
// (issue #285) can be cancelled, and the status filter lists waiting jobs.
func TestJobHandler_CancelWaitingJob(t *testing.T) {
	e := newTestEnv(t)
	ctx := context.Background()

	agent := createDBAgent(t, e.deps, "retention-agent")
	job := &db.Job{AgentID: agent.ID, Type: "retention", Status: "pending"}
	if err := e.deps.jobs.Create(ctx, job); err != nil {
		t.Fatalf("create job: %v", err)
	}
	if err := e.deps.jobs.UpdateStatus(ctx, job.ID, "waiting", nil, nil, ""); err != nil {
		t.Fatalf("queue job: %v", err)
	}

	resp := e.get(t, "/api/v1/jobs?status=waiting", e.adminToken(t))
	assertStatus(t, resp, http.StatusOK)
	var list struct {
		Total int64 `json:"total"`
	}
	decodeData(t, resp, &list)
	if list.Total != 1 {
		t.Errorf("jobs with status=waiting = %d, want 1", list.Total)
	}

	resp = e.post(t, "/api/v1/jobs/"+job.ID.String()+"/cancel", e.adminToken(t), nil)
	assertStatus(t, resp, http.StatusOK)

	stored, err := e.deps.jobs.GetByID(ctx, job.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if stored.Status != "cancelled" {
		t.Errorf("status after cancel = %q, want \"cancelled\"", stored.Status)
	}
}
