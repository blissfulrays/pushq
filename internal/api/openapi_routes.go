package api

import (
	"github.com/blissfulrays/pushq/internal/api/types"
)

// Per-route documentation for the generated OpenAPI spec. Keys are
// "METHOD /path" exactly as registered on the router; TestOpenAPI fails if a
// route has no entry or an entry has no route. Request/Response hold a zero
// value of the Go type the handler decodes/encodes; its JSON schema is
// derived by reflection.

type access int

const (
	accessPublic access = iota
	accessRead
	accessWrite
	accessList
	accessDeleteQueue
	accessManage
	accessUser
	accessOperator
)

type queryParam struct {
	Name    string
	Doc     string
	Enum    []string
	Integer bool
}

type opDoc struct {
	Summary     string
	Description string
	Tag         string
	Access      access
	Request     any // nil = no body
	Response    any // nil = no body (204), unless Redirect or ContentType
	Status      int // success status; default 200 (or 204 with no Response)
	Query       []queryParam
	Redirect    bool   // browser flow answered with a 302
	ContentType string // non-JSON response (text/html, text/plain)
}

var (
	pageSize  = queryParam{Name: "page_size", Doc: "Items per page.", Integer: true}
	pageToken = queryParam{Name: "page_token", Doc: "`next_page_token` from the previous page."}
	taskView  = queryParam{Name: "view", Doc: "`full` includes headers and body.", Enum: []string{"basic", "full"}}
)

var routeDocs = map[string]opDoc{
	// --- system ---
	"GET /healthz": {Tag: "System", Summary: "Liveness check", Access: accessPublic,
		Description: "Returns `ok` while the process is up.", ContentType: "text/plain"},
	"GET /readyz": {Tag: "System", Summary: "Readiness check", Access: accessPublic, Response: types.ReadyResponse{},
		Description: "Fails with 503 while the database is unreachable. `role` is `leader` on the replica running the dispatcher."},
	"GET /metrics": {Tag: "System", Summary: "Prometheus metrics", Access: accessOperator, ContentType: "text/plain",
		Description: "Public when `metrics.public` is set in the server config."},
	"GET /openapi.json": {Tag: "System", Summary: "This OpenAPI document", Access: accessPublic, ContentType: "application/json",
		Description: "Generated from the server's routes at runtime."},
	"GET /docs": {Tag: "System", Summary: "Interactive API docs (Swagger UI)", Access: accessPublic, ContentType: "text/html"},
	"GET /openapi/console.json": {Tag: "System", Summary: "This OpenAPI document", Access: accessPublic, ContentType: "application/json",
		Description: "The console API, generated from the server's routes at runtime."},
	"GET /docs/console": {Tag: "System", Summary: "Interactive console API docs (Swagger UI)", Access: accessPublic, ContentType: "text/html"},
	"GET /v1/version":   {Tag: "System", Summary: "Server version", Access: accessRead, Response: types.VersionResponse{}},

	// --- queues ---
	"POST /v1/queues": {Tag: "Queues", Summary: "Create a queue", Access: accessWrite,
		Request: types.QueueCreate{}, Response: types.QueueResponse{}, Status: 201,
		Description: "Fails with `already_exists` if the name is taken. Use PUT to create-or-update."},
	"PUT /v1/queues/{queue}": {Tag: "Queues", Summary: "Create or replace a queue's configuration", Access: accessWrite,
		Request: types.QueuePut{}, Response: types.QueueResponse{},
		Description: "Idempotent; never changes the queue's run state. Answers 201 when created, 200 otherwise, " +
			"with `X-Pushq-Result: created | updated | unchanged`. Omit `target.signing_secret` to keep the stored one; send `\"\"` to clear it."},
	"GET /v1/queues/{queue}": {Tag: "Queues", Summary: "Get a queue", Access: accessRead, Response: types.QueueResponse{}},
	"PATCH /v1/queues/{queue}": {Tag: "Queues", Summary: "Update some of a queue's settings", Access: accessWrite,
		Request: types.QueuePatch{}, Response: types.QueueResponse{},
		Description: "Only the fields you send change. Can also set `state` (including `disabled`)."},
	"DELETE /v1/queues/{queue}": {Tag: "Queues", Summary: "Delete a queue and all its tasks", Access: accessDeleteQueue},
	"GET /v1/queues": {Tag: "Queues", Summary: "List queues", Access: accessList, Response: types.QueueList{},
		Query: []queryParam{{Name: "name_prefix", Doc: "Only names starting with this; `user/` lists a folder."}, pageSize, pageToken}},
	"POST /v1/queues/{queue}:pause": {Tag: "Queues", Summary: "Pause dispatching", Access: accessWrite, Response: types.QueueResponse{},
		Description: "Tasks are still accepted; nothing is delivered until resumed."},
	"POST /v1/queues/{queue}:resume": {Tag: "Queues", Summary: "Resume dispatching", Access: accessWrite, Response: types.QueueResponse{}},
	"POST /v1/queues/{queue}:purge": {Tag: "Queues", Summary: "Delete every task created up to now", Access: accessWrite,
		Response: types.QueueResponse{}, Status: 202,
		Description: "In-flight results are discarded and task IDs become reusable. Tasks created after the purge are unaffected."},
	"GET /v1/queues/{queue}/stats": {Tag: "Queues", Summary: "Live queue statistics", Access: accessRead,
		Response: types.QueueStatsResponse{}, Query: []queryParam{{Name: "id_prefix", Doc: "Only tasks whose ID starts with this."}}},

	// --- tasks ---
	"POST /v1/queues/{queue}/tasks": {Tag: "Tasks", Summary: "Enqueue a task", Access: accessWrite,
		Request: types.TaskCreate{}, Response: types.TaskResponse{}, Status: 201,
		Description: "Set exactly one of `url` or `path` (resolved against the queue's `target.base_url`). " +
			"A `task_id` deduplicates: reusing one within the dedup window fails with `already_exists`."},
	"POST /v1/queues/{queue}/tasks:batch": {Tag: "Tasks", Summary: "Enqueue up to 500 tasks", Access: accessWrite,
		Request: types.TaskBatchCreate{}, Response: types.BatchResponse{},
		Description: "Not atomic: each item succeeds or fails on its own; results are in request order."},
	"GET /v1/queues/{queue}/tasks": {Tag: "Tasks", Summary: "List tasks (newest first)", Access: accessRead, Response: types.TaskList{},
		Query: []queryParam{{Name: "state", Doc: "Only tasks in this state.", Enum: []string{"pending", "running", "succeeded", "failed"}},
			{Name: "id_prefix", Doc: "Only IDs starting with this."}, taskView, pageSize, pageToken}},
	"GET /v1/queues/{queue}/tasks/{taskID}": {Tag: "Tasks", Summary: "Get a task", Access: accessRead,
		Response: types.TaskResponse{}, Query: []queryParam{taskView}},
	"DELETE /v1/queues/{queue}/tasks/{taskID}": {Tag: "Tasks", Summary: "Delete a task", Access: accessWrite,
		Description: "The ID stays reserved for the dedup window."},
	"POST /v1/queues/{queue}/tasks/{taskID}:run": {Tag: "Tasks", Summary: "Dispatch a task now", Access: accessWrite,
		Response: types.TaskResponse{}, Description: "Bypasses rate limits and the paused state."},
	"GET /v1/queues/{queue}/tasks/{taskID}/attempts": {Tag: "Tasks", Summary: "A task's delivery attempts (newest first)",
		Access: accessRead, Response: types.AttemptList{}},

	// --- sign-in ---
	"GET /v1/auth/session": {Tag: "Sign-in", Summary: "Who am I, and how does this server sign people in?", Access: accessPublic,
		Response: sessionResponse{}},
	"POST /v1/auth/setup": {Tag: "Sign-in", Summary: "Create the first admin account (self-hosted)", Access: accessPublic,
		Request: credentialsBody{}, Response: userEnvelope{}, Status: 201,
		Description: "Only while no account exists. If the server has config-file API keys, `api_key` must be one of its admin keys. Signs you in."},
	"POST /v1/auth/login": {Tag: "Sign-in", Summary: "Sign in with email and password (self-hosted)", Access: accessPublic,
		Request: credentialsBody{}, Response: userEnvelope{}, Description: "Sets the session cookie. Rate-limited."},
	"POST /v1/auth/logout": {Tag: "Sign-in", Summary: "Sign out", Access: accessPublic},
	"GET /v1/auth/invites/{token}": {Tag: "Sign-in", Summary: "Look up an invite or password-reset link (self-hosted)",
		Access: accessPublic, Response: inviteDTO{}},
	"POST /v1/auth/invites/{token}:accept": {Tag: "Sign-in", Summary: "Accept an invite or reset link (self-hosted)", Access: accessPublic,
		Request: credentialsBody{}, Response: userEnvelope{}, Description: "Creates the account (or sets the new password) and signs in."},
	"POST /v1/auth/email-code": {Tag: "Sign-in", Summary: "Email a 6-digit sign-in code (SaaS)", Access: accessPublic,
		Request: emailCodeBody{}, Response: codeSent{},
		Description: "A new email gets an account, so this is also sign-up. Doesn't reveal whether the email already had one. Rate-limited."},
	"POST /v1/auth/email-code:verify": {Tag: "Sign-in", Summary: "Sign in with the emailed code (SaaS)", Access: accessPublic,
		Request: emailCodeBody{}, Response: userEnvelope{},
		Description: "Sets the session cookie. May answer 403 `email_verification_required`; finish with POST /v1/auth/email-verification."},
	"GET /v1/auth/email-verification": {Tag: "Sign-in", Summary: "Is a sign-in waiting for an email verification code? (SaaS)",
		Access: accessPublic, Response: verifyPending{}},
	"POST /v1/auth/email-verification": {Tag: "Sign-in", Summary: "Finish a sign-in with the emailed verification code (SaaS)",
		Access: accessPublic, Request: emailCodeBody{}, Response: verifiedSignIn{}},
	"GET /v1/auth/workos/login": {Tag: "Sign-in", Summary: "Start signing in with GitHub (SaaS)", Access: accessPublic, Redirect: true,
		Query: []queryParam{{Name: "provider", Doc: "Sign-in provider.", Enum: []string{"github"}},
			{Name: "next", Doc: "Dashboard path to return to afterwards."}},
		Description: "Browser navigation: redirects to GitHub's consent screen."},
	"GET /v1/auth/workos/callback": {Tag: "Sign-in", Summary: "Where GitHub sign-in returns (SaaS)", Access: accessPublic, Redirect: true,
		Description: "Browser navigation from WorkOS. Redirects into the dashboard, to /login?verify=1 when email verification is needed, or to /login?error=… on failure."},

	// --- account ---
	"PATCH /v1/me": {Tag: "Account", Summary: "Change your name", Access: accessUser, Request: mePatchBody{}, Response: userDTO{}},
	"POST /v1/me/password": {Tag: "Account", Summary: "Change your password (self-hosted)", Access: accessUser,
		Request: passwordChangeBody{}, Description: "Signs you out on your other devices."},

	// --- teams (SaaS) ---
	"GET /v1/teams": {Tag: "Teams", Summary: "Your teams", Access: accessUser, Response: teamsList{}},
	"POST /v1/teams": {Tag: "Teams", Summary: "Create a team", Access: accessUser, Request: teamNameBody{}, Response: teamDTO{}, Status: 201,
		Description: "Creates a WorkOS Organization with you as admin and switches your session to it."},
	"POST /v1/teams/{id}:switch": {Tag: "Teams", Summary: "Switch your session to another of your teams", Access: accessUser},
	"PATCH /v1/teams/current": {Tag: "Teams", Summary: "Rename the current team", Access: accessManage,
		Request: teamNameBody{}, Response: teamDTO{}},

	// --- members & invites ---
	"GET /v1/users": {Tag: "Team members", Summary: "List members", Access: accessManage, Response: usersList{},
		Description: "SaaS mode: the current team's members, re-synced from WorkOS. `role` is the team role."},
	"PATCH /v1/users/{id}": {Tag: "Team members", Summary: "Change a member's role", Access: accessManage,
		Request: userPatchBody{}, Response: userDTO{}, Description: "The last admin can't be demoted."},
	"DELETE /v1/users/{id}": {Tag: "Team members", Summary: "Remove a member", Access: accessManage,
		Description: "They lose access immediately. The last admin can't be removed."},
	"POST /v1/users/{id}/reset-link": {Tag: "Team members", Summary: "Create a password-reset link (self-hosted)", Access: accessManage,
		Response: resetLinkCreated{}, Status: 201},
	"GET /v1/invites": {Tag: "Team members", Summary: "Pending invites", Access: accessManage, Response: invitesList{}},
	"POST /v1/invites": {Tag: "Team members", Summary: "Invite someone", Access: accessManage,
		Request: inviteCreateBody{}, Response: inviteCreated{}, Status: 201,
		Description: "Self-hosted: returns a single-use link to send. SaaS mode: WorkOS emails the invitation."},
	"DELETE /v1/invites/{id}": {Tag: "Team members", Summary: "Revoke an invite", Access: accessManage},

	// --- applications ---
	"GET /v1/apps": {Tag: "Applications", Summary: "List applications", Access: accessRead, Response: appsList{},
		Description: "Every queue belongs to one application. A key restricted to an application sees only that one."},
	"POST /v1/apps": {Tag: "Applications", Summary: "Create an application", Access: accessManage,
		Request: appCreateBody{}, Response: appDTO{}, Status: 201},
	"PATCH /v1/apps/{id}": {Tag: "Applications", Summary: "Rename an application or change its icon", Access: accessManage,
		Request: appPatchBody{}, Response: appDTO{}, Description: "Only the fields you send change."},
	"DELETE /v1/apps/{id}": {Tag: "Applications", Summary: "Delete an application", Access: accessManage,
		Description: "Its queues must be deleted first. API keys restricted to it are revoked. The Default application can't be deleted."},

	// --- API keys ---
	"GET /v1/api-keys": {Tag: "API keys", Summary: "List API keys", Access: accessManage, Response: keysList{},
		Description: "SaaS mode: the current team's keys."},
	"POST /v1/api-keys": {Tag: "API keys", Summary: "Create an API key", Access: accessManage,
		Request: keyCreateBody{}, Response: keyCreated{}, Status: 201,
		Description: "The key is returned once (`secret`) and stored only as a hash."},
	"DELETE /v1/api-keys/{id}": {Tag: "API keys", Summary: "Revoke an API key", Access: accessManage,
		Description: "Requests using it fail from then on."},
}
