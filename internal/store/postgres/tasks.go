package postgres

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/blissfulrays/pushq/internal/store"
)

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
	var deadlineMS int64
	var sched, created time.Time
	var firstAt, finishedAt, leaseExp, lastAt sql.NullTime
	var lastStatus sql.NullInt64
	err := r.Scan(&t.PK, &t.QueueID, &t.Queue, &t.ID, &t.State, &t.URL, &t.Method, &headers, &body,
		&deadlineMS, &retryOverride, &sched, &created, &firstAt, &finishedAt,
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
	t.ScheduleTime = sched.UTC()
	t.CreatedAt = created.UTC()
	t.FirstAttemptAt = scanNullableTime(firstAt)
	t.FinishedAt = scanNullableTime(finishedAt)
	t.LeaseToken = leaseToken.String
	t.LeaseExpiresAt = scanNullableTime(leaseExp)
	t.LastAttemptAt = scanNullableTime(lastAt)
	if lastStatus.Valid {
		t.LastStatus = int(lastStatus.Int64)
	}
	t.LastError = lastError.String
	return t, nil
}

type queueRef struct {
	id     int64
	state  store.QueueState
	purged *time.Time
}

func getQueueRef(ctx context.Context, tx querier, name string) (queueRef, error) {
	var ref queueRef
	var purged sql.NullTime
	err := tx.QueryRowContext(ctx, `SELECT id, state, purged_at FROM queues WHERE name = $1`, name).
		Scan(&ref.id, &ref.state, &purged)
	if err == sql.ErrNoRows {
		return ref, fmt.Errorf("queue %q: %w", name, store.ErrNotFound)
	}
	if err != nil {
		return ref, err
	}
	ref.purged = scanNullableTime(purged)
	return ref, nil
}

// createdAt returns the creation time to record for a new task. It is always
// strictly after the queue's purge watermark: a task created after a purge
// (even within the same microsecond, or on a replica whose clock lags the one
// that purged) must never be hidden by it.
func (ref queueRef) createdAt(now time.Time) time.Time {
	c := now.UTC().Round(time.Microsecond) // timestamptz precision
	if ref.purged != nil && !c.After(*ref.purged) {
		c = ref.purged.UTC().Add(time.Microsecond)
	}
	return c
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

// insertTask inserts inside a savepoint so a conflict doesn't poison the
// surrounding transaction (a Postgres-specific requirement).
func insertTask(ctx context.Context, tx *sql.Tx, ref queueRef, t store.NewTask, created time.Time, spName string) (int64, error) {
	headers, err := jsonOrNil(t.Headers)
	if err != nil {
		return 0, err
	}
	override, err := jsonOrNil(t.RetryOverride)
	if err != nil {
		return 0, err
	}
	args := []any{ref.id, t.ID, t.URL, t.Method, headers, t.Body,
		t.DispatchDeadline.Milliseconds(), override, t.ScheduleTime.UTC(), created}
	const q = `INSERT INTO tasks (queue_id, task_id, url, method, headers, body,
		dispatch_deadline_ms, retry_override, schedule_time, created_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10) RETURNING id`

	if _, err := tx.ExecContext(ctx, "SAVEPOINT "+spName); err != nil {
		return 0, err
	}
	pk, err := insertInSavepoint(ctx, tx, ref, t, q, args, spName)
	if err != nil {
		// Any failed statement aborts the whole transaction in Postgres;
		// roll back to the savepoint so the rest of a batch still commits.
		if _, rerr := tx.ExecContext(ctx, "ROLLBACK TO SAVEPOINT "+spName); rerr != nil {
			return 0, rerr
		}
		return 0, err
	}
	if _, err := tx.ExecContext(ctx, "RELEASE SAVEPOINT "+spName); err != nil {
		return 0, err
	}
	return pk, nil
}

func insertInSavepoint(ctx context.Context, tx *sql.Tx, ref queueRef, t store.NewTask, q string, args []any, spName string) (int64, error) {
	var pk int64
	err := tx.QueryRowContext(ctx, q, args...).Scan(&pk)
	if err != nil && isUniqueViolation(err) {
		if _, rerr := tx.ExecContext(ctx, "ROLLBACK TO SAVEPOINT "+spName); rerr != nil {
			return 0, rerr
		}
		// Purged rows are doomed; free the ID immediately.
		var existingPK int64
		var createdAt time.Time
		serr := tx.QueryRowContext(ctx,
			`SELECT id, created_at FROM tasks WHERE queue_id=$1 AND task_id=$2`, ref.id, t.ID).
			Scan(&existingPK, &createdAt)
		if serr != nil || ref.purged == nil || createdAt.After(*ref.purged) {
			return 0, store.ErrAlreadyExists
		}
		if _, derr := tx.ExecContext(ctx, `DELETE FROM tasks WHERE id=$1`, existingPK); derr != nil {
			return 0, derr
		}
		err = tx.QueryRowContext(ctx, q, args...).Scan(&pk)
	}
	return pk, err
}

func (s *Store) CreateTask(ctx context.Context, t store.NewTask, now time.Time) (store.Task, error) {
	res, err := s.CreateTasks(ctx, []store.NewTask{t}, now)
	if err != nil {
		return store.Task{}, err
	}
	return res[0].Task, res[0].Err
}

func (s *Store) CreateTasks(ctx context.Context, ts []store.NewTask, now time.Time) ([]store.TaskResult, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	refs := map[string]queueRef{}
	badQueues := map[string]error{}
	results := make([]store.TaskResult, len(ts))
	earliest := map[string]time.Time{}
	queueIDs := map[string]int64{}

	for i := range ts {
		t := ts[i]
		if t.ScheduleTime.IsZero() {
			t.ScheduleTime = now
		}
		normalizeNewTask(&t)

		if berr, bad := badQueues[t.Queue]; bad {
			results[i] = store.TaskResult{Err: berr}
			continue
		}
		ref, ok := refs[t.Queue]
		if !ok {
			ref, err = getQueueRef(ctx, tx, t.Queue)
			if err != nil {
				badQueues[t.Queue] = err
				results[i] = store.TaskResult{Err: err}
				continue
			}
			refs[t.Queue] = ref
		}
		if ref.state == store.QueueDisabled {
			results[i] = store.TaskResult{Err: fmt.Errorf("queue %q is disabled: %w", t.Queue, store.ErrFailedPrecondition)}
			continue
		}

		created := ref.createdAt(now)
		pk, ierr := insertTask(ctx, tx, ref, t, created, fmt.Sprintf("sp_%d", i))
		if ierr != nil {
			results[i] = store.TaskResult{Err: ierr}
			continue
		}
		results[i] = store.TaskResult{Task: store.Task{
			PK: pk, QueueID: ref.id, Queue: t.Queue, ID: t.ID, State: store.TaskPending,
			URL: t.URL, Method: t.Method, Headers: t.Headers, Body: t.Body,
			DispatchDeadline: t.DispatchDeadline, RetryOverride: t.RetryOverride,
			ScheduleTime: t.ScheduleTime.UTC(), CreatedAt: created,
		}}
		if cur, ok := earliest[t.Queue]; !ok || t.ScheduleTime.Before(cur) {
			earliest[t.Queue] = t.ScheduleTime
			queueIDs[t.Queue] = ref.id
		}
	}

	for name, sched := range earliest {
		notify(ctx, tx, store.Event{Kind: store.EventTaskReady, QueueID: queueIDs[name], Queue: name, ScheduleTime: sched})
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return results, nil
}

func (s *Store) GetTask(ctx context.Context, queue, taskID string, view store.View) (store.Task, error) {
	row := s.db.QueryRowContext(ctx, `SELECT `+taskCols(view)+` FROM tasks t JOIN queues q ON q.id = t.queue_id
		WHERE q.name=$1 AND t.task_id=$2 AND t.state != 'deleted' AND `+notPurged, queue, taskID)
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

	if _, err := s.GetQueue(ctx, opts.Queue); err != nil {
		return nil, "", err
	}

	query := `SELECT ` + taskCols(view) + ` FROM tasks t JOIN queues q ON q.id = t.queue_id
		WHERE q.name=$1 AND ` + notPurged
	args := []any{opts.Queue}
	n := 1
	if opts.State != "" {
		n++
		query += fmt.Sprintf(` AND t.state = $%d`, n)
		args = append(args, string(opts.State))
	} else {
		query += ` AND t.state != 'deleted'`
	}
	if opts.IDPrefix != "" {
		n++
		query += fmt.Sprintf(` AND t.task_id LIKE $%d ESCAPE '\'`, n)
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
		n++
		query += fmt.Sprintf(` AND t.id < $%d`, n)
		args = append(args, last)
	}
	n++
	query += fmt.Sprintf(` ORDER BY t.id DESC LIMIT $%d`, n)
	args = append(args, size+1)

	rows, err := s.db.QueryContext(ctx, query, args...)
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
	res, err := s.db.ExecContext(ctx, `UPDATE tasks SET state='deleted', finished_at=$1,
		lease_token=NULL, lease_expires_at=NULL
		WHERE id IN (
			SELECT t.id FROM tasks t JOIN queues q ON q.id = t.queue_id
			WHERE q.name=$2 AND t.task_id=$3 AND t.state != 'deleted' AND `+notPurged+`)`,
		now.UTC(), queue, taskID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return store.ErrNotFound
	}
	return nil
}

func (s *Store) ForceRun(ctx context.Context, queue, taskID string, now time.Time) (store.Task, error) {
	tx, err := s.db.BeginTx(ctx, nil)
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

	var pk int64
	var state string
	var createdAt time.Time
	err = tx.QueryRowContext(ctx, `SELECT id, state, created_at FROM tasks WHERE queue_id=$1 AND task_id=$2 FOR UPDATE`,
		ref.id, taskID).Scan(&pk, &state, &createdAt)
	if err == sql.ErrNoRows {
		return store.Task{}, store.ErrNotFound
	}
	if err != nil {
		return store.Task{}, err
	}
	if ref.purged != nil && !createdAt.After(*ref.purged) {
		return store.Task{}, store.ErrNotFound
	}
	switch store.TaskState(state) {
	case store.TaskPending:
	case store.TaskLeased:
		return store.Task{}, fmt.Errorf("task is currently dispatched: %w", store.ErrFailedPrecondition)
	default:
		return store.Task{}, store.ErrNotFound
	}

	if _, err := tx.ExecContext(ctx, `UPDATE tasks SET schedule_time=$1 WHERE id=$2`, now.UTC(), pk); err != nil {
		return store.Task{}, err
	}
	row := tx.QueryRowContext(ctx, `SELECT `+taskCols(store.ViewFull)+` FROM tasks t JOIN queues q ON q.id=t.queue_id WHERE t.id=$1`, pk)
	t, err := scanTask(row)
	if err != nil {
		return t, err
	}
	notify(ctx, tx, store.Event{Kind: store.EventRunTask, QueueID: ref.id, Queue: queue, TaskPK: pk})
	if err := tx.Commit(); err != nil {
		return t, err
	}
	return t, nil
}

func (s *Store) ListAttempts(ctx context.Context, queue, taskID string) ([]store.Attempt, error) {
	if _, err := s.GetTask(ctx, queue, taskID, store.ViewBasic); err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT a.id, a.task_pk, a.queue_id, a.attempt_number,
		a.dispatch_time, a.response_time, a.response_status, a.error_kind, a.error_snippet, a.duration_ms
		FROM task_attempts a JOIN tasks t ON t.id = a.task_pk JOIN queues q ON q.id = t.queue_id
		WHERE q.name=$1 AND t.task_id=$2 ORDER BY a.attempt_number DESC LIMIT 1000`, queue, taskID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []store.Attempt
	for rows.Next() {
		var a store.Attempt
		var respTime sql.NullTime
		var respStatus, durMS sql.NullInt64
		var kind, snippet sql.NullString
		var dispatch time.Time
		if err := rows.Scan(&a.ID, &a.TaskPK, &a.QueueID, &a.Number, &dispatch, &respTime,
			&respStatus, &kind, &snippet, &durMS); err != nil {
			return nil, err
		}
		a.DispatchTime = dispatch.UTC()
		a.ResponseTime = scanNullableTime(respTime)
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
	rows, err := s.db.QueryContext(ctx, `SELECT t.queue_id,
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
		var next sql.NullTime
		if err := rows.Scan(&seed.QueueID, &seed.Pending, &seed.Leased, &next); err != nil {
			return nil, err
		}
		seed.NextSchedule = scanNullableTime(next)
		out = append(out, seed)
	}
	return out, rows.Err()
}

// ClaimTasks: one budgeted statement across all hot queues — a VALUES list
// CROSS JOIN LATERAL per-queue limited SKIP LOCKED select feeding an UPDATE.
func (s *Store) ClaimTasks(ctx context.Context, now time.Time, leaseToken string, leaseGrace time.Duration, budgets []store.ClaimBudget) ([]store.Task, error) {
	budgets = nonEmpty(budgets)
	if len(budgets) == 0 {
		return nil, nil
	}

	values := make([]string, len(budgets))
	args := []any{}
	for i, b := range budgets {
		values[i] = fmt.Sprintf("($%d::bigint, $%d::int)", len(args)+1, len(args)+2)
		args = append(args, b.QueueID, b.N)
	}
	nowIdx := len(args) + 1
	tokIdx := len(args) + 2
	graceIdx := len(args) + 3
	args = append(args, now.UTC(), leaseToken, leaseGrace.Seconds())

	// The purge watermark is read live from queues (not from the caller's
	// cached config) so a purge is honored even if its change event was lost.
	query := fmt.Sprintf(`WITH b(queue_id, n) AS (VALUES %s),
c AS (
  SELECT x.id FROM b JOIN queues bq ON bq.id = b.queue_id CROSS JOIN LATERAL (
    SELECT t.id FROM tasks t
    WHERE t.queue_id = b.queue_id AND t.state = 'pending' AND t.schedule_time <= $%d
      AND (bq.purged_at IS NULL OR t.created_at > bq.purged_at)
    ORDER BY t.schedule_time LIMIT b.n
    FOR UPDATE SKIP LOCKED
  ) x
)
UPDATE tasks t SET state='leased',
  lease_token=$%d,
  lease_expires_at=$%d::timestamptz + make_interval(secs => t.dispatch_deadline_ms/1000.0) + make_interval(secs => $%d),
  dispatch_count = t.dispatch_count + 1,
  first_attempt_at = COALESCE(t.first_attempt_at, $%d::timestamptz)
FROM c, queues q
WHERE t.id = c.id AND q.id = t.queue_id
RETURNING `+taskCols(store.ViewFull),
		strings.Join(values, ","), nowIdx, tokIdx, nowIdx, graceIdx, nowIdx)

	rows, err := s.db.QueryContext(ctx, query, args...)
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
	// RETURNING order is unspecified; restore per-queue schedule order.
	sort.Slice(out, func(i, j int) bool {
		if out[i].QueueID != out[j].QueueID {
			return out[i].QueueID < out[j].QueueID
		}
		if !out[i].ScheduleTime.Equal(out[j].ScheduleTime) {
			return out[i].ScheduleTime.Before(out[j].ScheduleTime)
		}
		return out[i].PK < out[j].PK
	})
	return out, nil
}

func nonEmpty(budgets []store.ClaimBudget) []store.ClaimBudget {
	out := budgets[:0]
	for _, b := range budgets {
		if b.N > 0 {
			out = append(out, b)
		}
	}
	return out
}

func (s *Store) ClaimTaskForRun(ctx context.Context, now time.Time, leaseToken string, leaseGrace time.Duration, taskPK int64) (store.Task, bool, error) {
	rows, err := s.db.QueryContext(ctx, `UPDATE tasks t SET state='leased',
		lease_token=$1,
		lease_expires_at=$2::timestamptz + make_interval(secs => t.dispatch_deadline_ms/1000.0) + make_interval(secs => $3),
		dispatch_count = t.dispatch_count + 1,
		first_attempt_at = COALESCE(t.first_attempt_at, $2::timestamptz)
		FROM queues q
		WHERE t.id = $4 AND t.state='pending' AND q.id = t.queue_id AND q.state != 'disabled'
		AND `+notPurged+`
		RETURNING `+taskCols(store.ViewFull),
		leaseToken, now.UTC(), leaseGrace.Seconds(), taskPK)
	if err != nil {
		return store.Task{}, false, err
	}
	defer rows.Close()
	if !rows.Next() {
		return store.Task{}, false, rows.Err()
	}
	t, err := scanTask(rows)
	if err != nil {
		return t, false, err
	}
	return t, true, rows.Err()
}

func (s *Store) NextPending(ctx context.Context, queueID int64, now time.Time) (*time.Time, error) {
	var next sql.NullTime
	err := s.db.QueryRowContext(ctx, `SELECT MIN(t.schedule_time) FROM tasks t JOIN queues q ON q.id=t.queue_id
		WHERE t.queue_id=$1 AND t.state='pending' AND `+notPurged, queueID).Scan(&next)
	if err != nil {
		return nil, err
	}
	return scanNullableTime(next), nil
}

func (s *Store) FinishAttempt(ctx context.Context, now time.Time, taskPK int64, leaseToken string, att store.Attempt, outcome store.Outcome, nextSchedule time.Time) (bool, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()

	respInc := 0
	if att.ResponseStatus != 0 {
		respInc = 1
	}
	var lastStatus any
	if att.ResponseStatus != 0 {
		lastStatus = att.ResponseStatus
	}
	var lastErr any
	if att.ErrorKind != "" {
		msg := att.ErrorKind
		if att.ErrorSnippet != "" {
			msg = att.ErrorKind + ": " + truncate(att.ErrorSnippet, 512)
		}
		lastErr = msg
	}

	fence := ` WHERE t.id=$6 AND t.lease_token=$7 AND t.state='leased'
		AND t.created_at > COALESCE((SELECT q.purged_at FROM queues q WHERE q.id = t.queue_id), 'epoch'::timestamptz)`
	common := `response_count = t.response_count + $2, last_attempt_at=$3, last_status=$4, last_error=$5,
		lease_token=NULL, lease_expires_at=NULL`

	var res sql.Result
	switch outcome {
	case store.OutcomeSucceeded:
		res, err = tx.ExecContext(ctx, `UPDATE tasks t SET state='succeeded', finished_at=$1, `+common+fence,
			now.UTC(), respInc, att.DispatchTime.UTC(), lastStatus, lastErr, taskPK, leaseToken)
	case store.OutcomeFailed:
		res, err = tx.ExecContext(ctx, `UPDATE tasks t SET state='failed', finished_at=$1, `+common+fence,
			now.UTC(), respInc, att.DispatchTime.UTC(), lastStatus, lastErr, taskPK, leaseToken)
	case store.OutcomeRetry:
		res, err = tx.ExecContext(ctx, `UPDATE tasks t SET state='pending', schedule_time=$1, `+common+fence,
			nextSchedule.UTC(), respInc, att.DispatchTime.UTC(), lastStatus, lastErr, taskPK, leaseToken)
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
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)`,
		att.TaskPK, att.QueueID, att.Number, att.DispatchTime.UTC(), nullableTime(att.ResponseTime),
		lastStatusOrNil(att.ResponseStatus), nullableStr(att.ErrorKind), nullableStr(truncate(att.ErrorSnippet, 4096)),
		att.Duration.Milliseconds()); err != nil {
		return false, err
	}
	if outcome == store.OutcomeRetry {
		notify(ctx, tx, store.Event{Kind: store.EventTaskReady, QueueID: att.QueueID, ScheduleTime: nextSchedule})
	}
	if err := tx.Commit(); err != nil {
		return false, err
	}
	return true, nil
}

func (s *Store) ListExpiredLeases(ctx context.Context, now time.Time, limit int) ([]store.Task, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+taskCols(store.ViewFull)+`
		FROM tasks t JOIN queues q ON q.id=t.queue_id
		WHERE t.state='leased' AND t.lease_expires_at <= $1 LIMIT $2`, now.UTC(), limit)
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
		res, err := s.db.ExecContext(ctx, `DELETE FROM tasks WHERE id IN (
			SELECT id FROM tasks WHERE state=$1 AND finished_at <= $2 LIMIT $3)`,
			c.state, now.Add(-c.ttl).UTC(), limit)
		if err != nil {
			return total, err
		}
		n, _ := res.RowsAffected()
		total += n
	}
	return total, nil
}

func (s *Store) HardDeletePurged(ctx context.Context, limit int) (int64, error) {
	res, err := s.db.ExecContext(ctx, `DELETE FROM tasks WHERE id IN (
		SELECT t.id FROM tasks t JOIN queues q ON q.id=t.queue_id
		WHERE q.purged_at IS NOT NULL AND t.created_at <= q.purged_at LIMIT $1)`, limit)
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
