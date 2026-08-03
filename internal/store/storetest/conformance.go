// Package storetest is the conformance suite every store backend must pass.
// Backends call Run with a factory producing a fresh, migrated, empty store.
package storetest

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/blissfulrays/pushq/internal/store"
)

type Factory func(t *testing.T) store.Store

var t0 = time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)

func upsert(name string) store.QueueUpsert {
	return store.QueueUpsert{
		Name:       name,
		RateLimits: store.DefaultRateLimits(),
		Retry:      store.DefaultRetryConfig(),
		KeepSecret: true,
	}
}

func newTask(queue, id string, sched time.Time) store.NewTask {
	return store.NewTask{
		Queue: queue, ID: id, URL: "https://example.com/hook", Method: "POST",
		DispatchDeadline: 10 * time.Minute, ScheduleTime: sched,
	}
}

func mustQueue(t *testing.T, s store.Store, name string) store.Queue {
	t.Helper()
	q, _, err := s.UpsertQueue(context.Background(), upsert(name), t0)
	if err != nil {
		t.Fatalf("upsert queue %s: %v", name, err)
	}
	return q
}

func mustTask(t *testing.T, s store.Store, nt store.NewTask, now time.Time) store.Task {
	t.Helper()
	task, err := s.CreateTask(context.Background(), nt, now)
	if err != nil {
		t.Fatalf("create task %s/%s: %v", nt.Queue, nt.ID, err)
	}
	return task
}

func claimAll(t *testing.T, s store.Store, q store.Queue, n int, now time.Time, token string) []store.Task {
	t.Helper()
	tasks, err := s.ClaimTasks(context.Background(), now, token, time.Minute,
		[]store.ClaimBudget{{QueueID: q.ID, N: n, PurgedAt: q.PurgedAt}})
	if err != nil {
		t.Fatalf("claim: %v", err)
	}
	return tasks
}

func Run(t *testing.T, factory Factory) {
	ctx := context.Background()

	t.Run("QueueUpsertLifecycle", func(t *testing.T) {
		s := factory(t)
		u := upsert("q1")
		u.RateLimits.MaxDispatchesPerSecond = 10

		q, outcome, err := s.UpsertQueue(ctx, u, t0)
		if err != nil || outcome != store.UpsertCreated {
			t.Fatalf("first upsert: outcome=%v err=%v", outcome, err)
		}
		if q.State != store.QueueRunning || q.RateLimits.MaxDispatchesPerSecond != 10 {
			t.Fatalf("bad created queue: %+v", q)
		}

		// Same config → noop, no version bump.
		q2, outcome, err := s.UpsertQueue(ctx, u, t0.Add(time.Minute))
		if err != nil || outcome != store.UpsertNoop {
			t.Fatalf("noop upsert: outcome=%v err=%v", outcome, err)
		}
		if q2.ConfigVersion != q.ConfigVersion || !q2.UpdatedAt.Equal(q.UpdatedAt) {
			t.Fatalf("noop must not touch the row: v%d→v%d", q.ConfigVersion, q2.ConfigVersion)
		}

		// Changed config → updated + version bump.
		u.RateLimits.MaxDispatchesPerSecond = 20
		q3, outcome, err := s.UpsertQueue(ctx, u, t0.Add(2*time.Minute))
		if err != nil || outcome != store.UpsertUpdated {
			t.Fatalf("update upsert: outcome=%v err=%v", outcome, err)
		}
		if q3.ConfigVersion != q.ConfigVersion+1 {
			t.Fatalf("version not bumped: %d", q3.ConfigVersion)
		}

		// Upsert must never resume a paused queue.
		if _, err := s.SetQueueState(ctx, "q1", store.QueuePaused, t0.Add(3*time.Minute)); err != nil {
			t.Fatal(err)
		}
		u.RateLimits.MaxDispatchesPerSecond = 30
		q4, _, err := s.UpsertQueue(ctx, u, t0.Add(4*time.Minute))
		if err != nil {
			t.Fatal(err)
		}
		if q4.State != store.QueuePaused {
			t.Fatalf("upsert flipped state to %s", q4.State)
		}
	})

	t.Run("SecretKeepSemantics", func(t *testing.T) {
		s := factory(t)
		u := upsert("q1")
		u.KeepSecret = false
		u.Target.SigningSecret = "whsec_c2VjcmV0"
		if _, _, err := s.UpsertQueue(ctx, u, t0); err != nil {
			t.Fatal(err)
		}

		// KeepSecret upsert leaves it intact and counts as noop.
		u2 := upsert("q1")
		q, outcome, err := s.UpsertQueue(ctx, u2, t0.Add(time.Minute))
		if err != nil {
			t.Fatal(err)
		}
		if outcome != store.UpsertNoop || q.Target.SigningSecret != "whsec_c2VjcmV0" {
			t.Fatalf("secret not kept: outcome=%v secret=%q", outcome, q.Target.SigningSecret)
		}

		// Explicit empty secret clears it.
		u3 := upsert("q1")
		u3.KeepSecret = false
		q, _, err = s.UpsertQueue(ctx, u3, t0.Add(2*time.Minute))
		if err != nil {
			t.Fatal(err)
		}
		if q.Target.SigningSecret != "" {
			t.Fatalf("secret not cleared: %q", q.Target.SigningSecret)
		}
	})

	t.Run("CreateQueueConflictAndDelete", func(t *testing.T) {
		s := factory(t)
		if _, err := s.CreateQueue(ctx, upsert("q1"), t0); err != nil {
			t.Fatal(err)
		}
		if _, err := s.CreateQueue(ctx, upsert("q1"), t0); !errors.Is(err, store.ErrAlreadyExists) {
			t.Fatalf("want ErrAlreadyExists, got %v", err)
		}
		if err := s.DeleteQueue(ctx, "q1"); err != nil {
			t.Fatal(err)
		}
		if err := s.DeleteQueue(ctx, "q1"); !errors.Is(err, store.ErrNotFound) {
			t.Fatalf("want ErrNotFound, got %v", err)
		}
		// Name reusable immediately.
		if _, err := s.CreateQueue(ctx, upsert("q1"), t0); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("ListQueuesPrefixAndPagination", func(t *testing.T) {
		s := factory(t)
		for _, n := range []string{"account-1", "account-2", "account-3", "other"} {
			mustQueue(t, s, n)
		}
		qs, next, err := s.ListQueues(ctx, store.ListQueuesOpts{NamePrefix: "account-", PageSize: 2})
		if err != nil || len(qs) != 2 || next == "" {
			t.Fatalf("page1: n=%d next=%q err=%v", len(qs), next, err)
		}
		qs2, next2, err := s.ListQueues(ctx, store.ListQueuesOpts{NamePrefix: "account-", PageSize: 2, PageToken: next})
		if err != nil || len(qs2) != 1 || next2 != "" {
			t.Fatalf("page2: n=%d next=%q err=%v", len(qs2), next2, err)
		}
		if qs[0].Name != "account-1" || qs[1].Name != "account-2" || qs2[0].Name != "account-3" {
			t.Fatalf("wrong order: %s %s %s", qs[0].Name, qs[1].Name, qs2[0].Name)
		}
	})

	t.Run("TaskDedup", func(t *testing.T) {
		s := factory(t)
		mustQueue(t, s, "q1")
		mustTask(t, s, newTask("q1", "job-1", t0), t0)
		if _, err := s.CreateTask(ctx, newTask("q1", "job-1", t0), t0); !errors.Is(err, store.ErrAlreadyExists) {
			t.Fatalf("want ErrAlreadyExists, got %v", err)
		}
		// Deleted task tombstones the ID: still conflicts.
		if err := s.DeleteTask(ctx, "q1", "job-1", t0.Add(time.Second)); err != nil {
			t.Fatal(err)
		}
		if _, err := s.GetTask(ctx, "q1", "job-1", store.ViewBasic); !errors.Is(err, store.ErrNotFound) {
			t.Fatalf("deleted task should 404, got %v", err)
		}
		if _, err := s.CreateTask(ctx, newTask("q1", "job-1", t0), t0.Add(2*time.Second)); !errors.Is(err, store.ErrAlreadyExists) {
			t.Fatalf("tombstone should conflict, got %v", err)
		}
		// After the dedup window, cleanup frees the ID.
		n, err := s.CleanupTerminal(ctx, t0.Add(2*time.Hour), 24*time.Hour, 24*time.Hour, time.Hour, 100)
		if err != nil || n != 1 {
			t.Fatalf("cleanup: n=%d err=%v", n, err)
		}
		if _, err := s.CreateTask(ctx, newTask("q1", "job-1", t0), t0.Add(2*time.Hour)); err != nil {
			t.Fatalf("ID should be reusable after cleanup: %v", err)
		}
	})

	t.Run("DisabledQueueRejectsCreates", func(t *testing.T) {
		s := factory(t)
		mustQueue(t, s, "q1")
		disabled := store.QueueDisabled
		if _, err := s.PatchQueue(ctx, "q1", store.QueuePatch{State: &disabled}, t0); err != nil {
			t.Fatal(err)
		}
		if _, err := s.CreateTask(ctx, newTask("q1", "x", t0), t0); !errors.Is(err, store.ErrFailedPrecondition) {
			t.Fatalf("want ErrFailedPrecondition, got %v", err)
		}
	})

	t.Run("ClaimOrderingAndBudget", func(t *testing.T) {
		s := factory(t)
		q := mustQueue(t, s, "q1")
		mustTask(t, s, newTask("q1", "c", t0.Add(3*time.Second)), t0)
		mustTask(t, s, newTask("q1", "a", t0.Add(1*time.Second)), t0)
		mustTask(t, s, newTask("q1", "b", t0.Add(2*time.Second)), t0)
		mustTask(t, s, newTask("q1", "future", t0.Add(time.Hour)), t0)

		now := t0.Add(10 * time.Second)
		tasks := claimAll(t, s, q, 2, now, "tok-1")
		if len(tasks) != 2 || tasks[0].ID != "a" || tasks[1].ID != "b" {
			ids := []string{}
			for _, x := range tasks {
				ids = append(ids, x.ID)
			}
			t.Fatalf("want [a b], got %v", ids)
		}
		if tasks[0].DispatchCount != 1 || tasks[0].State != store.TaskLeased || tasks[0].FirstAttemptAt == nil {
			t.Fatalf("claim bookkeeping wrong: %+v", tasks[0])
		}

		// Second claim gets only "c" (a, b leased; future not due).
		tasks = claimAll(t, s, q, 10, now, "tok-2")
		if len(tasks) != 1 || tasks[0].ID != "c" {
			t.Fatalf("want [c], got %d tasks", len(tasks))
		}

		next, err := s.NextPending(ctx, q.ID, now)
		if err != nil || next == nil || !next.Equal(t0.Add(time.Hour)) {
			t.Fatalf("NextPending = %v, %v", next, err)
		}
	})

	t.Run("FinishAttemptFencing", func(t *testing.T) {
		s := factory(t)
		q := mustQueue(t, s, "q1")
		task := mustTask(t, s, newTask("q1", "job", t0), t0)

		now := t0.Add(time.Second)
		claimed := claimAll(t, s, q, 1, now, "tok-1")
		if len(claimed) != 1 {
			t.Fatal("no claim")
		}
		att := store.Attempt{TaskPK: task.PK, QueueID: q.ID, Number: 1, DispatchTime: now, ResponseStatus: 500, ErrorKind: "non_2xx"}

		// Wrong token is a no-op.
		ok, err := s.FinishAttempt(ctx, now, task.PK, "stale-token", att, store.OutcomeSucceeded, time.Time{})
		if err != nil || ok {
			t.Fatalf("stale token must fence: ok=%v err=%v", ok, err)
		}

		// Right token retries the task.
		retryAt := now.Add(30 * time.Second)
		ok, err = s.FinishAttempt(ctx, now, task.PK, "tok-1", att, store.OutcomeRetry, retryAt)
		if err != nil || !ok {
			t.Fatalf("finish: ok=%v err=%v", ok, err)
		}
		got, err := s.GetTask(ctx, "q1", "job", store.ViewFull)
		if err != nil {
			t.Fatal(err)
		}
		if got.State != store.TaskPending || !got.ScheduleTime.Equal(retryAt) || got.ResponseCount != 1 || got.LastStatus != 500 {
			t.Fatalf("retry state wrong: %+v", got)
		}

		// Double-finish with the old token is fenced (lease cleared).
		ok, err = s.FinishAttempt(ctx, now, task.PK, "tok-1", att, store.OutcomeSucceeded, time.Time{})
		if err != nil || ok {
			t.Fatalf("second finish must fence: ok=%v", ok)
		}

		attempts, err := s.ListAttempts(ctx, "q1", "job")
		if err != nil || len(attempts) != 1 || attempts[0].ResponseStatus != 500 {
			t.Fatalf("attempts: %v err=%v", attempts, err)
		}
	})

	t.Run("SucceededTerminalState", func(t *testing.T) {
		s := factory(t)
		q := mustQueue(t, s, "q1")
		task := mustTask(t, s, newTask("q1", "job", t0), t0)
		now := t0.Add(time.Second)
		claimAll(t, s, q, 1, now, "tok")
		rt := now.Add(50 * time.Millisecond)
		att := store.Attempt{TaskPK: task.PK, QueueID: q.ID, Number: 1, DispatchTime: now, ResponseTime: &rt, ResponseStatus: 200}
		if ok, err := s.FinishAttempt(ctx, now, task.PK, "tok", att, store.OutcomeSucceeded, time.Time{}); err != nil || !ok {
			t.Fatalf("finish: %v %v", ok, err)
		}
		got, _ := s.GetTask(ctx, "q1", "job", store.ViewBasic)
		if got.State != store.TaskSucceeded || got.FinishedAt == nil || got.ResponseCount != 1 {
			t.Fatalf("succeeded state wrong: %+v", got)
		}
	})

	t.Run("PurgeWatermark", func(t *testing.T) {
		s := factory(t)
		mustQueue(t, s, "q1")
		mustTask(t, s, newTask("q1", "old", t0), t0)

		purgeAt := t0.Add(time.Minute)
		q, err := s.PurgeQueue(ctx, "q1", purgeAt)
		if err != nil || q.PurgedAt == nil {
			t.Fatalf("purge: %v", err)
		}

		// Purged task invisible and unclaimable.
		if _, err := s.GetTask(ctx, "q1", "old", store.ViewBasic); !errors.Is(err, store.ErrNotFound) {
			t.Fatalf("purged task should 404, got %v", err)
		}
		if tasks := claimAll(t, s, q, 10, purgeAt.Add(time.Second), "tok"); len(tasks) != 0 {
			t.Fatalf("claimed purged tasks: %d", len(tasks))
		}

		// Its ID is immediately reusable (insert deletes the doomed row).
		if _, err := s.CreateTask(ctx, newTask("q1", "old", purgeAt.Add(time.Second)), purgeAt.Add(time.Second)); err != nil {
			t.Fatalf("purged ID should be reusable: %v", err)
		}

		// Tasks created after the watermark are claimable.
		q2, _ := s.GetQueue(ctx, "q1")
		tasks := claimAll(t, s, q2, 10, purgeAt.Add(2*time.Second), "tok2")
		if len(tasks) != 1 || tasks[0].ID != "old" {
			t.Fatalf("post-purge claim: %d", len(tasks))
		}

		// Hard delete drops nothing extra (row already replaced) but must not error.
		if _, err := s.HardDeletePurged(ctx, 100); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("PurgeDiscardsInflightResult", func(t *testing.T) {
		s := factory(t)
		q := mustQueue(t, s, "q1")
		task := mustTask(t, s, newTask("q1", "job", t0), t0)
		now := t0.Add(time.Second)
		claimAll(t, s, q, 1, now, "tok")
		if _, err := s.PurgeQueue(ctx, "q1", now.Add(time.Second)); err != nil {
			t.Fatal(err)
		}
		att := store.Attempt{TaskPK: task.PK, QueueID: q.ID, Number: 1, DispatchTime: now, ResponseStatus: 200}
		ok, err := s.FinishAttempt(ctx, now.Add(2*time.Second), task.PK, "tok", att, store.OutcomeSucceeded, time.Time{})
		if err != nil || ok {
			t.Fatalf("purge must fence in-flight completion: ok=%v err=%v", ok, err)
		}
	})

	t.Run("ForceRun", func(t *testing.T) {
		s := factory(t)
		q := mustQueue(t, s, "q1")
		mustTask(t, s, newTask("q1", "future", t0.Add(time.Hour)), t0)

		now := t0.Add(time.Minute)
		task, err := s.ForceRun(ctx, "q1", "future", now)
		if err != nil {
			t.Fatal(err)
		}
		if !task.ScheduleTime.Equal(now) {
			t.Fatalf("schedule not moved: %v", task.ScheduleTime)
		}
		got, claimed, err := s.ClaimTaskForRun(ctx, now, "tok", time.Minute, task.PK)
		if err != nil || !claimed || got.ID != "future" {
			t.Fatalf("claim for run: claimed=%v err=%v", claimed, err)
		}
		// Leased task cannot be re-run.
		if _, err := s.ForceRun(ctx, "q1", "future", now); !errors.Is(err, store.ErrFailedPrecondition) {
			t.Fatalf("running task ForceRun: want failed_precondition, got %v", err)
		}
		// Unknown task 404s.
		if _, err := s.ForceRun(ctx, "q1", "nope", now); !errors.Is(err, store.ErrNotFound) {
			t.Fatalf("want ErrNotFound, got %v", err)
		}
		_ = q
	})

	t.Run("ExpiredLeases", func(t *testing.T) {
		s := factory(t)
		q := mustQueue(t, s, "q1")
		nt := newTask("q1", "job", t0)
		nt.DispatchDeadline = time.Minute
		mustTask(t, s, nt, t0)

		now := t0.Add(time.Second)
		claimed := claimAll(t, s, q, 1, now, "tok")
		if len(claimed) != 1 {
			t.Fatal("no claim")
		}
		// Lease = deadline + grace(1m) = 2m. Not expired yet at +1m59s.
		exp, err := s.ListExpiredLeases(ctx, now.Add(119*time.Second), 10)
		if err != nil || len(exp) != 0 {
			t.Fatalf("premature expiry: %d err=%v", len(exp), err)
		}
		exp, err = s.ListExpiredLeases(ctx, now.Add(121*time.Second), 10)
		if err != nil || len(exp) != 1 || exp[0].ID != "job" {
			t.Fatalf("expiry: %d err=%v", len(exp), err)
		}
	})

	t.Run("ListTasksFilterAndPagination", func(t *testing.T) {
		s := factory(t)
		q := mustQueue(t, s, "q1")
		for _, id := range []string{"follow_up_1", "follow_up_2", "dm_1"} {
			mustTask(t, s, newTask("q1", id, t0), t0)
		}
		claimAll(t, s, q, 1, t0.Add(time.Second), "tok") // leases follow_up_1

		tasks, _, err := s.ListTasks(ctx, store.ListTasksOpts{Queue: "q1", State: store.TaskLeased})
		if err != nil || len(tasks) != 1 {
			t.Fatalf("leased filter: %d err=%v", len(tasks), err)
		}
		tasks, _, err = s.ListTasks(ctx, store.ListTasksOpts{Queue: "q1", IDPrefix: "follow_up_"})
		if err != nil || len(tasks) != 2 {
			t.Fatalf("prefix filter: %d err=%v", len(tasks), err)
		}
		// Pagination newest-first.
		page1, next, err := s.ListTasks(ctx, store.ListTasksOpts{Queue: "q1", PageSize: 2})
		if err != nil || len(page1) != 2 || next == "" {
			t.Fatalf("page1: %d next=%q err=%v", len(page1), next, err)
		}
		page2, next2, err := s.ListTasks(ctx, store.ListTasksOpts{Queue: "q1", PageSize: 2, PageToken: next})
		if err != nil || len(page2) != 1 || next2 != "" {
			t.Fatalf("page2: %d err=%v", len(page2), err)
		}
		if _, _, err := s.ListTasks(ctx, store.ListTasksOpts{Queue: "missing"}); !errors.Is(err, store.ErrNotFound) {
			t.Fatalf("unknown queue should 404, got %v", err)
		}
	})

	t.Run("QueueStats", func(t *testing.T) {
		s := factory(t)
		q := mustQueue(t, s, "q1")
		mustTask(t, s, newTask("q1", "due-1", t0), t0)
		mustTask(t, s, newTask("q1", "due-2", t0), t0)
		mustTask(t, s, newTask("q1", "future", t0.Add(time.Hour)), t0)
		mustTask(t, s, newTask("q1", "follow_up_x", t0), t0)

		now := t0.Add(time.Second)
		claimed := claimAll(t, s, q, 1, now, "tok")
		rt := now.Add(time.Millisecond)
		att := store.Attempt{TaskPK: claimed[0].PK, QueueID: q.ID, Number: 1, DispatchTime: now, ResponseTime: &rt, ResponseStatus: 500, ErrorKind: "non_2xx"}
		if ok, _ := s.FinishAttempt(ctx, now, claimed[0].PK, "tok", att, store.OutcomeFailed, time.Time{}); !ok {
			t.Fatal("finish failed")
		}

		st, err := s.QueueStats(ctx, "q1", "", now.Add(time.Second))
		if err != nil {
			t.Fatal(err)
		}
		if st.Pending != 2 || st.Failed != 1 || st.ScheduledFuture != 1 || st.Running != 0 {
			t.Fatalf("stats: %+v", st)
		}
		if st.Dispatched1m != 1 || st.Errors1m != 1 || st.Dispatched1h != 1 || st.Errors1h != 1 {
			t.Fatalf("attempt windows: %+v", st)
		}
		if st.OldestDueAt == nil {
			t.Fatal("missing oldest due")
		}

		// id_prefix scoping.
		st, err = s.QueueStats(ctx, "q1", "follow_up_", now.Add(time.Second))
		if err != nil || st.Pending != 1 || st.Failed != 0 {
			t.Fatalf("prefix stats: %+v err=%v", st, err)
		}
	})

	t.Run("SeedDispatch", func(t *testing.T) {
		s := factory(t)
		q1 := mustQueue(t, s, "q1")
		mustQueue(t, s, "q2") // empty queue must not appear
		mustTask(t, s, newTask("q1", "a", t0), t0)
		mustTask(t, s, newTask("q1", "b", t0.Add(time.Hour)), t0)

		seeds, err := s.SeedDispatch(ctx)
		if err != nil || len(seeds) != 1 {
			t.Fatalf("seeds: %d err=%v", len(seeds), err)
		}
		if seeds[0].QueueID != q1.ID || seeds[0].Pending != 2 || seeds[0].NextSchedule == nil || !seeds[0].NextSchedule.Equal(t0) {
			t.Fatalf("seed: %+v", seeds[0])
		}
	})

	t.Run("BatchCreatePartialConflicts", func(t *testing.T) {
		s := factory(t)
		mustQueue(t, s, "q1")
		mustTask(t, s, newTask("q1", "dup", t0), t0)

		res, err := s.CreateTasks(ctx, []store.NewTask{
			newTask("q1", "fresh-1", t0),
			newTask("q1", "dup", t0),
			newTask("missing-queue", "x", t0),
			newTask("q1", "fresh-2", t0),
		}, t0)
		if err != nil {
			t.Fatal(err)
		}
		if res[0].Err != nil || res[3].Err != nil {
			t.Fatalf("fresh tasks failed: %v %v", res[0].Err, res[3].Err)
		}
		if !errors.Is(res[1].Err, store.ErrAlreadyExists) {
			t.Fatalf("dup: want ErrAlreadyExists, got %v", res[1].Err)
		}
		if !errors.Is(res[2].Err, store.ErrNotFound) {
			t.Fatalf("missing queue: want ErrNotFound, got %v", res[2].Err)
		}
	})

	t.Run("RetryOverrideRoundTrip", func(t *testing.T) {
		s := factory(t)
		mustQueue(t, s, "q1")
		three := 3
		nt := newTask("q1", "job", t0)
		nt.RetryOverride = &store.RetryOverride{MaxAttempts: &three}
		mustTask(t, s, nt, t0)
		got, err := s.GetTask(ctx, "q1", "job", store.ViewFull)
		if err != nil || got.RetryOverride == nil || got.RetryOverride.MaxAttempts == nil || *got.RetryOverride.MaxAttempts != 3 {
			t.Fatalf("override lost: %+v err=%v", got.RetryOverride, err)
		}
	})

	t.Run("EventsEmitted", func(t *testing.T) {
		s := factory(t)
		mustQueue(t, s, "q1") // EventQueueChanged
		mustTask(t, s, newTask("q1", "a", t0), t0)

		var kinds []store.EventKind
		for i := 0; i < 2; i++ {
			select {
			case ev := <-s.Events():
				kinds = append(kinds, ev.Kind)
			case <-time.After(2 * time.Second):
				t.Fatalf("missing event %d (got %v)", i, kinds)
			}
		}
		if kinds[0] != store.EventQueueChanged || kinds[1] != store.EventTaskReady {
			t.Fatalf("wrong events: %v", kinds)
		}
	})
}
