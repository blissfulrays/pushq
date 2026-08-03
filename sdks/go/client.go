// Package pushq is the Go client for pushq — a self-hosted HTTP push task
// queue (open-source Cloud Tasks alternative). Stdlib only.
package pushq

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// Error is returned for any non-2xx API response (RFC 9457 problem+json).
type Error struct {
	Status int
	Code   string // already_exists, not_found, failed_precondition, …
	Detail string
}

func (e *Error) Error() string {
	return fmt.Sprintf("pushq: %s (%d): %s", e.Code, e.Status, e.Detail)
}

// IsCode reports whether err is a pushq *Error with the given code.
func IsCode(err error, code string) bool {
	var pe *Error
	ok := asErr(err, &pe)
	return ok && pe.Code == code
}

func asErr(err error, target **Error) bool {
	for err != nil {
		if pe, ok := err.(*Error); ok {
			*target = pe
			return true
		}
		u, ok := err.(interface{ Unwrap() error })
		if !ok {
			return false
		}
		err = u.Unwrap()
	}
	return false
}

type Client struct {
	baseURL string
	apiKey  string
	http    *http.Client
}

type Option func(*Client)

func WithHTTPClient(h *http.Client) Option { return func(c *Client) { c.http = h } }

func New(baseURL, apiKey string, opts ...Option) *Client {
	c := &Client{
		baseURL: strings.TrimRight(baseURL, "/"),
		apiKey:  apiKey,
		http:    &http.Client{Timeout: 30 * time.Second},
	}
	for _, o := range opts {
		o(c)
	}
	return c
}

// --- queues ---

// UpsertQueue creates or replaces queue config (idempotent; never changes run
// state; keeps a stored signing secret unless Target.ClearSigningSecret or a
// new secret is set).
func (c *Client) UpsertQueue(ctx context.Context, name string, cfg QueueConfig) (*Queue, error) {
	var out Queue
	if err := c.do(ctx, "PUT", "/v1/queues/"+esc(name), queueConfigWire(cfg, ""), &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *Client) CreateQueue(ctx context.Context, name string, cfg QueueConfig) (*Queue, error) {
	body := queueConfigWire(cfg, "")
	body["name"] = name
	var out Queue
	if err := c.do(ctx, "POST", "/v1/queues", body, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *Client) GetQueue(ctx context.Context, name string) (*Queue, error) {
	var out Queue
	if err := c.do(ctx, "GET", "/v1/queues/"+esc(name), nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// PatchQueue updates only provided fields; state may be
// "running"/"paused"/"disabled".
func (c *Client) PatchQueue(ctx context.Context, name string, cfg QueueConfig, state string) (*Queue, error) {
	var out Queue
	if err := c.do(ctx, "PATCH", "/v1/queues/"+esc(name), queueConfigWire(cfg, state), &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// ListQueues requires an admin-role key.
func (c *Client) ListQueues(ctx context.Context, p ListQueuesParams) ([]Queue, string, error) {
	q := url.Values{}
	setIf(q, "name_prefix", p.NamePrefix)
	if p.PageSize > 0 {
		q.Set("page_size", strconv.Itoa(p.PageSize))
	}
	setIf(q, "page_token", p.PageToken)
	var out struct {
		Queues        []Queue `json:"queues"`
		NextPageToken string  `json:"next_page_token"`
	}
	if err := c.do(ctx, "GET", withQuery("/v1/queues", q), nil, &out); err != nil {
		return nil, "", err
	}
	return out.Queues, out.NextPageToken, nil
}

func (c *Client) DeleteQueue(ctx context.Context, name string) error {
	return c.do(ctx, "DELETE", "/v1/queues/"+esc(name), nil, nil)
}

func (c *Client) PauseQueue(ctx context.Context, name string) (*Queue, error) {
	var out Queue
	if err := c.do(ctx, "POST", "/v1/queues/"+esc(name)+":pause", nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *Client) ResumeQueue(ctx context.Context, name string) (*Queue, error) {
	var out Queue
	if err := c.do(ctx, "POST", "/v1/queues/"+esc(name)+":resume", nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// PurgeQueue deletes every task created up to now (exact watermark).
func (c *Client) PurgeQueue(ctx context.Context, name string) (*Queue, error) {
	var out Queue
	if err := c.do(ctx, "POST", "/v1/queues/"+esc(name)+":purge", nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *Client) QueueStats(ctx context.Context, name, idPrefix string) (*QueueStats, error) {
	q := url.Values{}
	setIf(q, "id_prefix", idPrefix)
	var out QueueStats
	if err := c.do(ctx, "GET", withQuery("/v1/queues/"+esc(name)+"/stats", q), nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// --- tasks ---

func (c *Client) CreateTask(ctx context.Context, queue string, t TaskCreate) (*Task, error) {
	var out Task
	if err := c.do(ctx, "POST", "/v1/queues/"+esc(queue)+"/tasks", taskCreateWire(t), &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// CreateTasks enqueues up to 500 tasks; non-atomic, per-item results in order.
func (c *Client) CreateTasks(ctx context.Context, queue string, tasks []TaskCreate) ([]BatchResult, error) {
	wire := make([]map[string]any, len(tasks))
	for i, t := range tasks {
		wire[i] = taskCreateWire(t)
	}
	var out struct {
		Results []BatchResult `json:"results"`
	}
	if err := c.do(ctx, "POST", "/v1/queues/"+esc(queue)+"/tasks:batch", map[string]any{"tasks": wire}, &out); err != nil {
		return nil, err
	}
	return out.Results, nil
}

func (c *Client) GetTask(ctx context.Context, queue, id string) (*Task, error) {
	return c.getTask(ctx, queue, id, "basic")
}

// GetTaskFull includes headers and body.
func (c *Client) GetTaskFull(ctx context.Context, queue, id string) (*Task, error) {
	return c.getTask(ctx, queue, id, "full")
}

func (c *Client) getTask(ctx context.Context, queue, id, view string) (*Task, error) {
	var out Task
	if err := c.do(ctx, "GET", "/v1/queues/"+esc(queue)+"/tasks/"+esc(id)+"?view="+view, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *Client) ListTasks(ctx context.Context, queue string, p ListTasksParams) ([]Task, string, error) {
	q := url.Values{}
	setIf(q, "state", p.State)
	setIf(q, "id_prefix", p.IDPrefix)
	setIf(q, "view", p.View)
	if p.PageSize > 0 {
		q.Set("page_size", strconv.Itoa(p.PageSize))
	}
	setIf(q, "page_token", p.PageToken)
	var out struct {
		Tasks         []Task `json:"tasks"`
		NextPageToken string `json:"next_page_token"`
	}
	if err := c.do(ctx, "GET", withQuery("/v1/queues/"+esc(queue)+"/tasks", q), nil, &out); err != nil {
		return nil, "", err
	}
	return out.Tasks, out.NextPageToken, nil
}

func (c *Client) DeleteTask(ctx context.Context, queue, id string) error {
	return c.do(ctx, "DELETE", "/v1/queues/"+esc(queue)+"/tasks/"+esc(id), nil, nil)
}

// RunTask forces immediate dispatch, bypassing rate limits and PAUSED state.
func (c *Client) RunTask(ctx context.Context, queue, id string) (*Task, error) {
	var out Task
	if err := c.do(ctx, "POST", "/v1/queues/"+esc(queue)+"/tasks/"+esc(id)+":run", nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *Client) ListAttempts(ctx context.Context, queue, id string) ([]Attempt, error) {
	var out struct {
		Attempts []Attempt `json:"attempts"`
	}
	if err := c.do(ctx, "GET", "/v1/queues/"+esc(queue)+"/tasks/"+esc(id)+"/attempts", nil, &out); err != nil {
		return nil, err
	}
	return out.Attempts, nil
}

// --- plumbing ---

func (c *Client) do(ctx context.Context, method, path string, body any, out any) error {
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, rd)
	if err != nil {
		return err
	}
	if c.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.apiKey)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode >= 300 {
		pe := &Error{Status: resp.StatusCode, Code: "internal", Detail: strings.TrimSpace(string(data))}
		var prob struct {
			Code   string `json:"code"`
			Detail string `json:"detail"`
			Title  string `json:"title"`
		}
		if json.Unmarshal(data, &prob) == nil && prob.Code != "" {
			pe.Code = prob.Code
			if prob.Detail != "" {
				pe.Detail = prob.Detail
			} else {
				pe.Detail = prob.Title
			}
		}
		return pe
	}
	if out == nil || len(data) == 0 {
		return nil
	}
	return json.Unmarshal(data, out)
}

func queueConfigWire(cfg QueueConfig, state string) map[string]any {
	m := map[string]any{}
	if state != "" {
		m["state"] = state
	}
	if cfg.RateLimits != nil {
		m["rate_limits"] = cfg.RateLimits
	}
	if cfg.Retry != nil {
		m["retry"] = cfg.Retry
	}
	if cfg.Target != nil {
		t := map[string]any{}
		if cfg.Target.BaseURL != "" {
			t["base_url"] = cfg.Target.BaseURL
		}
		if cfg.Target.Method != "" {
			t["method"] = cfg.Target.Method
		}
		if len(cfg.Target.Headers) > 0 {
			t["headers"] = cfg.Target.Headers
		}
		if cfg.Target.ClearSigningSecret {
			t["signing_secret"] = ""
		} else if cfg.Target.SigningSecret != "" {
			t["signing_secret"] = cfg.Target.SigningSecret
		}
		m["target"] = t
	}
	if cfg.DeadLetterQueue != "" {
		m["dead_letter_queue"] = cfg.DeadLetterQueue
	}
	return m
}

func taskCreateWire(t TaskCreate) map[string]any {
	m := map[string]any{}
	set := func(k string, v any) { m[k] = v }
	if t.TaskID != "" {
		set("task_id", t.TaskID)
	}
	if t.URL != "" {
		set("url", t.URL)
	}
	if t.Path != "" {
		set("path", t.Path)
	}
	if t.Method != "" {
		set("method", t.Method)
	}
	if len(t.Headers) > 0 {
		set("headers", t.Headers)
	}
	if len(t.Body) > 0 {
		set("body_base64", base64.StdEncoding.EncodeToString(t.Body))
	}
	if t.JSONBody != nil {
		set("json_body", t.JSONBody)
	}
	if t.ScheduleTime != nil {
		set("schedule_time", t.ScheduleTime.UTC().Format(time.RFC3339Nano))
	}
	if t.DelaySeconds != nil {
		set("delay_seconds", *t.DelaySeconds)
	}
	if t.DispatchDeadlineSeconds != nil {
		set("dispatch_deadline_seconds", *t.DispatchDeadlineSeconds)
	}
	if t.Retry != nil {
		set("retry", t.Retry)
	}
	return m
}

func esc(s string) string { return url.PathEscape(s) }

func setIf(q url.Values, k, v string) {
	if v != "" {
		q.Set(k, v)
	}
}

func withQuery(path string, q url.Values) string {
	if len(q) == 0 {
		return path
	}
	return path + "?" + q.Encode()
}
