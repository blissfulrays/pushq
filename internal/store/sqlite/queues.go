package sqlite

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/blissfulrays/pushq/internal/store"
)

const queueCols = `id, name, state, max_dispatches_per_second, max_burst_size, max_concurrent_dispatches,
retry_max_attempts, retry_max_duration_ms, retry_min_backoff_ms, retry_max_backoff_ms, retry_max_doublings,
target_base_url, target_method, target_headers, signing_secret, dead_letter_queue,
purged_at, config_version, created_at, updated_at`

type rowScanner interface{ Scan(dest ...any) error }

func scanQueue(r rowScanner) (store.Queue, error) {
	var q store.Queue
	var burst sql.NullInt64
	var maxDurMS, minBackMS, maxBackMS int64
	var baseURL, method, headersJSON, secret, dlq sql.NullString
	var purged sql.NullInt64
	var created, updated int64
	err := r.Scan(&q.ID, &q.Name, &q.State,
		&q.RateLimits.MaxDispatchesPerSecond, &burst, &q.RateLimits.MaxConcurrentDispatches,
		&q.Retry.MaxAttempts, &maxDurMS, &minBackMS, &maxBackMS, &q.Retry.MaxDoublings,
		&baseURL, &method, &headersJSON, &secret, &dlq,
		&purged, &q.ConfigVersion, &created, &updated)
	if err != nil {
		return q, err
	}
	if burst.Valid {
		q.RateLimits.MaxBurstSize = int(burst.Int64)
	}
	q.Retry.MaxRetryDuration = time.Duration(maxDurMS) * time.Millisecond
	q.Retry.MinBackoff = time.Duration(minBackMS) * time.Millisecond
	q.Retry.MaxBackoff = time.Duration(maxBackMS) * time.Millisecond
	q.Target.BaseURL = baseURL.String
	q.Target.Method = method.String
	if headersJSON.Valid && headersJSON.String != "" {
		if err := json.Unmarshal([]byte(headersJSON.String), &q.Target.HeaderOverrides); err != nil {
			return q, fmt.Errorf("queue %s: bad target_headers: %w", q.Name, err)
		}
	}
	q.Target.SigningSecret = secret.String
	q.DeadLetterQueue = dlq.String
	q.PurgedAt = scanNullableMS(purged)
	q.CreatedAt = fromMS(created)
	q.UpdatedAt = fromMS(updated)
	return q, nil
}

func (s *Store) getQueueTx(ctx context.Context, db interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, name string) (store.Queue, error) {
	q, err := scanQueue(db.QueryRowContext(ctx, `SELECT `+queueCols+` FROM queues WHERE name = ?`, name))
	if err == sql.ErrNoRows {
		return q, store.ErrNotFound
	}
	return q, err
}

func (s *Store) GetQueue(ctx context.Context, name string) (store.Queue, error) {
	return s.getQueueTx(ctx, s.read, name)
}

func (s *Store) GetQueuesByID(ctx context.Context, ids []int64) ([]store.Queue, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	ph := make([]string, len(ids))
	args := make([]any, len(ids))
	for i, id := range ids {
		ph[i] = "?"
		args[i] = id
	}
	rows, err := s.read.QueryContext(ctx, `SELECT `+queueCols+` FROM queues WHERE id IN (`+strings.Join(ph, ",")+`)`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []store.Queue
	for rows.Next() {
		q, err := scanQueue(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, q)
	}
	return out, rows.Err()
}

func insertQueueArgs(u store.QueueUpsert, now time.Time) ([]any, error) {
	headers, err := jsonOrNil(u.Target.HeaderOverrides)
	if err != nil {
		return nil, err
	}
	var burst any
	if u.RateLimits.MaxBurstSize > 0 {
		burst = u.RateLimits.MaxBurstSize
	}
	return []any{
		u.Name, u.RateLimits.MaxDispatchesPerSecond, burst, u.RateLimits.MaxConcurrentDispatches,
		u.Retry.MaxAttempts, u.Retry.MaxRetryDuration.Milliseconds(), u.Retry.MinBackoff.Milliseconds(),
		u.Retry.MaxBackoff.Milliseconds(), u.Retry.MaxDoublings,
		nullableStr(u.Target.BaseURL), nullableStr(u.Target.Method), headers,
		nullableStr(u.Target.SigningSecret), nullableStr(u.DeadLetterQueue),
		ms(now), ms(now),
	}, nil
}

const insertQueueSQL = `INSERT INTO queues (
name, max_dispatches_per_second, max_burst_size, max_concurrent_dispatches,
retry_max_attempts, retry_max_duration_ms, retry_min_backoff_ms, retry_max_backoff_ms, retry_max_doublings,
target_base_url, target_method, target_headers, signing_secret, dead_letter_queue,
created_at, updated_at) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`

func (s *Store) CreateQueue(ctx context.Context, u store.QueueUpsert, now time.Time) (store.Queue, error) {
	args, err := insertQueueArgs(u, now)
	if err != nil {
		return store.Queue{}, err
	}
	if _, err := s.write.ExecContext(ctx, insertQueueSQL, args...); err != nil {
		if isUniqueViolation(err) {
			return store.Queue{}, store.ErrAlreadyExists
		}
		return store.Queue{}, err
	}
	q, err := s.getQueueTx(ctx, s.write, u.Name)
	if err != nil {
		return q, err
	}
	s.emit(store.Event{Kind: store.EventQueueChanged, QueueID: q.ID, Queue: q.Name})
	return q, nil
}

// sameConfig compares the config-bearing fields (not state/purge/timestamps).
func sameConfig(q store.Queue, u store.QueueUpsert) bool {
	if q.RateLimits.MaxDispatchesPerSecond != u.RateLimits.MaxDispatchesPerSecond ||
		q.RateLimits.MaxBurstSize != u.RateLimits.MaxBurstSize ||
		q.RateLimits.MaxConcurrentDispatches != u.RateLimits.MaxConcurrentDispatches {
		return false
	}
	if q.Retry != u.Retry {
		return false
	}
	if q.Target.BaseURL != u.Target.BaseURL || q.Target.Method != u.Target.Method ||
		q.Target.SigningSecret != u.Target.SigningSecret {
		return false
	}
	if len(q.Target.HeaderOverrides) != len(u.Target.HeaderOverrides) {
		return false
	}
	for k, v := range u.Target.HeaderOverrides {
		if q.Target.HeaderOverrides[k] != v {
			return false
		}
	}
	return q.DeadLetterQueue == u.DeadLetterQueue
}

func (s *Store) UpsertQueue(ctx context.Context, u store.QueueUpsert, now time.Time) (store.Queue, store.UpsertOutcome, error) {
	tx, err := s.write.BeginTx(ctx, nil)
	if err != nil {
		return store.Queue{}, 0, err
	}
	defer tx.Rollback()

	existing, err := s.getQueueTx(ctx, tx, u.Name)
	if err == store.ErrNotFound {
		args, aerr := insertQueueArgs(u, now)
		if aerr != nil {
			return store.Queue{}, 0, aerr
		}
		if _, err := tx.ExecContext(ctx, insertQueueSQL, args...); err != nil {
			return store.Queue{}, 0, err
		}
		q, err := s.getQueueTx(ctx, tx, u.Name)
		if err != nil {
			return q, 0, err
		}
		if err := tx.Commit(); err != nil {
			return q, 0, err
		}
		s.emit(store.Event{Kind: store.EventQueueChanged, QueueID: q.ID, Queue: q.Name})
		return q, store.UpsertCreated, nil
	}
	if err != nil {
		return store.Queue{}, 0, err
	}

	if u.KeepSecret {
		u.Target.SigningSecret = existing.Target.SigningSecret
	}
	if sameConfig(existing, u) {
		return existing, store.UpsertNoop, nil
	}

	headers, err := jsonOrNil(u.Target.HeaderOverrides)
	if err != nil {
		return store.Queue{}, 0, err
	}
	var burst any
	if u.RateLimits.MaxBurstSize > 0 {
		burst = u.RateLimits.MaxBurstSize
	}
	_, err = tx.ExecContext(ctx, `UPDATE queues SET
		max_dispatches_per_second=?, max_burst_size=?, max_concurrent_dispatches=?,
		retry_max_attempts=?, retry_max_duration_ms=?, retry_min_backoff_ms=?, retry_max_backoff_ms=?, retry_max_doublings=?,
		target_base_url=?, target_method=?, target_headers=?, signing_secret=?, dead_letter_queue=?,
		config_version=config_version+1, updated_at=? WHERE name=?`,
		u.RateLimits.MaxDispatchesPerSecond, burst, u.RateLimits.MaxConcurrentDispatches,
		u.Retry.MaxAttempts, u.Retry.MaxRetryDuration.Milliseconds(), u.Retry.MinBackoff.Milliseconds(),
		u.Retry.MaxBackoff.Milliseconds(), u.Retry.MaxDoublings,
		nullableStr(u.Target.BaseURL), nullableStr(u.Target.Method), headers,
		nullableStr(u.Target.SigningSecret), nullableStr(u.DeadLetterQueue),
		ms(now), u.Name)
	if err != nil {
		return store.Queue{}, 0, err
	}
	q, err := s.getQueueTx(ctx, tx, u.Name)
	if err != nil {
		return q, 0, err
	}
	if err := tx.Commit(); err != nil {
		return q, 0, err
	}
	s.emit(store.Event{Kind: store.EventQueueChanged, QueueID: q.ID, Queue: q.Name})
	return q, store.UpsertUpdated, nil
}

func (s *Store) PatchQueue(ctx context.Context, name string, p store.QueuePatch, now time.Time) (store.Queue, error) {
	tx, err := s.write.BeginTx(ctx, nil)
	if err != nil {
		return store.Queue{}, err
	}
	defer tx.Rollback()

	q, err := s.getQueueTx(ctx, tx, name)
	if err != nil {
		return store.Queue{}, err
	}

	u := store.QueueUpsert{
		Name: q.Name, RateLimits: q.RateLimits, Retry: q.Retry, Target: q.Target,
		DeadLetterQueue: q.DeadLetterQueue,
	}
	state := q.State
	if p.State != nil {
		state = *p.State
	}
	if p.MaxDispatchesPerSecond != nil {
		u.RateLimits.MaxDispatchesPerSecond = *p.MaxDispatchesPerSecond
	}
	if p.MaxBurstSize != nil {
		u.RateLimits.MaxBurstSize = *p.MaxBurstSize
	}
	if p.MaxConcurrentDispatches != nil {
		u.RateLimits.MaxConcurrentDispatches = *p.MaxConcurrentDispatches
	}
	if p.RetryMaxAttempts != nil {
		u.Retry.MaxAttempts = *p.RetryMaxAttempts
	}
	if p.RetryMaxRetryDuration != nil {
		u.Retry.MaxRetryDuration = *p.RetryMaxRetryDuration
	}
	if p.RetryMinBackoff != nil {
		u.Retry.MinBackoff = *p.RetryMinBackoff
	}
	if p.RetryMaxBackoff != nil {
		u.Retry.MaxBackoff = *p.RetryMaxBackoff
	}
	if p.RetryMaxDoublings != nil {
		u.Retry.MaxDoublings = *p.RetryMaxDoublings
	}
	if p.TargetBaseURL != nil {
		u.Target.BaseURL = *p.TargetBaseURL
	}
	if p.TargetMethod != nil {
		u.Target.Method = *p.TargetMethod
	}
	if p.TargetHeaderOverrides != nil {
		u.Target.HeaderOverrides = *p.TargetHeaderOverrides
	}
	if p.SigningSecret != nil {
		u.Target.SigningSecret = *p.SigningSecret
	}
	if p.DeadLetterQueue != nil {
		u.DeadLetterQueue = *p.DeadLetterQueue
	}

	if p.Validate != nil {
		if err := p.Validate(u); err != nil {
			return store.Queue{}, err
		}
	}
	if sameConfig(q, u) && state == q.State {
		return q, nil
	}

	headers, err := jsonOrNil(u.Target.HeaderOverrides)
	if err != nil {
		return store.Queue{}, err
	}
	var burst any
	if u.RateLimits.MaxBurstSize > 0 {
		burst = u.RateLimits.MaxBurstSize
	}
	_, err = tx.ExecContext(ctx, `UPDATE queues SET
		state=?, max_dispatches_per_second=?, max_burst_size=?, max_concurrent_dispatches=?,
		retry_max_attempts=?, retry_max_duration_ms=?, retry_min_backoff_ms=?, retry_max_backoff_ms=?, retry_max_doublings=?,
		target_base_url=?, target_method=?, target_headers=?, signing_secret=?, dead_letter_queue=?,
		config_version=config_version+1, updated_at=? WHERE name=?`,
		string(state),
		u.RateLimits.MaxDispatchesPerSecond, burst, u.RateLimits.MaxConcurrentDispatches,
		u.Retry.MaxAttempts, u.Retry.MaxRetryDuration.Milliseconds(), u.Retry.MinBackoff.Milliseconds(),
		u.Retry.MaxBackoff.Milliseconds(), u.Retry.MaxDoublings,
		nullableStr(u.Target.BaseURL), nullableStr(u.Target.Method), headers,
		nullableStr(u.Target.SigningSecret), nullableStr(u.DeadLetterQueue),
		ms(now), name)
	if err != nil {
		return store.Queue{}, err
	}
	out, err := s.getQueueTx(ctx, tx, name)
	if err != nil {
		return out, err
	}
	if err := tx.Commit(); err != nil {
		return out, err
	}
	s.emit(store.Event{Kind: store.EventQueueChanged, QueueID: out.ID, Queue: out.Name})
	return out, nil
}

func (s *Store) ListQueues(ctx context.Context, opts store.ListQueuesOpts) ([]store.Queue, string, error) {
	size := opts.PageSize
	if size <= 0 || size > 1000 {
		if size <= 0 {
			size = 100
		} else {
			size = 1000
		}
	}
	var after string
	if opts.PageToken != "" {
		b, err := base64.RawURLEncoding.DecodeString(opts.PageToken)
		if err != nil {
			return nil, "", fmt.Errorf("%w: bad page_token", store.ErrFailedPrecondition)
		}
		after = string(b)
	}
	query := `SELECT ` + queueCols + ` FROM queues WHERE name > ?`
	args := []any{after}
	if opts.NamePrefix != "" {
		query += ` AND name LIKE ? ESCAPE '\'`
		args = append(args, escapeLike(opts.NamePrefix)+"%")
	}
	if opts.RootOnly {
		query += ` AND name NOT LIKE '%:%'`
	}
	query += ` ORDER BY name LIMIT ?`
	args = append(args, size+1)

	rows, err := s.read.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, "", err
	}
	defer rows.Close()
	var out []store.Queue
	for rows.Next() {
		q, err := scanQueue(rows)
		if err != nil {
			return nil, "", err
		}
		out = append(out, q)
	}
	if err := rows.Err(); err != nil {
		return nil, "", err
	}
	next := ""
	if len(out) > size {
		out = out[:size]
		next = base64.RawURLEncoding.EncodeToString([]byte(out[len(out)-1].Name))
	}
	return out, next, nil
}

func (s *Store) DeleteQueue(ctx context.Context, name string) error {
	res, err := s.write.ExecContext(ctx, `DELETE FROM queues WHERE name = ?`, name)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return store.ErrNotFound
	}
	s.emit(store.Event{Kind: store.EventQueueChanged, Queue: name})
	return nil
}

func (s *Store) SetQueueState(ctx context.Context, name string, state store.QueueState, now time.Time) (store.Queue, error) {
	tx, err := s.write.BeginTx(ctx, nil)
	if err != nil {
		return store.Queue{}, err
	}
	defer tx.Rollback()

	q, err := s.getQueueTx(ctx, tx, name)
	if err != nil {
		return store.Queue{}, err
	}
	if state == store.QueuePaused && q.State == store.QueueDisabled {
		return store.Queue{}, fmt.Errorf("%w: cannot pause a disabled queue", store.ErrFailedPrecondition)
	}
	if q.State == state {
		return q, nil
	}
	if _, err := tx.ExecContext(ctx,
		`UPDATE queues SET state=?, config_version=config_version+1, updated_at=? WHERE name=?`,
		string(state), ms(now), name); err != nil {
		return store.Queue{}, err
	}
	out, err := s.getQueueTx(ctx, tx, name)
	if err != nil {
		return out, err
	}
	if err := tx.Commit(); err != nil {
		return out, err
	}
	s.emit(store.Event{Kind: store.EventQueueChanged, QueueID: out.ID, Queue: out.Name})
	return out, nil
}

func (s *Store) PurgeQueue(ctx context.Context, name string, now time.Time) (store.Queue, error) {
	tx, err := s.write.BeginTx(ctx, nil)
	if err != nil {
		return store.Queue{}, err
	}
	defer tx.Rollback()
	res, err := tx.ExecContext(ctx,
		// Cover every existing task even if the clock stepped backwards
		// since it was created (CreateTasks stamps later tasks after this).
		`UPDATE queues SET
			purged_at = MAX(?, COALESCE((SELECT MAX(t.created_at) FROM tasks t WHERE t.queue_id = queues.id), 0)),
			config_version=config_version+1, updated_at=? WHERE name=?`,
		ms(now), ms(now), name)
	if err != nil {
		return store.Queue{}, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return store.Queue{}, store.ErrNotFound
	}
	q, err := s.getQueueTx(ctx, tx, name)
	if err != nil {
		return q, err
	}
	if err := tx.Commit(); err != nil {
		return q, err
	}
	s.emit(store.Event{Kind: store.EventQueueChanged, QueueID: q.ID, Queue: q.Name})
	return q, nil
}

func (s *Store) QueueStats(ctx context.Context, name, idPrefix string, now time.Time) (store.QueueStats, error) {
	q, err := s.GetQueue(ctx, name)
	if err != nil {
		return store.QueueStats{}, err
	}
	var purgedMS int64 = -1
	if q.PurgedAt != nil {
		purgedMS = ms(*q.PurgedAt)
	}

	var st store.QueueStats
	query := `SELECT
		COALESCE(SUM(CASE WHEN state='pending' AND schedule_time <= ? THEN 1 ELSE 0 END),0),
		COALESCE(SUM(CASE WHEN state='leased' THEN 1 ELSE 0 END),0),
		COALESCE(SUM(CASE WHEN state='succeeded' THEN 1 ELSE 0 END),0),
		COALESCE(SUM(CASE WHEN state='failed' THEN 1 ELSE 0 END),0),
		COALESCE(SUM(CASE WHEN state='pending' AND schedule_time > ? THEN 1 ELSE 0 END),0),
		MIN(CASE WHEN state='pending' AND schedule_time <= ? THEN schedule_time END)
		FROM tasks WHERE queue_id=? AND state != 'deleted' AND created_at > ?`
	args := []any{ms(now), ms(now), ms(now), q.ID, purgedMS}
	if idPrefix != "" {
		query += ` AND task_id LIKE ? ESCAPE '\'`
		args = append(args, escapeLike(idPrefix)+"%")
	}
	var oldest sql.NullInt64
	if err := s.read.QueryRowContext(ctx, query, args...).Scan(
		&st.Pending, &st.Running, &st.Succeeded, &st.Failed, &st.ScheduledFuture, &oldest); err != nil {
		return st, err
	}
	st.OldestDueAt = scanNullableMS(oldest)

	m1 := ms(now.Add(-time.Minute))
	h1 := ms(now.Add(-time.Hour))
	aq := `SELECT
		COALESCE(SUM(CASE WHEN a.dispatch_time > ? THEN 1 ELSE 0 END),0),
		COALESCE(SUM(CASE WHEN a.dispatch_time > ? AND NOT (COALESCE(a.response_status,0) BETWEEN 200 AND 299) THEN 1 ELSE 0 END),0),
		COUNT(*),
		COALESCE(SUM(CASE WHEN NOT (COALESCE(a.response_status,0) BETWEEN 200 AND 299) THEN 1 ELSE 0 END),0)
		FROM task_attempts a`
	aargs := []any{m1, m1}
	if idPrefix != "" {
		aq += ` JOIN tasks t ON t.id = a.task_pk`
	}
	aq += ` WHERE a.queue_id=? AND a.dispatch_time > ?`
	aargs = append(aargs, q.ID, h1)
	if idPrefix != "" {
		aq += ` AND t.task_id LIKE ? ESCAPE '\'`
		aargs = append(aargs, escapeLike(idPrefix)+"%")
	}
	if err := s.read.QueryRowContext(ctx, aq, aargs...).Scan(
		&st.Dispatched1m, &st.Errors1m, &st.Dispatched1h, &st.Errors1h); err != nil {
		return st, err
	}
	return st, nil
}

func escapeLike(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `%`, `\%`)
	s = strings.ReplaceAll(s, `_`, `\_`)
	return s
}
