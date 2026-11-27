package sqlite

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/blissfulrays/pushq/internal/store"
)

// notPurged keeps rows created after the queue's purge watermark (purge frees
// every row at or before it, including tombstones, so task IDs become reusable).
const notPurged = `(q.purged_at IS NULL OR t.created_at > q.purged_at)`

func taskCols(view store.View) string {
	headers, body := "t.headers", "t.body"
	if view == store.ViewBasic {
		headers, body = "NULL", "NULL"
	}
	return `t.id, t.queue_id, q.name, t.task_id, t.state, t.url, t.method, ` + headers + `, ` + body + `,
t.dispatch_deadline_ms, t.retry_override, t.schedule_time, t.created_at, t.first_attempt_at, t.finished_at,
t.dispatch_count, t.response_count, t.lease_token, t.lease_expires_at, t.last_attempt_at, t.last_status, t.last_error`
}

func scanTask(r rowScanner) (store.Task, error) {
	var t store.Task
	var headers, retryOverride, leaseToken, lastError sql.NullString
	var body []byte
	var deadlineMS, schedMS, createdMS int64
	var firstAt, finishedAt, leaseExp, lastAt, lastStatus sql.NullInt64
	err := r.Scan(&t.PK, &t.QueueID, &t.Queue, &t.ID, &t.State, &t.URL, &t.Method, &headers, &body,
		&deadlineMS, &retryOverride, &schedMS, &createdMS, &firstAt, &finishedAt,
		&t.DispatchCount, &t.ResponseCount, &leaseToken, &leaseExp, &lastAt, &lastStatus, &lastError)
	if err != nil {
		return t, err
	}
	if headers.Valid && headers.String != "" {
		if err := json.Unmarshal([]byte(headers.String), &t.Headers); err != nil {
			return t, fmt.Errorf("task %s: bad headers json: %w", t.ID, err)
		}
	}
	t.Body = body
	t.DispatchDeadline = time.Duration(deadlineMS) * time.Millisecond
	if retryOverride.Valid && retryOverride.String != "" {
		var ro store.RetryOverride
		if err := json.Unmarshal([]byte(retryOverride.String), &ro); err != nil {
			return t, fmt.Errorf("task %s: bad retry_override json: %w", t.ID, err)
		}
		t.RetryOverride = &ro
	}
	t.ScheduleTime = fromMS(schedMS)
	t.CreatedAt = fromMS(createdMS)
	t.FirstAttemptAt = scanNullableMS(firstAt)
	t.FinishedAt = scanNullableMS(finishedAt)
	t.LeaseToken = leaseToken.String
	t.LeaseExpiresAt = scanNullableMS(leaseExp)
	t.LastAttemptAt = scanNullableMS(lastAt)
	if lastStatus.Valid {
		t.LastStatus = int(lastStatus.Int64)
	}
	t.LastError = lastError.String
	return t, nil
}

type queueRef struct {
	id       int64
	state    store.QueueState
	purgedMS int64 // -1 when never purged
}

func getQueueRef(ctx context.Context, tx *sql.Tx, name string) (queueRef, error) {
	var ref queueRef
	var purged sql.NullInt64
	err := tx.QueryRowContext(ctx, `SELECT id, state, purged_at FROM queues WHERE name = ?`, name).
		Scan(&ref.id, &ref.state, &purged)
	if err == sql.ErrNoRows {
		return ref, fmt.Errorf("queue %q: %w", name, store.ErrNotFound)
	}
	if err != nil {
		return ref, err
	}
	ref.purgedMS = -1
	if purged.Valid {
		ref.purgedMS = purged.Int64
	}
	return ref, nil
}

// createdMS returns the creation time (unix ms) to record for a new task. It
// is always strictly after the queue's purge watermark, so a task created in
// the same millisecond as a purge is not hidden by it.
func (ref queueRef) createdMS(now time.Time) int64 {
	c := ms(now)
	if ref.purgedMS >= 0 && c <= ref.purgedMS {
		c = ref.purgedMS + 1
	}
	return c
}

func (s *Store) insertTask(ctx context.Context, tx *sql.Tx, ref queueRef, t store.NewTask, createdMS int64) (int64, error) {
	headers, err := jsonOrNil(t.Headers)
	if err != nil {
		return 0, err
	}
	override, err := jsonOrNil(t.RetryOverride)
	if err != nil {
		return 0, err
	}
	args := []any{ref.id, t.ID, t.URL, t.Method, headers, t.Body,
		t.DispatchDeadline.Milliseconds(), override, ms(t.ScheduleTime), createdMS}
	const q = `INSERT INTO tasks (queue_id, task_id, url, method, headers, body,
		dispatch_deadline_ms, retry_override, schedule_time, created_at) VALUES (?,?,?,?,?,?,?,?,?,?)`
	res, err := tx.ExecContext(ctx, q, args...)
	if isUniqueViolation(err) {
		// A row at or before the purge watermark is doomed; free the ID now
		// so purge is exact even before the hard-deleter catches up.
		var pk, existingMS int64
		serr := tx.QueryRowContext(ctx,
			`SELECT id, created_at FROM tasks WHERE queue_id=? AND task_id=?`, ref.id, t.ID).Scan(&pk, &existingMS)
		if serr != nil || ref.purgedMS < 0 || existingMS > ref.purgedMS {
			return 0, store.ErrAlreadyExists
		}
		if _, derr := tx.ExecContext(ctx, `DELETE FROM tasks WHERE id=?`, pk); derr != nil {
			return 0, derr
		}
		res, err = tx.ExecContext(ctx, q, args...)
	}
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func normalizeNewTask(t *store.NewTask) {
	if t.ID == "" {
		id, err := uuid.NewV7()
		if err != nil {
			id = uuid.New()
		}
		t.ID = id.String()
	}
	if t.Method == "" {
		t.Method = "POST"
	}
	if t.DispatchDeadline <= 0 {
		t.DispatchDeadline = 10 * time.Minute
	}
}

func (s *Store) CreateTask(ctx context.Context, t store.NewTask, now time.Time) (store.Task, error) {
	res, err := s.CreateTasks(ctx, []store.NewTask{t}, now)
	if err != nil {
		return store.Task{}, err
	}
	return res[0].Task, res[0].Err
}

func (s *Store) CreateTasks(ctx context.Context, ts []store.NewTask, now time.Time) ([]store.TaskResult, error) {
	tx, err := s.write.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	refs := map[string]queueRef{}
	results := make([]store.TaskResult, len(ts))
	// queue name -> earliest schedule among created tasks, for one event per queue
	earliest := map[string]time.Time{}
	queueIDs := map[string]int64{}

	for i := range ts {
		t := ts[i]
		if t.ScheduleTime.IsZero() {
			t.ScheduleTime = now
		}
		normalizeNewTask(&t)

		ref, ok := refs[t.Queue]
		if !ok {
			ref, err = getQueueRef(ctx, tx, t.Queue)
			if err != nil {
				results[i] = store.TaskResult{Err: err}
				continue
			}
			refs[t.Queue] = ref
		}
		if ref.state == store.QueueDisabled {
			results[i] = store.TaskResult{Err: fmt.Errorf("queue %q is disabled: %w", t.Queue, store.ErrFailedPrecondition)}
			continue
		}

		createdMS := ref.createdMS(now)
		pk, ierr := s.insertTask(ctx, tx, ref, t, createdMS)
		if ierr != nil {
			results[i] = store.TaskResult{Err: ierr}
			continue
		}
		results[i] = store.TaskResult{Task: store.Task{
			PK: pk, QueueID: ref.id, Queue: t.Queue, ID: t.ID, State: store.TaskPending,
			URL: t.URL, Method: t.Method, Headers: t.Headers, Body: t.Body,
			DispatchDeadline: t.DispatchDeadline, RetryOverride: t.RetryOverride,
			ScheduleTime: t.ScheduleTime.UTC(), CreatedAt: time.UnixMilli(createdMS).UTC(),
		}}
		if cur, ok := earliest[t.Queue]; !ok || t.ScheduleTime.Before(cur) {
			earliest[t.Queue] = t.ScheduleTime
			queueIDs[t.Queue] = ref.id
		}
	}

	if err := tx.Commit(); err != nil {
		return nil, err
	}
	for name, sched := range earliest {
		s.emit(store.Event{Kind: store.EventTaskReady, QueueID: queueIDs[name], Queue: name, ScheduleTime: sched})
	}
	return results, nil
}

func (s *Store) GetTask(ctx context.Context, queue, taskID string, view store.View) (store.Task, error) {
	row := s.read.QueryRowContext(ctx, `SELECT `+taskCols(view)+` FROM tasks t JOIN queues q ON q.id = t.queue_id
		WHERE q.name=? AND t.task_id=? AND t.state != 'deleted' AND `+notPurged, queue, taskID)
	t, err := scanTask(row)
	if err == sql.ErrNoRows {
		return t, store.ErrNotFound
	}
	return t, err
}

func (s *Store) ListTasks(ctx context.Context, opts store.ListTasksOpts) ([]store.Task, string, error) {
	size := opts.PageSize
	if size <= 0 {
		size = 100
	} else if size > 1000 {
		size = 1000
	}
	view := opts.View
	if view == "" {
		view = store.ViewBasic
	}

	query := `SELECT ` + taskCols(view) + ` FROM tasks t JOIN queues q ON q.id = t.queue_id
		WHERE q.name=? AND ` + notPurged
	args := []any{opts.Queue}
	if opts.State != "" {
		query += ` AND t.state = ?`
		args = append(args, string(opts.State))
	} else {
		query += ` AND t.state != 'deleted'`
	}
	if opts.IDPrefix != "" {
		query += ` AND t.task_id LIKE ? ESCAPE '\'`
		args = append(args, escapeLike(opts.IDPrefix)+"%")
	}
	if opts.PageToken != "" {
		b, err := base64.RawURLEncoding.DecodeString(opts.PageToken)
		if err != nil {
			return nil, "", fmt.Errorf("%w: bad page_token", store.ErrFailedPrecondition)
		}
		last, err := strconv.ParseInt(string(b), 10, 64)
		if err != nil {
			return nil, "", fmt.Errorf("%w: bad page_token", store.ErrFailedPrecondition)
		}
		query += ` AND t.id < ?`
		args = append(args, last)
	}
	query += ` ORDER BY t.id DESC LIMIT ?`
	args = append(args, size+1)

	// Queue existence check so an empty result on an unknown queue 404s.
	if _, err := s.GetQueue(ctx, opts.Queue); err != nil {
		return nil, "", err
	}

	rows, err := s.read.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, "", err
	}
	defer rows.Close()
	var out []store.Task
	for rows.Next() {
		t, err := scanTask(rows)
		if err != nil {
			return nil, "", err
		}
		out = append(out, t)
	}
	if err := rows.Err(); err != nil {
		return nil, "", err
	}
	next := ""
	if len(out) > size {
		out = out[:size]
		next = base64.RawURLEncoding.EncodeToString([]byte(strconv.FormatInt(out[len(out)-1].PK, 10)))
	}
	return out, next, nil
}

func (s *Store) DeleteTask(ctx context.Context, queue, taskID string, now time.Time) error {
	res, err := s.write.ExecContext(ctx, `UPDATE tasks SET state='deleted', finished_at=?,
		lease_token=NULL, lease_expires_at=NULL
		WHERE id IN (
			SELECT t.id FROM tasks t JOIN queues q ON q.id = t.queue_id
			WHERE q.name=? AND t.task_id=? AND t.state != 'deleted' AND `+notPurged+`)`,
		ms(now), queue, taskID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return store.ErrNotFound
	}
	return nil
}

func (s *Store) ForceRun(ctx context.Context, queue, taskID string, now time.Time) (store.Task, error) {
	tx, err := s.write.BeginTx(ctx, nil)
	if err != nil {
		return store.Task{}, err
	}
	defer tx.Rollback()

	ref, err := getQueueRef(ctx, tx, queue)
	if err != nil {
		return store.Task{}, err
	}
	if ref.state == store.QueueDisabled {
		return store.Task{}, fmt.Errorf("queue %q is disabled: %w", queue, store.ErrFailedPrecondition)
	}

	var pk, createdMS int64
	var state string
	err = tx.QueryRowContext(ctx, `SELECT id, state, created_at FROM tasks WHERE queue_id=? AND task_id=?`,
		ref.id, taskID).Scan(&pk, &state, &createdMS)
	if err == sql.ErrNoRows {
		return store.Task{}, store.ErrNotFound
	}
	if err != nil {
		return store.Task{}, err
	}
	if ref.purgedMS >= 0 && createdMS <= ref.purgedMS {
		return store.Task{}, store.ErrNotFound
	}
	switch store.TaskState(state) {
	case store.TaskPending:
	case store.TaskLeased:
		return store.Task{}, fmt.Errorf("task is currently dispatched: %w", store.ErrFailedPrecondition)
	default: // succeeded, failed, deleted — GCP parity: not found
		return store.Task{}, store.ErrNotFound
	}

	if _, err := tx.ExecContext(ctx, `UPDATE tasks SET schedule_time=? WHERE id=?`, ms(now), pk); err != nil {
		return store.Task{}, err
	}
	row := tx.QueryRowContext(ctx, `SELECT `+taskCols(store.ViewFull)+` FROM tasks t JOIN queues q ON q.id=t.queue_id WHERE t.id=?`, pk)
	t, err := scanTask(row)
	if err != nil {
		return t, err
	}
	if err := tx.Commit(); err != nil {
		return t, err
	}
	s.emit(store.Event{Kind: store.EventRunTask, QueueID: ref.id, Queue: queue, TaskPK: pk})
	return t, nil
}

func (s *Store) ListAttempts(ctx context.Context, queue, taskID string) ([]store.Attempt, error) {
	if _, err := s.GetTask(ctx, queue, taskID, store.ViewBasic); err != nil {
		return nil, err
	}
	rows, err := s.read.QueryContext(ctx, `SELECT a.id, a.task_pk, a.queue_id, a.attempt_number,
		a.dispatch_time, a.response_time, a.response_status, a.error_kind, a.error_snippet, a.duration_ms
		FROM task_attempts a JOIN tasks t ON t.id = a.task_pk JOIN queues q ON q.id = t.queue_id
		WHERE q.name=? AND t.task_id=? ORDER BY a.attempt_number DESC LIMIT 1000`, queue, taskID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []store.Attempt
	for rows.Next() {
		var a store.Attempt
		var respTime, respStatus, durMS sql.NullInt64
		var kind, snippet sql.NullString
		var dispatchMS int64
		if err := rows.Scan(&a.ID, &a.TaskPK, &a.QueueID, &a.Number, &dispatchMS, &respTime,
			&respStatus, &kind, &snippet, &durMS); err != nil {
			return nil, err
		}
		a.DispatchTime = fromMS(dispatchMS)
		a.ResponseTime = scanNullableMS(respTime)
		if respStatus.Valid {
			a.ResponseStatus = int(respStatus.Int64)
		}
		a.ErrorKind = kind.String
		a.ErrorSnippet = snippet.String
		if durMS.Valid {
			a.Duration = time.Duration(durMS.Int64) * time.Millisecond
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// --- dispatcher ---

func (s *Store) SeedDispatch(ctx context.Context) ([]store.QueueSeed, error) {
	rows, err := s.read.QueryContext(ctx, `SELECT t.queue_id,
		COALESCE(SUM(CASE WHEN t.state='pending' THEN 1 ELSE 0 END),0),
		COALESCE(SUM(CASE WHEN t.state='leased' THEN 1 ELSE 0 END),0),
		MIN(CASE WHEN t.state='pending' THEN t.schedule_time END)
		FROM tasks t JOIN queues q ON q.id = t.queue_id
		WHERE t.state IN ('pending','leased') AND `+notPurged+`
		GROUP BY t.queue_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []store.QueueSeed
	for rows.Next() {
		var seed store.QueueSeed
		var next sql.NullInt64
		if err := rows.Scan(&seed.QueueID, &seed.Pending, &seed.Leased, &next); err != nil {
			return nil, err
		}
		seed.NextSchedule = scanNullableMS(next)
		out = append(out, seed)
	}
	return out, rows.Err()
}

func (s *Store) ClaimTasks(ctx context.Context, now time.Time, leaseToken string, leaseGrace time.Duration, budgets []store.ClaimBudget) ([]store.Task, error) {
	if len(budgets) == 0 {
		return nil, nil
	}
	tx, err := s.write.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	var claimed []int64
	for _, b := range budgets {
		if b.N <= 0 {
			continue
		}
		// Purge watermark read live, not from the caller's cached config.
		rows, err := tx.QueryContext(ctx, `SELECT id FROM tasks
			WHERE queue_id=? AND state='pending' AND schedule_time <= ?
			AND created_at > COALESCE((SELECT purged_at FROM queues WHERE id=?), -1)
			ORDER BY schedule_time LIMIT ?`, b.QueueID, ms(now), b.QueueID, b.N)
		if err != nil {
			return nil, err
		}
		var ids []int64
		for rows.Next() {
			var id int64
			if err := rows.Scan(&id); err != nil {
				rows.Close()
				return nil, err
			}
			ids = append(ids, id)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return nil, err
		}
		if len(ids) == 0 {
			continue
		}
		ph := make([]string, len(ids))
		args := []any{ms(now), leaseToken, ms(now), leaseGrace.Milliseconds()}
		for i, id := range ids {
			ph[i] = "?"
			args = append(args, id)
		}
		if _, err := tx.ExecContext(ctx, `UPDATE tasks SET state='leased',
			first_attempt_at = COALESCE(first_attempt_at, ?),
			lease_token = ?, lease_expires_at = ? + dispatch_deadline_ms + ?,
			dispatch_count = dispatch_count + 1
			WHERE id IN (`+strings.Join(ph, ",")+`)`, args...); err != nil {
			return nil, err
		}
		claimed = append(claimed, ids...)
	}

	if len(claimed) == 0 {
		return nil, tx.Commit()
	}
	ph := make([]string, len(claimed))
	args := make([]any, len(claimed))
	for i, id := range claimed {
		ph[i] = "?"
		args[i] = id
	}
	rows, err := tx.QueryContext(ctx, `SELECT `+taskCols(store.ViewFull)+` FROM tasks t JOIN queues q ON q.id=t.queue_id
		WHERE t.id IN (`+strings.Join(ph, ",")+`)`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []store.Task
	for rows.Next() {
		t, err := scanTask(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, tx.Commit()
}

func (s *Store) ClaimTaskForRun(ctx context.Context, now time.Time, leaseToken string, leaseGrace time.Duration, taskPK int64) (store.Task, bool, error) {
	tx, err := s.write.BeginTx(ctx, nil)
	if err != nil {
		return store.Task{}, false, err
	}
	defer tx.Rollback()

	res, err := tx.ExecContext(ctx, `UPDATE tasks SET state='leased',
		first_attempt_at = COALESCE(first_attempt_at, ?),
		lease_token = ?, lease_expires_at = ? + dispatch_deadline_ms + ?,
		dispatch_count = dispatch_count + 1
		WHERE id = ? AND state='pending'
		AND (SELECT q.state FROM queues q WHERE q.id = tasks.queue_id) != 'disabled'
		AND created_at > COALESCE((SELECT q.purged_at FROM queues q WHERE q.id = tasks.queue_id), -1)`,
		ms(now), leaseToken, ms(now), leaseGrace.Milliseconds(), taskPK)
	if err != nil {
		return store.Task{}, false, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return store.Task{}, false, tx.Commit()
	}
	row := tx.QueryRowContext(ctx, `SELECT `+taskCols(store.ViewFull)+` FROM tasks t JOIN queues q ON q.id=t.queue_id WHERE t.id=?`, taskPK)
	t, err := scanTask(row)
	if err != nil {
		return t, false, err
	}
	return t, true, tx.Commit()
}

func (s *Store) NextPending(ctx context.Context, queueID int64, now time.Time) (*time.Time, error) {
	var next sql.NullInt64
	err := s.read.QueryRowContext(ctx, `SELECT MIN(t.schedule_time) FROM tasks t JOIN queues q ON q.id=t.queue_id
		WHERE t.queue_id=? AND t.state='pending' AND `+notPurged, queueID).Scan(&next)
	if err != nil {
		return nil, err
	}
	return scanNullableMS(next), nil
}

func (s *Store) FinishAttempt(ctx context.Context, now time.Time, taskPK int64, leaseToken string, att store.Attempt, outcome store.Outcome, nextSchedule time.Time) (bool, error) {
	tx, err := s.write.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()

	respInc := 0
	if att.ResponseStatus != 0 {
		respInc = 1
	}
	fence := ` WHERE id=? AND lease_token=? AND state='leased'
		AND created_at > COALESCE((SELECT q.purged_at FROM queues q WHERE q.id = tasks.queue_id), -1)`

	var res sql.Result
	common := `response_count = response_count + ?, last_attempt_at=?, last_status=?, last_error=?,
		lease_token=NULL, lease_expires_at=NULL`
	lastStatus := any(nil)
	if att.ResponseStatus != 0 {
		lastStatus = att.ResponseStatus
	}
	lastErr := any(nil)
	if att.ErrorKind != "" {
		lastErr = att.ErrorKind
		if att.ErrorSnippet != "" {
			lastErr = att.ErrorKind + ": " + truncate(att.ErrorSnippet, 512)
		}
	}
	switch outcome {
	case store.OutcomeSucceeded:
		res, err = tx.ExecContext(ctx, `UPDATE tasks SET state='succeeded', finished_at=?, `+common+fence,
			ms(now), respInc, ms(att.DispatchTime), lastStatus, lastErr, taskPK, leaseToken)
	case store.OutcomeFailed:
		res, err = tx.ExecContext(ctx, `UPDATE tasks SET state='failed', finished_at=?, `+common+fence,
			ms(now), respInc, ms(att.DispatchTime), lastStatus, lastErr, taskPK, leaseToken)
	case store.OutcomeRetry:
		res, err = tx.ExecContext(ctx, `UPDATE tasks SET state='pending', schedule_time=?, `+common+fence,
			ms(nextSchedule), respInc, ms(att.DispatchTime), lastStatus, lastErr, taskPK, leaseToken)
	default:
		return false, fmt.Errorf("unknown outcome %d", outcome)
	}
	if err != nil {
		return false, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return false, tx.Commit()
	}

	if _, err := tx.ExecContext(ctx, `INSERT INTO task_attempts
		(task_pk, queue_id, attempt_number, dispatch_time, response_time, response_status, error_kind, error_snippet, duration_ms)
		VALUES (?,?,?,?,?,?,?,?,?)`,
		att.TaskPK, att.QueueID, att.Number, ms(att.DispatchTime), nullableMS(att.ResponseTime),
		lastStatusOrNil(att.ResponseStatus), nullableStr(att.ErrorKind), nullableStr(truncate(att.ErrorSnippet, 4096)),
		att.Duration.Milliseconds()); err != nil {
		return false, err
	}
	if err := tx.Commit(); err != nil {
		return false, err
	}
	if outcome == store.OutcomeRetry {
		s.emit(store.Event{Kind: store.EventTaskReady, QueueID: att.QueueID, ScheduleTime: nextSchedule})
	}
	return true, nil
}

func (s *Store) ListExpiredLeases(ctx context.Context, now time.Time, limit int) ([]store.Task, error) {
	rows, err := s.read.QueryContext(ctx, `SELECT `+taskCols(store.ViewFull)+`
		FROM tasks t JOIN queues q ON q.id=t.queue_id
		WHERE t.state='leased' AND t.lease_expires_at <= ? LIMIT ?`, ms(now), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []store.Task
	for rows.Next() {
		t, err := scanTask(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

func (s *Store) CleanupTerminal(ctx context.Context, now time.Time, succeededTTL, failedTTL, dedupWindow time.Duration, limit int) (int64, error) {
	maxD := func(a, b time.Duration) time.Duration {
		if a > b {
			return a
		}
		return b
	}
	var total int64
	for _, c := range []struct {
		state string
		ttl   time.Duration
	}{
		{"succeeded", maxD(succeededTTL, dedupWindow)},
		{"failed", maxD(failedTTL, dedupWindow)},
		{"deleted", dedupWindow},
	} {
		cutoff := ms(now.Add(-c.ttl))
		res, err := s.write.ExecContext(ctx, `DELETE FROM tasks WHERE id IN (
			SELECT id FROM tasks WHERE state=? AND finished_at <= ? LIMIT ?)`, c.state, cutoff, limit)
		if err != nil {
			return total, err
		}
		n, _ := res.RowsAffected()
		total += n
	}
	return total, nil
}

func (s *Store) HardDeletePurged(ctx context.Context, limit int) (int64, error) {
	res, err := s.write.ExecContext(ctx, `DELETE FROM tasks WHERE id IN (
		SELECT t.id FROM tasks t JOIN queues q ON q.id=t.queue_id
		WHERE q.purged_at IS NOT NULL AND t.created_at <= q.purged_at LIMIT ?)`, limit)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

func lastStatusOrNil(status int) any {
	if status == 0 {
		return nil
	}
	return status
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}
