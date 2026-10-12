package destqueue

import (
	"bytes"
	"context"
	"errors"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"

	"github.com/arkeep-io/arkeep/server/internal/db"
	"github.com/arkeep-io/arkeep/server/internal/notification"
	"github.com/arkeep-io/arkeep/server/internal/repositories"
)

// agentSet is an AgentChecker over a fixed set of connected agents.
type agentSet map[string]bool

func (a agentSet) IsConnected(agentID string) bool { return a[agentID] }

func (a agentSet) SendCancel(string, string) error { return nil }

// cancellingAgents is an agentSet that records the cancels it is sent.
type cancellingAgents struct {
	agentSet
	mu        sync.Mutex
	cancelled []string
}

func (c *cancellingAgents) SendCancel(_, jobID string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.cancelled = append(c.cancelled, jobID)
	return nil
}

// failedNotifier is a notification.Service that reports each job-failed
// notification on a channel: the queue sends them asynchronously.
type failedNotifier struct{ failed chan string }

func (n *failedNotifier) NotifyJobSucceeded(context.Context, notification.JobSubject) error {
	return nil
}

func (n *failedNotifier) NotifyJobFailed(_ context.Context, _ notification.JobSubject, errMsg string) error {
	n.failed <- errMsg
	return nil
}

func (n *failedNotifier) NotifyAgentOffline(context.Context, uuid.UUID, string) error { return nil }

func (n *failedNotifier) NotifyAgentOnline(context.Context, uuid.UUID, string) error { return nil }

// fakeStarter claims the job's queued destinations through admit and records
// what it started. sendErr simulates a dispatch failure after admission.
type fakeStarter struct {
	t       *testing.T
	jobs    repositories.JobRepository
	dests   repositories.DestinationRepository
	sendErr error
	started []uuid.UUID
}

func (f *fakeStarter) StartQueued(ctx context.Context, job *db.Job, admit AdmitFunc) error {
	ids, err := f.jobs.ListQueuedDestinationIDs(ctx, job.ID)
	if err != nil {
		return err
	}
	ok, err := admit(ctx, ids)
	if err != nil {
		return err
	}
	if !ok {
		return ErrNotAdmitted
	}
	if f.sendErr != nil {
		for _, id := range ids {
			if err := f.dests.ReleaseBusy(ctx, id, job.ID); err != nil {
				f.t.Fatalf("ReleaseBusy: %v", err)
			}
		}
		return f.sendErr
	}
	f.started = append(f.started, job.ID)
	return nil
}

type env struct {
	t        *testing.T
	gdb      *gorm.DB
	jobs     repositories.JobRepository
	dests    repositories.DestinationRepository
	settings repositories.SettingsRepository
}

func newEnv(t *testing.T) *env {
	t.Helper()
	if err := db.InitEncryption(bytes.Repeat([]byte("k"), 32)); err != nil {
		t.Fatalf("InitEncryption: %v", err)
	}
	gdb, err := db.New(db.Config{Driver: "sqlite", DSN: ":memory:", Logger: zap.NewNop(), LogLevel: gormlogger.Silent})
	if err != nil {
		t.Fatalf("db.New: %v", err)
	}
	return &env{
		t:        t,
		gdb:      gdb,
		jobs:     repositories.NewJobRepository(gdb),
		dests:    repositories.NewDestinationRepository(gdb),
		settings: repositories.NewSettingsRepository(gdb),
	}
}

func (e *env) agent(name string) *db.Agent {
	e.t.Helper()
	a := &db.Agent{Name: name, Status: "online", Labels: "{}"}
	if err := repositories.NewAgentRepository(e.gdb).Create(context.Background(), a); err != nil {
		e.t.Fatalf("create agent: %v", err)
	}
	return a
}

func (e *env) dest(name string) *db.Destination {
	e.t.Helper()
	d := &db.Destination{Name: name, Type: "local", Config: "{}", Enabled: true}
	if err := e.dests.Create(context.Background(), d); err != nil {
		e.t.Fatalf("create destination: %v", err)
	}
	return d
}

// waitingJob creates a retention-typed waiting job over dests (retention jobs
// need no policy, which keeps the fixture small).
func (e *env) waitingJob(agent *db.Agent, dests ...*db.Destination) *db.Job {
	e.t.Helper()
	ctx := context.Background()
	j := &db.Job{AgentID: agent.ID, Type: "retention", Status: "pending"}
	if err := e.jobs.Create(ctx, j); err != nil {
		e.t.Fatalf("create job: %v", err)
	}
	for _, d := range dests {
		if err := e.jobs.CreateDestination(ctx, &db.JobDestination{JobID: j.ID, DestinationID: d.ID, Status: "pending"}); err != nil {
			e.t.Fatalf("create job destination: %v", err)
		}
	}
	if err := e.jobs.UpdateStatus(ctx, j.ID, "waiting", nil, nil, ""); err != nil {
		e.t.Fatalf("queue job: %v", err)
	}
	time.Sleep(2 * time.Millisecond) // distinct created_at for the FIFO order
	return j
}

func (e *env) status(j *db.Job) string {
	e.t.Helper()
	got, err := e.jobs.GetByID(context.Background(), j.ID)
	if err != nil {
		e.t.Fatalf("GetByID: %v", err)
	}
	return got.Status
}

func (e *env) queue(agents agentSet, starter Starter) *Dispatcher {
	d := New(e.jobs, e.dests, e.settings, agents, zap.NewNop())
	d.RegisterStarter("retention", starter)
	return d
}

// An older job whose agent is offline must not hold up a younger job on the
// same destination: it cannot run anyway.
func TestDrain_OfflineAgentDoesNotReserve(t *testing.T) {
	e := newEnv(t)
	offline := e.agent("offline")
	online := e.agent("online")
	d := e.dest("d")
	older := e.waitingJob(offline, d)
	younger := e.waitingJob(online, d)

	starter := &fakeStarter{t: t, jobs: e.jobs, dests: e.dests}
	e.queue(agentSet{online.ID.String(): true}, starter).Drain(context.Background())

	if got := e.status(older); got != "waiting" {
		t.Errorf("offline agent's job status = %q, want \"waiting\"", got)
	}
	if got := e.status(younger); got != "pending" {
		t.Errorf("online agent's job status = %q, want \"pending\"", got)
	}
	if len(starter.started) != 1 || starter.started[0] != younger.ID {
		t.Errorf("started = %v, want only %s", starter.started, younger.ID)
	}
}

// A dispatch that fails after admission puts the job back in the queue with
// its gates released, so it is retried on the next drain.
func TestDrain_DispatchFailureRequeues(t *testing.T) {
	e := newEnv(t)
	agent := e.agent("a")
	d := e.dest("d")
	job := e.waitingJob(agent, d)

	starter := &fakeStarter{t: t, jobs: e.jobs, dests: e.dests, sendErr: errors.New("stream closed")}
	q := e.queue(agentSet{agent.ID.String(): true}, starter)
	q.Drain(context.Background())

	if got := e.status(job); got != "waiting" {
		t.Fatalf("job status after failed dispatch = %q, want \"waiting\"", got)
	}
	got, err := e.dests.GetByID(context.Background(), d.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if got.BusyJobID != nil {
		t.Errorf("BusyJobID = %v, want nil after a failed dispatch", got.BusyJobID)
	}

	starter.sendErr = nil
	q.Drain(context.Background())
	if got := e.status(job); got != "pending" {
		t.Errorf("job status on retry = %q, want \"pending\"", got)
	}
}

// A timeout of 0 disables it: a job may wait indefinitely.
func TestDrain_TimeoutDisabled(t *testing.T) {
	e := newEnv(t)
	agent := e.agent("a")
	d := e.dest("d")
	ctx := context.Background()

	holder := &db.Job{AgentID: agent.ID, Type: "backup", Status: "running"}
	if err := e.jobs.Create(ctx, holder); err != nil {
		t.Fatalf("create holder: %v", err)
	}
	if ok, err := e.dests.TryAcquireBusy(ctx, d.ID, holder.ID); err != nil || !ok {
		t.Fatalf("TryAcquireBusy: %v %v", ok, err)
	}
	job := e.waitingJob(agent, d)
	if err := e.gdb.Model(&db.Job{}).Where("id = ?", job.ID).UpdateColumn("updated_at", time.Now().UTC().AddDate(0, -1, 0)).Error; err != nil {
		t.Fatalf("backdate job: %v", err)
	}
	if err := e.settings.Set(ctx, KeyTimeoutMinutes, db.EncryptedString("0")); err != nil {
		t.Fatalf("set timeout: %v", err)
	}

	e.queue(agentSet{agent.ID.String(): true}, &fakeStarter{t: t, jobs: e.jobs, dests: e.dests}).Drain(ctx)

	if got := e.status(job); got != "waiting" {
		t.Errorf("job status = %q, want \"waiting\" with the timeout disabled", got)
	}
}

func TestTimeoutMinutes(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	if got := TimeoutMinutes(ctx, e.settings); got != DefaultTimeoutMinutes {
		t.Errorf("TimeoutMinutes unset = %d, want %d", got, DefaultTimeoutMinutes)
	}
	for value, want := range map[string]int{"90": 90, "0": 0, "-5": DefaultTimeoutMinutes, "abc": DefaultTimeoutMinutes} {
		if err := e.settings.Set(ctx, KeyTimeoutMinutes, db.EncryptedString(value)); err != nil {
			t.Fatalf("set: %v", err)
		}
		if got := TimeoutMinutes(ctx, e.settings); got != want {
			t.Errorf("TimeoutMinutes(%q) = %d, want %d", value, got, want)
		}
	}
}

func TestNotify_NeverBlocks(t *testing.T) {
	var nilQueue *Dispatcher
	nilQueue.Notify() // must not panic

	e := newEnv(t)
	q := e.queue(agentSet{}, &fakeStarter{t: t, jobs: e.jobs, dests: e.dests})
	for range 10 {
		q.Notify()
	}
	if len(q.wake) != 1 {
		t.Errorf("pending wake-ups = %d, want 1 (notifications collapse)", len(q.wake))
	}
}

// A job queued while it still holds gates from an earlier dispatch (a pending
// job re-sent to a reconnected agent) must let go of them: a waiting job never
// holds part of what it needs.
func TestEnqueue_ReleasesHeldGates(t *testing.T) {
	e := newEnv(t)
	agent := e.agent("a")
	d := e.dest("d")
	ctx := context.Background()

	job := &db.Job{AgentID: agent.ID, Type: "backup", Status: "pending"}
	if err := e.jobs.Create(ctx, job); err != nil {
		t.Fatalf("create job: %v", err)
	}
	if ok, err := e.dests.TryAcquireBusy(ctx, d.ID, job.ID); err != nil || !ok {
		t.Fatalf("TryAcquireBusy: %v %v", ok, err)
	}

	q := e.queue(agentSet{}, &fakeStarter{t: t, jobs: e.jobs, dests: e.dests})
	if err := q.Enqueue(ctx, job.ID); err != nil {
		t.Fatalf("Enqueue: %v", err)
	}

	if got := e.status(job); got != "waiting" {
		t.Errorf("job status = %q, want \"waiting\"", got)
	}
	got, err := e.dests.GetByID(ctx, d.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if got.BusyJobID != nil {
		t.Errorf("BusyJobID = %v, want nil once the job is queued", got.BusyJobID)
	}
}

// pendingJob creates a pending job of agent over d, last updated at updatedAt.
func (e *env) pendingJob(agent *db.Agent, status string, d *db.Destination, updatedAt time.Time) *db.Job {
	e.t.Helper()
	ctx := context.Background()
	j := &db.Job{AgentID: agent.ID, Type: "retention", Status: status}
	if err := e.jobs.Create(ctx, j); err != nil {
		e.t.Fatalf("create job: %v", err)
	}
	if err := e.jobs.CreateDestination(ctx, &db.JobDestination{JobID: j.ID, DestinationID: d.ID, Status: "pending"}); err != nil {
		e.t.Fatalf("create job destination: %v", err)
	}
	if err := e.gdb.Model(&db.Job{}).Where("id = ?", j.ID).UpdateColumn("updated_at", updatedAt).Error; err != nil {
		e.t.Fatalf("backdate job: %v", err)
	}
	return j
}

// Issue #325: a job still pending after the queue timeout — its agent was
// offline, or never started it — is failed and reported, the agent told to
// drop it, and its destination released. Drain does this with no job waiting.
func TestDrain_StalePendingFails(t *testing.T) {
	e := newEnv(t)
	online := e.agent("online")
	offline := e.agent("offline")
	d := e.dest("d")
	ctx := context.Background()
	old := time.Now().UTC().Add(-25 * time.Hour)

	sent := e.pendingJob(online, "pending", d, old)
	if ok, err := e.dests.TryAcquireBusyAll(ctx, []uuid.UUID{d.ID}, sent.ID); err != nil || !ok {
		t.Fatalf("TryAcquireBusyAll: %v %v", ok, err)
	}
	unsent := e.pendingJob(offline, "pending", e.dest("d2"), old)
	recent := e.pendingJob(online, "pending", e.dest("d3"), time.Now().UTC().Add(-time.Hour))
	running := e.pendingJob(online, "running", e.dest("d4"), old)

	agents := &cancellingAgents{agentSet: agentSet{online.ID.String(): true}}
	notifier := &failedNotifier{failed: make(chan string, 4)}
	q := New(e.jobs, e.dests, e.settings, agents, zap.NewNop())
	q.SetNotificationService(notifier)
	q.Drain(ctx)

	for _, j := range []*db.Job{sent, unsent} {
		got, err := e.jobs.GetByID(ctx, j.ID)
		if err != nil {
			t.Fatalf("GetByID: %v", err)
		}
		if got.Status != "failed" || got.Error != PendingTimeoutError || got.EndedAt == nil {
			t.Errorf("stale pending job = %s %q (ended %v), want failed %q with an end time", got.Status, got.Error, got.EndedAt, PendingTimeoutError)
		}
	}
	for _, j := range []*db.Job{recent} {
		if got := e.status(j); got != "pending" {
			t.Errorf("recent pending job status = %q, want \"pending\"", got)
		}
	}
	if got := e.status(running); got != "running" {
		t.Errorf("running job status = %q, want \"running\"", got)
	}
	if !slices.Equal(agents.cancelled, []string{sent.ID.String()}) {
		t.Errorf("cancels sent = %v, want only the connected agent's job %s", agents.cancelled, sent.ID)
	}
	if got, err := e.dests.GetByID(ctx, d.ID); err != nil || got.BusyJobID != nil {
		t.Errorf("destination holder = %v (%v), want released", got.BusyJobID, err)
	}
	for range 2 {
		select {
		case msg := <-notifier.failed:
			if msg != PendingTimeoutError {
				t.Errorf("notified error = %q, want %q", msg, PendingTimeoutError)
			}
		case <-time.After(3 * time.Second):
			t.Fatal("job failed notification not sent")
		}
	}
}

// A timeout of 0 lets a pending job wait for its agent indefinitely.
func TestDrain_PendingTimeoutDisabled(t *testing.T) {
	e := newEnv(t)
	agent := e.agent("a")
	ctx := context.Background()
	job := e.pendingJob(agent, "pending", e.dest("d"), time.Now().UTC().AddDate(0, -1, 0))
	if err := e.settings.Set(ctx, KeyTimeoutMinutes, db.EncryptedString("0")); err != nil {
		t.Fatalf("set timeout: %v", err)
	}

	e.queue(agentSet{}, &fakeStarter{t: t, jobs: e.jobs, dests: e.dests}).Drain(ctx)

	if got := e.status(job); got != "pending" {
		t.Errorf("job status = %q, want \"pending\" with the timeout disabled", got)
	}
}
