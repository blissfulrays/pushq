using System;
using System.Collections.Generic;
using System.Globalization;
using System.Linq;
using System.Security.Cryptography;
using System.Text;

namespace Pushq
{
    /// <summary>Thrown when a delivery's signature doesn't verify.</summary>
    public sealed class WebhookVerificationException : Exception
    {
        /// <summary>Creates the exception.</summary>
        public WebhookVerificationException(string message)
            : base("pushq webhook verification failed: " + message)
        {
        }
    }

    /// <summary>
    /// Receiver-side verification of pushq deliveries signed with a queue's
    /// signing secret (Standard Webhooks: HMAC-SHA256 over "id.timestamp.payload").
    /// </summary>
    /// <example>
    /// ASP.NET Core minimal API:
    /// <code>
    /// app.MapPost("/hooks/send", async (HttpRequest req) => {
    ///     using var ms = new MemoryStream();
    ///     await req.Body.CopyToAsync(ms);
    ///     var id = Webhooks.VerifySignature(secret, name => req.Headers[name].FirstOrDefault(), ms.ToArray());
    ///     // id ("queue/task_id") is stable across retries: use it as an idempotency key.
    ///     return Results.Ok();
    /// });
    /// </code>
    /// </example>
    public static class Webhooks
    {
        private const string SecretPrefix = "whsec_";

        /// <summary>Default allowed clock skew between pushq and the receiver.</summary>
        public static readonly TimeSpan DefaultTolerance = TimeSpan.FromMinutes(5);

        /// <summary>
        /// Verifies a delivery and returns its webhook id ("queue/task_id").
        /// </summary>
        /// <param name="secret">The queue's signing secret ("whsec_…" or raw).</param>
        /// <param name="header">Looks up a request header by name (case-insensitive), or null if absent.</param>
        /// <param name="payload">The raw request body, exactly as received.</param>
        /// <param name="tolerance">Max timestamp skew; default 5 minutes. <see cref="TimeSpan.Zero"/> disables the check.</param>
        /// <param name="now">Override "now" (tests).</param>
        /// <exception cref="WebhookVerificationException">The signature, timestamp or headers are invalid.</exception>
        public static string VerifySignature(string secret, Func<string, string?> header, byte[] payload,
            TimeSpan? tolerance = null, DateTimeOffset? now = null)
        {
            if (header == null) throw new ArgumentNullException(nameof(header));
            if (payload == null) throw new ArgumentNullException(nameof(payload));
            var id = header("webhook-id");
            var tsRaw = header("webhook-timestamp");
            var sigHeader = header("webhook-signature");
            if (string.IsNullOrEmpty(id) || string.IsNullOrEmpty(tsRaw) || string.IsNullOrEmpty(sigHeader))
                throw new WebhookVerificationException("missing webhook-id/-timestamp/-signature headers");

            if (!long.TryParse(tsRaw, NumberStyles.Integer, CultureInfo.InvariantCulture, out var ts))
                throw new WebhookVerificationException("invalid timestamp");
            var tol = tolerance ?? DefaultTolerance;
            if (tol > TimeSpan.Zero)
            {
                var nowSec = (now ?? DateTimeOffset.UtcNow).ToUnixTimeSeconds();
                if (Math.Abs(nowSec - ts) > tol.TotalSeconds)
                    throw new WebhookVerificationException("timestamp outside tolerance");
            }

            var expected = Mac(SigningKey(secret), id!, ts, payload);
            foreach (var part in sigHeader!.Split(new[] { ' ' }, StringSplitOptions.RemoveEmptyEntries))
            {
                var comma = part.IndexOf(',');
                if (comma < 0 || part.Substring(0, comma) != "v1") continue;
                byte[] sig;
                try
                {
                    sig = Convert.FromBase64String(part.Substring(comma + 1));
                }
                catch (FormatException)
                {
                    continue;
                }
                if (FixedTimeEquals(sig, expected)) return id!;
            }
            throw new WebhookVerificationException("no matching signature");
        }

        /// <summary>Verifies a delivery whose body is UTF-8 text.</summary>
        public static string VerifySignature(string secret, Func<string, string?> header, string payload,
            TimeSpan? tolerance = null, DateTimeOffset? now = null)
            => VerifySignature(secret, header, Encoding.UTF8.GetBytes(payload ?? throw new ArgumentNullException(nameof(payload))), tolerance, now);

        /// <summary>Verifies a delivery given its headers as a dictionary (names matched case-insensitively).</summary>
        public static string VerifySignature(string secret, IEnumerable<KeyValuePair<string, string>> headers, byte[] payload,
            TimeSpan? tolerance = null, DateTimeOffset? now = null)
        {
            if (headers == null) throw new ArgumentNullException(nameof(headers));
            var map = headers.GroupBy(h => h.Key, StringComparer.OrdinalIgnoreCase)
                .ToDictionary(g => g.Key, g => g.First().Value, StringComparer.OrdinalIgnoreCase);
            return VerifySignature(secret, name => map.TryGetValue(name, out var v) ? v : null, payload, tolerance, now);
        }

        /// <summary>Computes a "v1,&lt;base64&gt;" signature (tests and tooling).</summary>
        public static string Sign(string secret, string msgId, long timestampSeconds, byte[] payload)
            => "v1," + Convert.ToBase64String(Mac(SigningKey(secret), msgId, timestampSeconds, payload));

        private static byte[] SigningKey(string secret)
        {
            if (secret == null) throw new ArgumentNullException(nameof(secret));
            if (secret.StartsWith(SecretPrefix, StringComparison.Ordinal))
            {
                try
                {
                    return Convert.FromBase64String(secret.Substring(SecretPrefix.Length));
                }
                catch (FormatException)
                {
                    throw new ArgumentException("invalid whsec_ secret (not base64)", nameof(secret));
                }
            }
            return Encoding.UTF8.GetBytes(secret);
        }

        private static byte[] Mac(byte[] key, string id, long ts, byte[] payload)
        {
            var prefix = Encoding.UTF8.GetBytes(id + "." + ts.ToString(CultureInfo.InvariantCulture) + ".");
            var message = new byte[prefix.Length + payload.Length];
            Buffer.BlockCopy(prefix, 0, message, 0, prefix.Length);
            Buffer.BlockCopy(payload, 0, message, prefix.Length, payload.Length);
            using var hmac = new HMACSHA256(key);
            return hmac.ComputeHash(message);
        }

        // CryptographicOperations.FixedTimeEquals isn't in netstandard2.0.
        private static bool FixedTimeEquals(byte[] a, byte[] b)
        {
            if (a.Length != b.Length) return false;
            var diff = 0;
            for (var i = 0; i < a.Length; i++) diff |= a[i] ^ b[i];
            return diff == 0;
        }
    }
}
