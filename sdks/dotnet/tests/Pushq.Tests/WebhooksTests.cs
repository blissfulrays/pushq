using System;
using System.Collections.Generic;
using System.Text;
using Xunit;

namespace Pushq.Tests;

public class WebhooksTests
{
    private const string Secret = "whsec_dGVzdC1zZWNyZXQ="; // "test-secret"
    private const string Id = "orders/job-1";
    private const long Ts = 1_700_000_000;
    private static readonly byte[] Payload = Encoding.UTF8.GetBytes(@"{""hello"":""world""}");
    private static readonly DateTimeOffset Now = DateTimeOffset.FromUnixTimeSeconds(Ts);

    private static Dictionary<string, string> Headers(byte[] body, long at = Ts, string secret = Secret) => new()
    {
        ["Webhook-Id"] = Id,
        ["webhook-timestamp"] = at.ToString(),
        ["WEBHOOK-SIGNATURE"] = Webhooks.Sign(secret, Id, at, body),
    };

    [Fact]
    public void VerifiesAndReturnsId() =>
        Assert.Equal(Id, Webhooks.VerifySignature(Secret, Headers(Payload), Payload, now: Now));

    // Reference value computed independently (Python hmac/hashlib) for the
    // Standard Webhooks scheme the server signs with.
    [Fact]
    public void MatchesReferenceSignature()
    {
        var sig = Webhooks.Sign(Secret, Id, Ts, Payload);
        Assert.Equal("v1,mJ+J6ykrlzGyekWCV40yeYYFcKaooL7Zmozetss/3Io=", sig);
        Assert.Equal(Id, Webhooks.VerifySignature(Secret,
            name => name == "webhook-id" ? Id : name == "webhook-timestamp" ? Ts.ToString() : name == "webhook-signature" ? sig : null,
            @"{""hello"":""world""}", now: Now));
    }

    [Fact]
    public void RejectsTamperedPayload() =>
        Assert.Throws<WebhookVerificationException>(() =>
            Webhooks.VerifySignature(Secret, Headers(Payload), Encoding.UTF8.GetBytes(@"{""hello"":""world!""}"), now: Now));

    [Fact]
    public void RejectsWrongSecret() =>
        Assert.Throws<WebhookVerificationException>(() =>
            Webhooks.VerifySignature("whsec_b3RoZXI=", Headers(Payload), Payload, now: Now));

    [Fact]
    public void RejectsStaleTimestampUnlessToleranceDisabled()
    {
        var h = Headers(Payload, at: Ts - 600);
        Assert.Throws<WebhookVerificationException>(() => Webhooks.VerifySignature(Secret, h, Payload, now: Now));
        Assert.Equal(Id, Webhooks.VerifySignature(Secret, h, Payload, tolerance: TimeSpan.Zero, now: Now));
    }

    [Fact]
    public void AcceptsAnyMatchingSignatureInList()
    {
        var h = Headers(Payload);
        h["WEBHOOK-SIGNATURE"] = "v1,bm90LWl0 v2,ignored " + h["WEBHOOK-SIGNATURE"];
        Assert.Equal(Id, Webhooks.VerifySignature(Secret, h, Payload, now: Now));
    }

    [Fact]
    public void RejectsMissingHeaders() =>
        Assert.Throws<WebhookVerificationException>(() =>
            Webhooks.VerifySignature(Secret, new Dictionary<string, string>(), Payload, now: Now));

    [Fact]
    public void RawSecretsWork()
    {
        var h = Headers(Payload, secret: "plain-secret");
        Assert.Equal(Id, Webhooks.VerifySignature("plain-secret", h, Payload, now: Now));
    }
}
