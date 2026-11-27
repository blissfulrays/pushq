using System;
using System.Collections.Generic;
using System.Linq;
using System.Net;
using System.Net.Http;
using System.Text;
using System.Text.Json;
using System.Threading;
using System.Threading.Tasks;
using Xunit;

namespace Pushq.Tests;

// Request-shape tests against a recording handler: no server needed.
public class ClientTests
{
    private sealed class Recorder : HttpMessageHandler
    {
        public readonly List<(string Method, string Url, string? Auth, string? Body)> Requests = new();
        public HttpStatusCode Status = HttpStatusCode.OK;
        public string Response = "{}";

        protected override async Task<HttpResponseMessage> SendAsync(HttpRequestMessage req, CancellationToken ct)
        {
            var body = req.Content == null ? null : await req.Content.ReadAsStringAsync();
            Requests.Add((req.Method.Method, req.RequestUri!.AbsoluteUri, req.Headers.Authorization?.ToString(), body));
            return new HttpResponseMessage(Status) { Content = new StringContent(Response, Encoding.UTF8, "application/json") };
        }
    }

    private static (PushqClient, Recorder) Client(string? apiKey = "pq_test")
    {
        var rec = new Recorder();
        var client = new PushqClient(new PushqClientOptions
        {
            BaseUrl = "http://pushq.test/",
            ApiKey = apiKey,
            HttpClient = new HttpClient(rec),
        });
        return (client, rec);
    }

    private const string QueueJson = @"{""name"":""user/instagram"",""state"":""paused"",
        ""rate_limits"":{""max_dispatches_per_second"":5,""max_burst_size"":5,""max_concurrent_dispatches"":10},
        ""retry"":{""max_attempts"":3,""max_retry_duration_seconds"":0,""min_backoff_seconds"":0.1,""max_backoff_seconds"":3600,""max_doublings"":16},
        ""target"":{""base_url"":""https://api.example.com"",""has_signing_secret"":true},
        ""created_at"":""2026-01-02T03:04:05Z"",""updated_at"":""2026-01-02T03:04:06Z""}";

    [Fact]
    public async Task FolderQueueNamesAreOnePathSegment()
    {
        var (c, rec) = Client();
        rec.Response = QueueJson;
        var q = await c.PauseQueueAsync("user/instagram");

        var r = rec.Requests.Single();
        Assert.Equal("POST", r.Method);
        Assert.Equal("http://pushq.test/v1/queues/user%2Finstagram:pause", r.Url);
        Assert.Equal("Bearer pq_test", r.Auth);
        Assert.Equal("user/instagram", q.Name);
        Assert.Equal(QueueState.Paused, q.State);
        Assert.True(q.Target.HasSigningSecret);
        Assert.Equal(10, q.RateLimits.MaxConcurrentDispatches);
        Assert.Equal(new DateTimeOffset(2026, 1, 2, 3, 4, 5, TimeSpan.Zero), q.CreatedAt);
    }

    [Fact]
    public async Task UpsertSendsOnlySetFieldsInSnakeCase()
    {
        var (c, rec) = Client(apiKey: null);
        rec.Response = QueueJson;
        await c.UpsertQueueAsync("emails", new QueueConfig
        {
            RateLimits = new RateLimits { MaxDispatchesPerSecond = 50 },
            Target = new Target { BaseUrl = "https://api.example.com", SigningSecret = "" },
        });

        var r = rec.Requests.Single();
        Assert.Equal("PUT", r.Method);
        Assert.Null(r.Auth);
        using var doc = JsonDocument.Parse(r.Body!);
        var root = doc.RootElement;
        Assert.Equal(50, root.GetProperty("rate_limits").GetProperty("max_dispatches_per_second").GetDouble());
        Assert.False(root.GetProperty("rate_limits").TryGetProperty("max_burst_size", out _));
        Assert.False(root.TryGetProperty("retry", out _));
        // "" is meaningful (clear the secret) and must be sent.
        Assert.Equal("", root.GetProperty("target").GetProperty("signing_secret").GetString());
    }

    [Fact]
    public async Task PatchSendsLowercaseState()
    {
        var (c, rec) = Client();
        rec.Response = QueueJson;
        await c.PatchQueueAsync("q", new QueuePatch { State = QueueState.Disabled });
        Assert.Equal("PATCH", rec.Requests[0].Method);
        Assert.Equal(@"{""state"":""disabled""}", rec.Requests[0].Body);
    }

    [Fact]
    public async Task CreateTaskEncodesBodyDelayAndJson()
    {
        var (c, rec) = Client();
        rec.Status = HttpStatusCode.Created;
        rec.Response = @"{""id"":""t1"",""queue"":""q"",""state"":""pending"",""url"":""/x"",""method"":""POST"",
            ""schedule_time"":""2026-01-02T03:04:05Z"",""created_at"":""2026-01-02T03:04:05Z"",
            ""dispatch_deadline_seconds"":600,""dispatch_count"":0,""response_count"":0}";

        await c.CreateTaskAsync("q", new TaskCreate
        {
            TaskId = "t1", Path = "/x", Body = Encoding.UTF8.GetBytes("hi"),
            Delay = TimeSpan.FromSeconds(90), DispatchDeadline = TimeSpan.FromMinutes(1),
        });
        var t = await c.CreateTaskAsync("q", new TaskCreate { Url = "https://a.example/x", JsonBody = new { n = 42, tags = new[] { "a" } } });

        using (var doc = JsonDocument.Parse(rec.Requests[0].Body!))
        {
            var root = doc.RootElement;
            Assert.Equal("aGk=", root.GetProperty("body_base64").GetString());
            Assert.Equal(90, root.GetProperty("delay_seconds").GetDouble());
            Assert.Equal(60, root.GetProperty("dispatch_deadline_seconds").GetDouble());
        }
        Assert.Equal(@"{""url"":""https://a.example/x"",""json_body"":{""n"":42,""tags"":[""a""]}}", rec.Requests[1].Body);
        Assert.Equal(TaskState.Pending, t.State);
    }

    [Fact]
    public async Task ConflictingTaskFieldsAreRejectedLocally()
    {
        var (c, rec) = Client();
        await Assert.ThrowsAsync<ArgumentException>(() =>
            c.CreateTaskAsync("q", new TaskCreate { Body = new byte[1], JsonBody = new { } }));
        await Assert.ThrowsAsync<ArgumentException>(() =>
            c.CreateTaskAsync("q", new TaskCreate { Delay = TimeSpan.FromSeconds(1), ScheduleTime = DateTimeOffset.UtcNow }));
        Assert.Empty(rec.Requests);
    }

    [Fact]
    public async Task ListTasksBuildsQueryAndPages()
    {
        var (c, rec) = Client();
        rec.Response = @"{""tasks"":[],""next_page_token"":""abc""}";
        var page = await c.ListTasksAsync("user/ig", new ListTasksOptions { State = TaskState.Failed, View = TaskView.Full, PageSize = 25, PageToken = "x/y" });

        Assert.Equal("http://pushq.test/v1/queues/user%2Fig/tasks?state=failed&view=full&page_size=25&page_token=x%2Fy", rec.Requests[0].Url);
        Assert.Equal("abc", page.NextPageToken);
        Assert.Empty(page.Items);

        rec.Response = @"{""tasks"":[],""next_page_token"":""""}";
        Assert.Null((await c.ListTasksAsync("q")).NextPageToken);
    }

    [Fact]
    public async Task ProblemJsonBecomesPushqException()
    {
        var (c, rec) = Client();
        rec.Status = HttpStatusCode.Conflict;
        rec.Response = @"{""type"":""about:blank"",""title"":""Conflict"",""status"":409,""code"":""already_exists"",""detail"":""task t1 exists""}";
        var ex = await Assert.ThrowsAsync<PushqException>(() => c.CreateTaskAsync("q", new TaskCreate { Path = "/x" }));
        Assert.Equal(409, ex.Status);
        Assert.Equal("already_exists", ex.Code);
        Assert.Equal("task t1 exists", ex.Detail);

        rec.Status = HttpStatusCode.BadGateway;
        rec.Response = "<html>bad gateway</html>";
        var ex2 = await Assert.ThrowsAsync<PushqException>(() => c.GetQueueAsync("q"));
        Assert.Equal(502, ex2.Status);
        Assert.Equal("internal", ex2.Code);
    }

    [Fact]
    public async Task DeleteHandlesNoContent()
    {
        var (c, rec) = Client();
        rec.Status = HttpStatusCode.NoContent;
        rec.Response = "";
        await c.DeleteTaskAsync("q", "t1");
        Assert.Equal("DELETE", rec.Requests[0].Method);
        Assert.Equal("http://pushq.test/v1/queues/q/tasks/t1", rec.Requests[0].Url);
    }
}
