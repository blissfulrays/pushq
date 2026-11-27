package postgres

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

type querier interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

func scanQueue(r rowScanner) (store.Queue, error) {
	var q store.Queue
	var burst sql.NullInt64
	var maxDurMS, minBackMS, maxBackMS int64
	var baseURL, method, headersJSON, secret, dlq sql.NullString
	var purged sql.NullTime
	var created, updated time.Time
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
	q.PurgedAt = scanNullableTime(purged)
	q.CreatedAt = created.UTC()
	q.UpdatedAt = updated.UTC()
	return q, nil
}

func getQueue(ctx context.Context, db querier, name string, forUpdate bool) (store.Queue, error) {
	suffix := ""
	if forUpdate {
		suffix = " FOR UPDATE"
	}
	q, err := scanQueue(db.QueryRowContext(ctx, `SELECT `+queueCols+` FROM queues WHERE name = $1`+suffix, name))
	if err == sql.ErrNoRows {
		return q, store.ErrNotFound
	}
	return q, err
}

func (s *Store) GetQueue(ctx context.Context, name string) (store.Queue, error) {
	return getQueue(ctx, s.db, name, false)
}

func (s *Store) GetQueuesByID(ctx context.Context, ids []int64) ([]store.Queue, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	ph := make([]string, len(ids))
	args := make([]any, len(ids))
	for i, id := range ids {
		ph[i] = fmt.Sprintf("$%d", i+1)
		args[i] = id
	}
	rows, err := s.db.QueryContext(ctx, `SELECT `+queueCols+` FROM queues WHERE id IN (`+strings.Join(ph, ",")+`)`, args...)
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

const insertQueueSQL = `INSERT INTO queues (
name, max_dispatches_per_second, max_burst_size, max_concurrent_dispatches,
retry_max_attempts, retry_max_duration_ms, retry_min_backoff_ms, retry_max_backoff_ms, retry_max_doublings,
target_base_url, target_method, target_headers, signing_secret, dead_letter_queue,
created_at, updated_at) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16)`

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
		now.UTC(), now.UTC(),
	}, nil
}

func (s *Store) CreateQueue(ctx context.Context, u store.QueueUpsert, now time.Time) (store.Queue, error) {
	args, err := insertQueueArgs(u, now)
	if err != nil {
		return store.Queue{}, err
	}
	if _, err := s.db.ExecContext(ctx, insertQueueSQL, args...); err != nil {
		if isUniqueViolation(err) {
			return store.Queue{}, store.ErrAlreadyExists
		}
		return store.Queue{}, err
	}
	q, err := getQueue(ctx, s.db, u.Name, false)
	if err != nil {
		return q, err
	}
	notify(ctx, s.db, store.Event{Kind: store.EventQueueChanged, QueueID: q.ID, Queue: q.Name})
	return q, nil
}

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
	for attempt := 0; ; attempt++ {
		q, outcome, err := s.upsertOnce(ctx, u, now)
		// Concurrent create can race our insert; retry once as an update.
		if err != nil && isUniqueViolation(err) && attempt == 0 {
			continue
		}
		return q, outcome, err
	}
}

func (s *Store) upsertOnce(ctx context.Context, u store.QueueUpsert, now time.Time) (store.Queue, store.UpsertOutcome, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return store.Queue{}, 0, err
	}
	defer tx.Rollback()

	existing, err := getQueue(ctx, tx, u.Name, true)
	if err == store.ErrNotFound {
		args, aerr := insertQueueArgs(u, now)
		if aerr != nil {
			return store.Queue{}, 0, aerr
		}
		if _, err := tx.ExecContext(ctx, insertQueueSQL, args...); err != nil {
			return store.Queue{}, 0, err
		}
		q, err := getQueue(ctx, tx, u.Name, false)
		if err != nil {
			return q, 0, err
		}
		notify(ctx, tx, store.Event{Kind: store.EventQueueChanged, QueueID: q.ID, Queue: q.Name})
		if err := tx.Commit(); err != nil {
			return q, 0, err
		}
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

	if err := updateQueueConfig(ctx, tx, u, string(existing.State), now); err != nil {
		return store.Queue{}, 0, err
	}
	q, err := getQueue(ctx, tx, u.Name, false)
	if err != nil {
		return q, 0, err
	}
	notify(ctx, tx, store.Event{Kind: store.EventQueueChanged, QueueID: q.ID, Queue: q.Name})
	if err := tx.Commit(); err != nil {
		return q, 0, err
	}
	return q, store.UpsertUpdated, nil
}

func updateQueueConfig(ctx context.Context, tx querier, u store.QueueUpsert, state string, now time.Time) error {
	headers, err := jsonOrNil(u.Target.HeaderOverrides)
	if err != nil {
		return err
	}
	var burst any
	if u.RateLimits.MaxBurstSize > 0 {
		burst = u.RateLimits.MaxBurstSize
	}
	_, err = tx.ExecContext(ctx, `UPDATE queues SET
		state=$1, max_dispatches_per_second=$2, max_burst_size=$3, max_concurrent_dispatches=$4,
		retry_max_attempts=$5, retry_max_duration_ms=$6, retry_min_backoff_ms=$7, retry_max_backoff_ms=$8, retry_max_doublings=$9,
		target_base_url=$10, target_method=$11, target_headers=$12, signing_secret=$13, dead_letter_queue=$14,
		config_version=config_version+1, updated_at=$15 WHERE name=$16`,
		state,
		u.RateLimits.MaxDispatchesPerSecond, burst, u.RateLimits.MaxConcurrentDispatches,
		u.Retry.MaxAttempts, u.Retry.MaxRetryDuration.Milliseconds(), u.Retry.MinBackoff.Milliseconds(),
		u.Retry.MaxBackoff.Milliseconds(), u.Retry.MaxDoublings,
		nullableStr(u.Target.BaseURL), nullableStr(u.Target.Method), headers,
		nullableStr(u.Target.SigningSecret), nullableStr(u.DeadLetterQueue),
		now.UTC(), u.Name)
	return err
}

func (s *Store) PatchQueue(ctx context.Context, name string, p store.QueuePatch, now time.Time) (store.Queue, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return store.Queue{}, err
	}
	defer tx.Rollback()

	q, err := getQueue(ctx, tx, name, true)
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
	if err := updateQueueConfig(ctx, tx, u, string(state), now); err != nil {
		return store.Queue{}, err
	}
	out, err := getQueue(ctx, tx, name, false)
	if err != nil {
		return out, err
	}
	notify(ctx, tx, store.Event{Kind: store.EventQueueChanged, QueueID: out.ID, Queue: out.Name})
	if err := tx.Commit(); err != nil {
		return out, err
	}
	return out, nil
}

func (s *Store) ListQueues(ctx context.Context, opts store.ListQueuesOpts) ([]store.Queue, string, error) {
	size := opts.PageSize
	if size <= 0 {
		size = 100
	} else if size > 1000 {
		size = 1000
	}
	var after string
	if opts.PageToken != "" {
		b, err := base64.RawURLEncoding.DecodeString(opts.PageToken)
		if err != nil {
			return nil, "", fmt.Errorf("%w: bad page_token", store.ErrFailedPrecondition)
		}
		after = string(b)
	}
	query := `SELECT ` + queueCols + ` FROM queues WHERE name > $1`
	args := []any{after}
	if opts.NamePrefix != "" {
		query += ` AND name LIKE $2 ESCAPE '\'`
		args = append(args, escapeLike(opts.NamePrefix)+"%")
	}
	query += fmt.Sprintf(` ORDER BY name LIMIT $%d`, len(args)+1)
	args = append(args, size+1)

	rows, err := s.db.QueryContext(ctx, query, args...)
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
	res, err := s.db.ExecContext(ctx, `DELETE FROM queues WHERE name = $1`, name)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return store.ErrNotFound
	}
	notify(ctx, s.db, store.Event{Kind: store.EventQueueChanged, Queue: name})
	return nil
}

func (s *Store) SetQueueState(ctx context.Context, name string, state store.QueueState, now time.Time) (store.Queue, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return store.Queue{}, err
	}
	defer tx.Rollback()

	q, err := getQueue(ctx, tx, name, true)
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
		`UPDATE queues SET state=$1, config_version=config_version+1, updated_at=$2 WHERE name=$3`,
		string(state), now.UTC(), name); err != nil {
		return store.Queue{}, err
	}
	out, err := getQueue(ctx, tx, name, false)
	if err != nil {
		return out, err
	}
	notify(ctx, tx, store.Event{Kind: store.EventQueueChanged, QueueID: out.ID, Queue: out.Name})
	if err := tx.Commit(); err != nil {
		return out, err
	}
	return out, nil
}

func (s *Store) PurgeQueue(ctx context.Context, name string, now time.Time) (store.Queue, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return store.Queue{}, err
	}
	defer tx.Rollback()
	// The watermark covers every task that exists now, even ones stamped by
	// a replica whose clock runs ahead of this one; CreateTasks in turn
	// stamps later tasks strictly after it (see queueRef.createdAt).
	res, err := tx.ExecContext(ctx,
		`UPDATE queues SET
			purged_at = GREATEST($1, (SELECT max(t.created_at) FROM tasks t WHERE t.queue_id = queues.id)),
			config_version=config_version+1, updated_at=$1
		WHERE name=$2`,
		now.UTC(), name)
	if err != nil {
		return store.Queue{}, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return store.Queue{}, store.ErrNotFound
	}
	q, err := getQueue(ctx, tx, name, false)
	if err != nil {
		return q, err
	}
	notify(ctx, tx, store.Event{Kind: store.EventQueueChanged, QueueID: q.ID, Queue: q.Name})
	if err := tx.Commit(); err != nil {
		return q, err
	}
	return q, nil
}

func (s *Store) QueueStats(ctx context.Context, name, idPrefix string, now time.Time) (store.QueueStats, error) {
	q, err := s.GetQueue(ctx, name)
	if err != nil {
		return store.QueueStats{}, err
	}
	purged := nullableTime(q.PurgedAt)

	var st store.QueueStats
	query := `SELECT
		COALESCE(SUM(CASE WHEN state='pending' AND schedule_time <= $1 THEN 1 ELSE 0 END),0),
		COALESCE(SUM(CASE WHEN state='leased' THEN 1 ELSE 0 END),0),
		COALESCE(SUM(CASE WHEN state='succeeded' THEN 1 ELSE 0 END),0),
		COALESCE(SUM(CASE WHEN state='failed' THEN 1 ELSE 0 END),0),
		COALESCE(SUM(CASE WHEN state='pending' AND schedule_time > $1 THEN 1 ELSE 0 END),0),
		MIN(CASE WHEN state='pending' AND schedule_time <= $1 THEN schedule_time END)
		FROM tasks WHERE queue_id=$2 AND state != 'deleted'
		AND ($3::timestamptz IS NULL OR created_at > $3)`
	args := []any{now.UTC(), q.ID, purged}
	if idPrefix != "" {
		query += ` AND task_id LIKE $4 ESCAPE '\'`
		args = append(args, escapeLike(idPrefix)+"%")
	}
	var oldest sql.NullTime
	if err := s.db.QueryRowContext(ctx, query, args...).Scan(
		&st.Pending, &st.Running, &st.Succeeded, &st.Failed, &st.ScheduledFuture, &oldest); err != nil {
		return st, err
	}
	st.OldestDueAt = scanNullableTime(oldest)

	aq := `SELECT
		COALESCE(SUM(CASE WHEN a.dispatch_time > $1 THEN 1 ELSE 0 END),0),
		COALESCE(SUM(CASE WHEN a.dispatch_time > $1 AND NOT (COALESCE(a.response_status,0) BETWEEN 200 AND 299) THEN 1 ELSE 0 END),0),
		COUNT(*),
		COALESCE(SUM(CASE WHEN NOT (COALESCE(a.response_status,0) BETWEEN 200 AND 299) THEN 1 ELSE 0 END),0)
		FROM task_attempts a`
	aargs := []any{now.Add(-time.Minute).UTC()}
	if idPrefix != "" {
		aq += ` JOIN tasks t ON t.id = a.task_pk`
	}
	aq += ` WHERE a.queue_id=$2 AND a.dispatch_time > $3`
	aargs = append(aargs, q.ID, now.Add(-time.Hour).UTC())
	if idPrefix != "" {
		aq += ` AND t.task_id LIKE $4 ESCAPE '\'`
		aargs = append(aargs, escapeLike(idPrefix)+"%")
	}
	if err := s.db.QueryRowContext(ctx, aq, aargs...).Scan(
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
