using System;
using System.Collections.Concurrent;
using System.Collections.Generic;
using System.Diagnostics;
using System.IO;
using System.Linq;
using System.Net;
using System.Net.Http;
using System.Net.Sockets;
using System.Text;
using System.Text.Json;
using System.Threading;
using System.Threading.Tasks;
using Xunit;

namespace Pushq.Tests;

/// <summary>Runs only when PUSHQ_BIN points at a built pushq binary.</summary>
public sealed class IntegrationFactAttribute : FactAttribute
{
    public IntegrationFactAttribute()
    {
        if (string.IsNullOrEmpty(Environment.GetEnvironmentVariable("PUSHQ_BIN")))
            Skip = "PUSHQ_BIN not set; skipping integration tests";
    }
}

public sealed class PushqServer : IAsyncLifetime
{
    private Process? _proc;
    public string BaseUrl { get; private set; } = "";

    public async Task InitializeAsync()
    {
        var bin = Environment.GetEnvironmentVariable("PUSHQ_BIN");
        if (string.IsNullOrEmpty(bin)) return;
        var port = FreePort();
        BaseUrl = $"http://127.0.0.1:{port}";
        _proc = Process.Start(new ProcessStartInfo(bin, $"dev --memory --listen 127.0.0.1:{port}")
        {
            RedirectStandardOutput = true,
            RedirectStandardError = true,
            UseShellExecute = false,
        })!;
        _proc.OutputDataReceived += (_, _) => { };
        _proc.ErrorDataReceived += (_, _) => { };
        _proc.BeginOutputReadLine();
        _proc.BeginErrorReadLine();

        using var http = new HttpClient();
        var deadline = DateTime.UtcNow.AddSeconds(15);
        while (true)
        {
            try
            {
                if ((await http.GetAsync(BaseUrl + "/healthz")).IsSuccessStatusCode) return;
            }
            catch (HttpRequestException)
            {
            }
            if (DateTime.UtcNow > deadline) throw new TimeoutException("pushq did not start");
            await Task.Delay(100);
        }
    }

    public Task DisposeAsync()
    {
        try
        {
            if (_proc is { HasExited: false }) _proc.Kill();
        }
        catch (InvalidOperationException)
        {
        }
        _proc?.Dispose();
        return Task.CompletedTask;
    }

    internal static int FreePort()
    {
        var l = new TcpListener(IPAddress.Loopback, 0);
        l.Start();
        var port = ((IPEndPoint)l.LocalEndpoint).Port;
        l.Stop();
        return port;
    }
}

public class IntegrationTests : IClassFixture<PushqServer>, IDisposable
{
    private readonly PushqClient _client;
    private readonly HttpListener _receiver = new();
    private readonly ConcurrentQueue<(Dictionary<string, string> Headers, byte[] Body)> _received = new();
    private readonly string _receiverUrl = "";

    public IntegrationTests(PushqServer server)
    {
        _client = new PushqClient(server.BaseUrl == "" ? "http://unused" : server.BaseUrl);
        if (server.BaseUrl == "") return;
        _receiverUrl = $"http://127.0.0.1:{PushqServer.FreePort()}";
        _receiver.Prefixes.Add(_receiverUrl + "/");
        _receiver.Start();
        _ = Task.Run(async () =>
        {
            while (_receiver.IsListening)
            {
                HttpListenerContext ctx;
                try
                {
                    ctx = await _receiver.GetContextAsync();
                }
                catch (Exception)
                {
                    return; // listener stopped
                }
                using var ms = new MemoryStream();
                await ctx.Request.InputStream.CopyToAsync(ms);
                var headers = ctx.Request.Headers.AllKeys.Where(k => k != null)
                    .ToDictionary(k => k!, k => ctx.Request.Headers[k]!, StringComparer.OrdinalIgnoreCase);
                _received.Enqueue((headers, ms.ToArray()));
                ctx.Response.StatusCode = 200;
                ctx.Response.Close();
            }
        });
    }

    public void Dispose()
    {
        if (_receiver.IsListening) _receiver.Stop();
        _client.Dispose();
    }

    private static async Task WaitFor(Func<Task<bool>> cond, string what)
    {
        var deadline = DateTime.UtcNow.AddSeconds(10);
        while (!await cond())
        {
            if (DateTime.UtcNow > deadline) throw new TimeoutException("timed out waiting for " + what);
            await Task.Delay(50);
        }
    }

    [IntegrationFact]
    public async Task FullLifecycleWithSignedDeliveryInFolderQueue()
    {
        const string secret = "whsec_dGVzdC1zZWNyZXQ=";
        const string queue = "dotnet/e2e";

        var q = await _client.UpsertQueueAsync(queue, new QueueConfig
        {
            RateLimits = new RateLimits { MaxDispatchesPerSecond = 50 },
            Retry = new RetryConfig { MaxAttempts = 3, MinBackoffSeconds = 0.05 },
            Target = new Target { BaseUrl = _receiverUrl, SigningSecret = secret },
        });
        Assert.Equal(queue, q.Name);
        Assert.True(q.Target.HasSigningSecret);
        Assert.Equal(QueueState.Running, q.State);

        var task = await _client.CreateTaskAsync(queue, new TaskCreate
        {
            TaskId = "dotnet-task-1", Path = "/hook", JsonBody = new { n = 42 },
        });
        Assert.Equal("dotnet-task-1", task.Id);
        Assert.Equal(TaskState.Pending, task.State);

        // Delivered, signed and verifiable with the SDK's own helper.
        await WaitFor(() => Task.FromResult(!_received.IsEmpty), "delivery");
        _received.TryPeek(out var delivery);
        var id = Webhooks.VerifySignature(secret, delivery.Headers, delivery.Body);
        Assert.Equal("dotnet/e2e/dotnet-task-1", id);
        Assert.Equal(42, JsonDocument.Parse(delivery.Body).RootElement.GetProperty("n").GetInt32());
        Assert.Equal(queue, delivery.Headers["X-Pushq-Queue"]);

        await WaitFor(async () => (await _client.GetTaskAsync(queue, "dotnet-task-1")).State == TaskState.Succeeded, "succeeded");
        var attempts = await _client.ListAttemptsAsync(queue, "dotnet-task-1");
        Assert.Single(attempts);
        Assert.Equal(200, attempts[0].ResponseStatus);

        var full = await _client.GetTaskAsync(queue, "dotnet-task-1", TaskView.Full);
        Assert.Equal(@"{""n"":42}", Encoding.UTF8.GetString(full.Body!));

        // Dedup: the same ID is rejected while it's reserved.
        var dup = await Assert.ThrowsAsync<PushqException>(() =>
            _client.CreateTaskAsync(queue, new TaskCreate { TaskId = "dotnet-task-1", Path = "/hook" }));
        Assert.Equal("already_exists", dup.Code);
        Assert.Equal(409, dup.Status);
    }

    [IntegrationFact]
    public async Task BatchPauseListPurgeAndDelete()
    {
        const string queue = "dotnet/batch";
        await _client.UpsertQueueAsync(queue, new QueueConfig { Target = new Target { BaseUrl = _receiverUrl } });
        await _client.PauseQueueAsync(queue);

        var results = await _client.CreateTasksAsync(queue, new[]
        {
            new TaskCreate { TaskId = "a", Path = "/x", Delay = TimeSpan.FromHours(1) },
            new TaskCreate { TaskId = "b", Path = "/x", Delay = TimeSpan.FromHours(1) },
            new TaskCreate { TaskId = "a", Path = "/x" },
        });
        Assert.Equal(3, results.Count);
        Assert.NotNull(results[0].Task);
        Assert.NotNull(results[1].Task);
        Assert.Equal("already_exists", results[2].Error?.Code);

        var page = await _client.ListTasksAsync(queue, new ListTasksOptions { PageSize = 1 });
        Assert.Single(page.Items);
        Assert.NotNull(page.NextPageToken);
        var next = await _client.ListTasksAsync(queue, new ListTasksOptions { PageSize = 1, PageToken = page.NextPageToken });
        Assert.Single(next.Items);
        Assert.NotEqual(page.Items[0].Id, next.Items[0].Id);

        var stats = await _client.GetQueueStatsAsync(queue);
        Assert.Equal(2, stats.ScheduledFuture); // both are an hour out
        Assert.Equal("paused", stats.State);

        var folder = await _client.ListQueuesAsync(new ListQueuesOptions { NamePrefix = "dotnet/" });
        Assert.Contains(folder.Items, x => x.Name == queue);

        var patched = await _client.PatchQueueAsync(queue, new QueuePatch { State = QueueState.Running, RateLimits = new RateLimits { MaxConcurrentDispatches = 7 } });
        Assert.Equal(QueueState.Running, patched.State);
        Assert.Equal(7, patched.RateLimits.MaxConcurrentDispatches);

        var purged = await _client.PurgeQueueAsync(queue);
        Assert.NotNull(purged.PurgedAt);
        var missing = await Assert.ThrowsAsync<PushqException>(() => _client.GetTaskAsync(queue, "a"));
        Assert.Equal("not_found", missing.Code);

        await _client.DeleteQueueAsync(queue);
        var gone = await Assert.ThrowsAsync<PushqException>(() => _client.GetQueueAsync(queue));
        Assert.Equal(404, gone.Status);
    }
}
