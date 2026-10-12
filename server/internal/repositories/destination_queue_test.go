package repositories

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/arkeep-io/arkeep/server/internal/db"
)

func idsOf(dests ...*db.Destination) []uuid.UUID {
	ids := make([]uuid.UUID, len(dests))
	for i, d := range dests {
		ids[i] = d.ID
	}
	return ids
}

func jobIDs(jobs []db.Job) []uuid.UUID {
	ids := make([]uuid.UUID, len(jobs))
	for i, j := range jobs {
		ids[i] = j.ID
	}
	return ids
}

func TestTryAcquireBusyAll(t *testing.T) {
	gdb := newTestDB(t)
	destRepo := NewDestinationRepository(gdb)
	jobRepo := NewJobRepository(gdb)
	agentRepo := NewAgentRepository(gdb)
	ctx := context.Background()

	busy, holder := busyFixture(t, destRepo, jobRepo, agentRepo, "busy")
	free := &db.Destination{Name: "d-free", Type: "local", Config: "{}", Enabled: true}
	if err := destRepo.Create(ctx, free); err != nil {
		t.Fatalf("create destination: %v", err)
	}
	job := &db.Job{AgentID: holder.AgentID, Type: "backup", Status: "pending"}
	if err := jobRepo.Create(ctx, job); err != nil {
		t.Fatalf("create job: %v", err)
	}

	// One destination busy: nothing is claimed, not even the free one.
	acquired, err := destRepo.TryAcquireBusyAll(ctx, nil, job.ID, db.LockExclusive)
	if err != nil || !acquired {
		t.Fatalf("TryAcquireBusyAll(no destinations) = %v, %v; want true, nil", acquired, err)
	}
	acquired, err = destRepo.TryAcquireBusyAll(ctx, idsOf(free, busy), job.ID, db.LockExclusive)
	if err != nil {
		t.Fatalf("TryAcquireBusyAll: %v", err)
	}
	if acquired {
		t.Fatal("TryAcquireBusyAll with a busy destination = true, want false")
	}
	if got := busyHolder(t, destRepo, free.ID); got != "" {
		t.Errorf("free destination holder = %q, want none: a refused claim must roll back", got)
	}
	if got := busyHolder(t, destRepo, busy.ID); got != holder.ID.String() {
		t.Errorf("busy destination holder = %q, want %s", got, holder.ID)
	}

	// Holder done: both are claimed, and claiming again is idempotent.
	if err := jobRepo.UpdateStatus(ctx, holder.ID, "succeeded", nil, nil, ""); err != nil {
		t.Fatalf("finish holder: %v", err)
	}
	for range 2 {
		acquired, err = destRepo.TryAcquireBusyAll(ctx, idsOf(free, busy, free), job.ID, db.LockExclusive)
		if err != nil || !acquired {
			t.Fatalf("TryAcquireBusyAll = %v, %v; want true, nil", acquired, err)
		}
	}
	for _, d := range []*db.Destination{free, busy} {
		if got := busyHolder(t, destRepo, d.ID); got != job.ID.String() {
			t.Errorf("destination %s holder = %q, want %s", d.Name, got, job.ID)
		}
	}
}

// TestTryAcquireBusyAll_Modes covers the shared/exclusive destination locks of
// issue #319: backups hold a destination side by side, a retention sweep or
// integrity check needs it alone, and a refused claim takes nothing.
func TestTryAcquireBusyAll_Modes(t *testing.T) {
	gdb := newTestDB(t)
	destRepo := NewDestinationRepository(gdb)
	jobRepo := NewJobRepository(gdb)
	agentRepo := NewAgentRepository(gdb)
	ctx := context.Background()

	agent := newTestAgentForRetention(t, agentRepo, "agent")
	dest := &db.Destination{Name: "d", Type: "local", Config: "{}", Enabled: true}
	free := &db.Destination{Name: "free", Type: "local", Config: "{}", Enabled: true}
	for _, d := range []*db.Destination{dest, free} {
		if err := destRepo.Create(ctx, d); err != nil {
			t.Fatalf("create destination: %v", err)
		}
	}
	newJob := func(jobType string) *db.Job {
		t.Helper()
		j := &db.Job{AgentID: agent.ID, Type: jobType, Status: "running"}
		if err := jobRepo.Create(ctx, j); err != nil {
			t.Fatalf("create job: %v", err)
		}
		time.Sleep(2 * time.Millisecond) // distinct acquired_at for the holder order
		return j
	}
	claim := func(j *db.Job, mode string, dests ...*db.Destination) bool {
		t.Helper()
		ok, err := destRepo.TryAcquireBusyAll(ctx, idsOf(dests...), j.ID, mode)
		if err != nil {
			t.Fatalf("TryAcquireBusyAll: %v", err)
		}
		return ok
	}

	backup1, backup2, sweep := newJob("backup"), newJob("backup"), newJob("retention")

	if !claim(backup1, db.LockShared, dest) || !claim(backup2, db.LockShared, dest) {
		t.Fatal("two shared claims on one destination: want both granted")
	}
	if got, want := busyHolder(t, destRepo, dest.ID), backup1.ID.String()+","+backup2.ID.String(); got != want {
		t.Errorf("holders = %q, want %q", got, want)
	}
	if claim(sweep, db.LockExclusive, dest) {
		t.Error("exclusive claim while backups hold the destination = true, want false")
	}

	locks, err := destRepo.ListLocks(ctx, idsOf(dest, free))
	if err != nil {
		t.Fatalf("ListLocks: %v", err)
	}
	if len(locks) != 2 || locks[0].JobID != backup1.ID || locks[1].JobID != backup2.ID || locks[0].Mode != db.LockShared {
		t.Errorf("ListLocks = %+v, want the two shared holds, oldest first", locks)
	}

	// Both backups end: the sweep gets the destination alone.
	for _, j := range []*db.Job{backup1, backup2} {
		if err := jobRepo.UpdateStatus(ctx, j.ID, "succeeded", nil, nil, ""); err != nil {
			t.Fatalf("finish backup: %v", err)
		}
	}
	if !claim(sweep, db.LockExclusive, dest) {
		t.Fatal("exclusive claim on a free destination = false, want true")
	}
	backup3 := newJob("backup")
	if claim(backup3, db.LockShared, free, dest) {
		t.Error("shared claim while a sweep holds one of the destinations = true, want false")
	}
	if got := busyHolder(t, destRepo, free.ID); got != "" {
		t.Errorf("free destination holder = %q, want none: a refused claim must take nothing", got)
	}
}

func TestDestinationQueueQueries(t *testing.T) {
	gdb := newTestDB(t)
	destRepo := NewDestinationRepository(gdb)
	jobRepo := NewJobRepository(gdb)
	agentRepo := NewAgentRepository(gdb)
	ctx := context.Background()

	agent := newTestAgentForRetention(t, agentRepo, "agent")
	a := &db.Destination{Name: "a", Type: "local", Config: "{}", Enabled: true}
	b := &db.Destination{Name: "b", Type: "local", Config: "{}", Enabled: true}
	for _, d := range []*db.Destination{a, b} {
		if err := destRepo.Create(ctx, d); err != nil {
			t.Fatalf("create destination: %v", err)
		}
	}
	policy := &db.Policy{Name: "p", AgentID: agent.ID, Schedule: "@daily", Sources: "[]", RepoPassword: db.EncryptedString("x")}
	if err := NewPolicyRepository(gdb).Create(ctx, policy); err != nil {
		t.Fatalf("create policy: %v", err)
	}

	newJob := func(jobType, status string, destStatus map[*db.Destination]string) *db.Job {
		t.Helper()
		j := &db.Job{AgentID: agent.ID, Type: jobType, Status: "pending"}
		if jobType == "backup" {
			j.PolicyID = &policy.ID
		}
		if err := jobRepo.Create(ctx, j); err != nil {
			t.Fatalf("create job: %v", err)
		}
		setJobStatus(t, jobRepo, j, status)
		for d, st := range destStatus {
			if err := jobRepo.CreateDestination(ctx, &db.JobDestination{JobID: j.ID, DestinationID: d.ID, Status: st}); err != nil {
				t.Fatalf("create job destination: %v", err)
			}
		}
		time.Sleep(2 * time.Millisecond) // distinct created_at for the FIFO order
		return j
	}

	if has, _ := jobRepo.HasPendingJob(ctx, policy.ID); has {
		t.Fatal("HasPendingJob before any job = true")
	}
	older := newJob("backup", "waiting", map[*db.Destination]string{a: "pending", b: "skipped"})
	younger := newJob("backup", "waiting", map[*db.Destination]string{b: "pending"})
	newJob("backup", "running", map[*db.Destination]string{a: "pending"})

	if has, err := jobRepo.HasPendingJob(ctx, policy.ID); err != nil || !has {
		t.Errorf("HasPendingJob with a waiting job = %v, %v; want true: a waiting job must coalesce scheduled ticks", has, err)
	}

	waiting, err := jobRepo.ListWaiting(ctx, 10)
	if err != nil {
		t.Fatalf("ListWaiting: %v", err)
	}
	if len(waiting) != 2 || waiting[0].ID != older.ID || waiting[1].ID != younger.ID {
		t.Errorf("ListWaiting = %v, want [older, younger]", jobIDs(waiting))
	}

	ids, err := jobRepo.ListQueuedDestinationIDs(ctx, older.ID)
	if err != nil {
		t.Fatalf("ListQueuedDestinationIDs: %v", err)
	}
	if len(ids) != 1 || ids[0] != a.ID {
		t.Errorf("ListQueuedDestinationIDs = %v, want only the unresolved destination %s", ids, a.ID)
	}

	for _, tc := range []struct {
		name    string
		dests   []*db.Destination
		exclude *db.Job
		mode    string
		want    bool
	}{
		{"a needed by older", []*db.Destination{a}, younger, db.LockExclusive, true},
		{"b only skipped by older", []*db.Destination{b}, younger, db.LockExclusive, false},
		{"b needed by younger", []*db.Destination{b}, older, db.LockExclusive, true},
		{"own rows excluded", []*db.Destination{a}, older, db.LockExclusive, false},
		// Issue #319: waiting backups want a shared hold, so another backup
		// does not queue behind them.
		{"shared claim ignores waiting backups", []*db.Destination{a, b}, younger, db.LockShared, false},
	} {
		got, err := jobRepo.HasWaitingForDestinations(ctx, idsOf(tc.dests...), tc.exclude.ID, tc.mode)
		if err != nil {
			t.Fatalf("%s: HasWaitingForDestinations: %v", tc.name, err)
		}
		if got != tc.want {
			t.Errorf("%s: HasWaitingForDestinations = %v, want %v", tc.name, got, tc.want)
		}
	}

	if has, _ := jobRepo.HasActiveJobOfType(ctx, "retention", a.ID); has {
		t.Error("HasActiveJobOfType(retention) with only backups = true, want false")
	}
	newJob("retention", "succeeded", map[*db.Destination]string{a: "succeeded"})
	if has, _ := jobRepo.HasActiveJobOfType(ctx, "retention", a.ID); has {
		t.Error("HasActiveJobOfType(retention) with a finished sweep = true, want false")
	}
	newJob("retention", "waiting", map[*db.Destination]string{a: "pending"})
	if has, err := jobRepo.HasActiveJobOfType(ctx, "retention", a.ID); err != nil || !has {
		t.Errorf("HasActiveJobOfType(retention) with a waiting sweep = %v, %v; want true", has, err)
	}
	if has, err := jobRepo.HasWaitingForDestinations(ctx, idsOf(a), younger.ID, db.LockShared); err != nil || !has {
		t.Errorf("HasWaitingForDestinations(shared) with a waiting sweep = %v, %v; want true: a backup must not overtake it", has, err)
	}
	if has, _ := jobRepo.HasActiveJobOfType(ctx, "retention", b.ID); has {
		t.Error("HasActiveJobOfType(retention) on another destination = true, want false")
	}
	if has, _ := jobRepo.HasActiveJobOfType(ctx, "check", a.ID); has {
		t.Error("HasActiveJobOfType(check) with only a waiting sweep = true, want false")
	}
}
