package main

// Client subcommands: operate a running pushq server from the terminal.
// Server address via --server / PUSHQ_SERVER, key via --api-key / PUSHQ_API_KEY.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

type apiClient struct {
	server string
	key    string
}

func clientFlags(cmd *cobra.Command) *apiClient {
	c := &apiClient{}
	cmd.PersistentFlags().StringVar(&c.server, "server", envOr("PUSHQ_SERVER", "http://localhost:8080"), "pushq server URL")
	cmd.PersistentFlags().StringVar(&c.key, "api-key", os.Getenv("PUSHQ_API_KEY"), "API key (or PUSHQ_API_KEY)")
	return c
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func (c *apiClient) call(method, path string, body any) error {
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, strings.TrimRight(c.server, "/")+path, rd)
	if err != nil {
		return err
	}
	if c.key != "" {
		req.Header.Set("Authorization", "Bearer "+c.key)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := (&http.Client{Timeout: 30 * time.Second}).Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)

	if resp.StatusCode >= 300 {
		var prob struct {
			Code   string `json:"code"`
			Detail string `json:"detail"`
		}
		if json.Unmarshal(data, &prob) == nil && prob.Code != "" {
			return fmt.Errorf("%s (%d): %s", prob.Code, resp.StatusCode, prob.Detail)
		}
		return fmt.Errorf("HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(data)))
	}
	if len(data) == 0 {
		fmt.Println("ok")
		return nil
	}
	var pretty bytes.Buffer
	if json.Indent(&pretty, data, "", "  ") == nil {
		fmt.Println(pretty.String())
	} else {
		fmt.Println(string(data))
	}
	return nil
}

func prune(m map[string]any) map[string]any {
	for k, v := range m {
		switch x := v.(type) {
		case map[string]any:
			if p := prune(x); len(p) == 0 {
				delete(m, k)
			}
		case nil:
			delete(m, k)
		case string:
			if x == "" {
				delete(m, k)
			}
		}
	}
	return m
}

func queuesCmd() *cobra.Command {
	root := &cobra.Command{Use: "queues", Short: "Manage queues on a running server"}
	c := clientFlags(root)

	var prefix string
	ls := &cobra.Command{
		Use: "ls", Short: "List queues (admin)",
		RunE: func(cmd *cobra.Command, args []string) error {
			q := url.Values{}
			if prefix != "" {
				q.Set("name_prefix", prefix)
			}
			path := "/v1/queues"
			if len(q) > 0 {
				path += "?" + q.Encode()
			}
			return c.call("GET", path, nil)
		},
	}
	ls.Flags().StringVar(&prefix, "prefix", "", "name prefix filter")

	var rate float64
	var burst, concurrency, maxAttempts int
	var minBackoff, maxBackoff time.Duration
	var baseURL, methodOverride, dlq, secret string
	upsert := &cobra.Command{
		Use: "upsert NAME", Short: "Create or update a queue", Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			body := map[string]any{
				"rate_limits": map[string]any{},
				"retry":       map[string]any{},
				"target":      map[string]any{},
			}
			rl := body["rate_limits"].(map[string]any)
			if cmd.Flags().Changed("rate") {
				rl["max_dispatches_per_second"] = rate
			}
			if cmd.Flags().Changed("burst") {
				rl["max_burst_size"] = burst
			}
			if cmd.Flags().Changed("concurrency") {
				rl["max_concurrent_dispatches"] = concurrency
			}
			rc := body["retry"].(map[string]any)
			if cmd.Flags().Changed("max-attempts") {
				rc["max_attempts"] = maxAttempts
			}
			if cmd.Flags().Changed("min-backoff") {
				rc["min_backoff_seconds"] = minBackoff.Seconds()
			}
			if cmd.Flags().Changed("max-backoff") {
				rc["max_backoff_seconds"] = maxBackoff.Seconds()
			}
			tg := body["target"].(map[string]any)
			if baseURL != "" {
				tg["base_url"] = baseURL
			}
			if methodOverride != "" {
				tg["method"] = methodOverride
			}
			if cmd.Flags().Changed("signing-secret") {
				tg["signing_secret"] = secret
			}
			if dlq != "" {
				body["dead_letter_queue"] = dlq
			}
			return c.call("PUT", "/v1/queues/"+url.PathEscape(args[0]), prune(body))
		},
	}
	upsert.Flags().Float64Var(&rate, "rate", 0, "max dispatches per second")
	upsert.Flags().IntVar(&burst, "burst", 0, "max burst size (0 = auto)")
	upsert.Flags().IntVar(&concurrency, "concurrency", 0, "max concurrent dispatches")
	upsert.Flags().IntVar(&maxAttempts, "max-attempts", 0, "retry max attempts (-1 unlimited)")
	upsert.Flags().DurationVar(&minBackoff, "min-backoff", 0, "retry min backoff (e.g. 1s)")
	upsert.Flags().DurationVar(&maxBackoff, "max-backoff", 0, "retry max backoff (e.g. 5m)")
	upsert.Flags().StringVar(&baseURL, "base-url", "", "target base URL for relative task paths")
	upsert.Flags().StringVar(&methodOverride, "method", "", "override HTTP method for all tasks")
	upsert.Flags().StringVar(&secret, "signing-secret", "", `signing secret ("" clears; omit flag to keep)`)
	upsert.Flags().StringVar(&dlq, "dlq", "", "dead-letter queue name")

	one := func(use, short, method, suffix string) *cobra.Command {
		return &cobra.Command{
			Use: use + " NAME", Short: short, Args: cobra.ExactArgs(1),
			RunE: func(cmd *cobra.Command, args []string) error {
				return c.call(method, "/v1/queues/"+url.PathEscape(args[0])+suffix, nil)
			},
		}
	}

	var idPrefix string
	stats := &cobra.Command{
		Use: "stats NAME", Short: "Queue depth and error rates", Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			path := "/v1/queues/" + url.PathEscape(args[0]) + "/stats"
			if idPrefix != "" {
				path += "?id_prefix=" + url.QueryEscape(idPrefix)
			}
			return c.call("GET", path, nil)
		},
	}
	stats.Flags().StringVar(&idPrefix, "id-prefix", "", "scope counts to task IDs with this prefix")

	root.AddCommand(ls, upsert,
		one("get", "Show a queue", "GET", ""),
		one("pause", "Pause dispatching", "POST", ":pause"),
		one("resume", "Resume dispatching", "POST", ":resume"),
		one("purge", "Delete all tasks created up to now", "POST", ":purge"),
		one("rm", "Delete a queue (admin)", "DELETE", ""),
		stats,
	)
	return root
}

func tasksCmd() *cobra.Command {
	root := &cobra.Command{Use: "tasks", Short: "Manage tasks on a running server"}
	c := clientFlags(root)

	var state, idPrefix, view string
	ls := &cobra.Command{
		Use: "ls QUEUE", Short: "List tasks", Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			q := url.Values{}
			if state != "" {
				q.Set("state", state)
			}
			if idPrefix != "" {
				q.Set("id_prefix", idPrefix)
			}
			if view != "" {
				q.Set("view", view)
			}
			path := "/v1/queues/" + url.PathEscape(args[0]) + "/tasks"
			if len(q) > 0 {
				path += "?" + q.Encode()
			}
			return c.call("GET", path, nil)
		},
	}
	ls.Flags().StringVar(&state, "state", "", "pending|running|succeeded|failed")
	ls.Flags().StringVar(&idPrefix, "prefix", "", "task ID prefix filter")
	ls.Flags().StringVar(&view, "view", "", "basic|full")

	var taskID, urlFlag, pathFlag, method, jsonBody string
	var delay time.Duration
	var maxAttempts int
	create := &cobra.Command{
		Use: "create QUEUE", Short: "Enqueue a task", Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			body := map[string]any{
				"task_id": taskID, "url": urlFlag, "path": pathFlag, "method": method,
			}
			if jsonBody != "" {
				var v any
				if err := json.Unmarshal([]byte(jsonBody), &v); err != nil {
					return fmt.Errorf("--json is not valid JSON: %w", err)
				}
				body["json_body"] = v
			}
			if delay > 0 {
				body["delay_seconds"] = delay.Seconds()
			}
			if cmd.Flags().Changed("max-attempts") {
				body["retry"] = map[string]any{"max_attempts": maxAttempts}
			}
			return c.call("POST", "/v1/queues/"+url.PathEscape(args[0])+"/tasks", prune(body))
		},
	}
	create.Flags().StringVar(&taskID, "id", "", "task ID (dedup); omit for server-generated")
	create.Flags().StringVar(&urlFlag, "url", "", "absolute target URL")
	create.Flags().StringVar(&pathFlag, "path", "", "path resolved against queue base_url")
	create.Flags().StringVar(&method, "method", "", "HTTP method (default POST)")
	create.Flags().StringVar(&jsonBody, "json", "", "JSON body")
	create.Flags().DurationVar(&delay, "delay", 0, "delivery delay (e.g. 30s, 2h)")
	create.Flags().IntVar(&maxAttempts, "max-attempts", 0, "per-task retry override")

	two := func(use, short, method, suffix string) *cobra.Command {
		return &cobra.Command{
			Use: use + " QUEUE ID", Short: short, Args: cobra.ExactArgs(2),
			RunE: func(cmd *cobra.Command, args []string) error {
				return c.call(method, "/v1/queues/"+url.PathEscape(args[0])+"/tasks/"+url.PathEscape(args[1])+suffix, nil)
			},
		}
	}

	root.AddCommand(ls, create,
		two("get", "Show a task", "GET", "?view=full"),
		two("rm", "Delete a task", "DELETE", ""),
		two("run", "Force immediate dispatch", "POST", ":run"),
		two("attempts", "Show attempt history", "GET", "/attempts"),
	)
	return root
}
