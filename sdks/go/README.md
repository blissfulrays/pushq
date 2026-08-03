# pushq (Go)

Go client for [pushq](https://github.com/blissfulrays/pushq) — a self-hosted HTTP
push task queue (open-source Cloud Tasks alternative). Stdlib only.

```bash
go get github.com/blissfulrays/pushq/sdks/go
```

```go
import (
    "context"
    pushq "github.com/blissfulrays/pushq/sdks/go"
)

pq := pushq.New("http://localhost:8080", "pq_...")
ctx := context.Background()

// Queues are created/updated at runtime — idempotent.
pq.UpsertQueue(ctx, "account-42", pushq.QueueConfig{
    RateLimits: &pushq.RateLimits{MaxDispatchesPerSecond: pushq.Float64(10)},
    Target:     &pushq.Target{BaseURL: "https://api.example.com", SigningSecret: "whsec_..."},
})

pq.CreateTask(ctx, "account-42", pushq.TaskCreate{
    Path:         "/hooks/send-message",
    JSONBody:     map[string]any{"user_id": 1},
    DelaySeconds: pushq.Float64(3600),
    TaskID:       "follow_up_abc",                       // dedup
    Retry:        &pushq.Retry{MaxAttempts: pushq.Int(3)}, // per-task override
})
```

## Verify deliveries

```go
func handler(w http.ResponseWriter, r *http.Request) {
    body, _ := io.ReadAll(r.Body)
    id, err := pushq.VerifySignature("whsec_...", r.Header, body, 5*time.Minute)
    if err != nil {
        w.WriteHeader(http.StatusUnauthorized)
        return
    }
    _ = id // "queue/task_id" — stable across retries; use as idempotency key
}
```

Errors are `*pushq.Error` with `Status`, `Code`, `Detail`;
`pushq.IsCode(err, "already_exists")` for branching.
