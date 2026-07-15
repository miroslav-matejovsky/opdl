using Microsoft.Kiota.Abstractions.Authentication;
using Microsoft.Kiota.Http.HttpClientLibrary;
using Opdl.Sdk.Client;
using Opdl.Sdk.Client.Models;

namespace Opdl.Sdk.E2E;

/// <summary>
/// End-to-end tests that drive a real two-machine OPDL site through the generated
/// SDK, as a consumer would: the generated client is the only way this test
/// reaches the platform, and every call passes a cancellation token.
/// </summary>
/// <remarks>
/// <para>
/// The scenarios module owns everything around this test. It builds both machines
/// from a blueprint, starts node A, passes both base URLs and a control directory,
/// and starts node B only once this test signals it has finished its pending
/// assertions. Without those variables there is no site to reach, so the test
/// skips instead of failing.
/// </para>
/// <para>
/// The subject is the acceptance barrier. Node B is deliberately not running for
/// the first half: a request taken by node A must stay pending, and say that node
/// B is the machine it is waiting for, until node B exists and accepts it. The
/// handshake is what makes that a fact rather than a race.
/// </para>
/// </remarks>
public class RegistrationTests
{
    private const string NodeAVariable = "OPDL_PLATFORM_BASEURL_A";
    private const string NodeBVariable = "OPDL_PLATFORM_BASEURL_B";
    private const string ControlDirVariable = "OPDL_CONTROL_DIR";

    /// <summary>The marker this test writes once pending behavior is proven and node B may start.</summary>
    private const string PendingObservedMarker = "pending-observed";

    private const byte UnitType = 7;
    private const int UnitId = 42;

    private const string Pending = "pending";
    private const string Accepted = "accepted";

    /// <summary>Bounds a poll for a state only another machine can bring about.</summary>
    private static readonly TimeSpan StatusTimeout = TimeSpan.FromSeconds(60);

    private static readonly TimeSpan StatusPollInterval = TimeSpan.FromMilliseconds(100);

    [SkippableFact]
    public async Task RegistrationIsAcceptedOnlyAfterEveryExpectedMachineConfirms()
    {
        var nodeAUrl = Environment.GetEnvironmentVariable(NodeAVariable);
        var nodeBUrl = Environment.GetEnvironmentVariable(NodeBVariable);
        var controlDir = Environment.GetEnvironmentVariable(ControlDirVariable);
        Skip.If(
            string.IsNullOrWhiteSpace(nodeAUrl)
                || string.IsNullOrWhiteSpace(nodeBUrl)
                || string.IsNullOrWhiteSpace(controlDir),
            $"{NodeAVariable}, {NodeBVariable}, and {ControlDirVariable} not set; this test is driven by the scenarios module.");

        using var cancellation = new CancellationTokenSource(TimeSpan.FromMinutes(5));
        var token = cancellation.Token;
        var nodeA = NewClient(nodeAUrl!);
        var nodeB = NewClient(nodeBUrl!);

        // -- Node B is not running yet. ------------------------------------------------

        var before = await nodeA.Registrations.GetAsync(cancellationToken: token);
        Assert.Empty(before ?? []);

        // A role-less request: role is optional, and the platform must not invent one.
        await nodeA.Registrations.PostAsync(
            new RegistrationRequest
            {
                UnitType = UnitType,
                UnitId = UnitId,
                UnitTypeNameAdvertised = "Billing",
            },
            cancellationToken: token);

        // PostAsync returns no value at all: 202 says the request was taken, and the
        // contract deliberately gives a consumer nothing to mistake for a registration.

        var pending = await nodeA.Registrations[UnitType][UnitId].Status.GetAsync(cancellationToken: token);
        Assert.NotNull(pending);
        Assert.Equal(Pending, pending!.Status);
        Assert.Equal("node-a", pending.Machine);
        Assert.Equal("127.0.0.1", pending.Ip);
        Assert.Null(pending.Role);
        Assert.Equal("Billing", pending.UnitTypeNameAdvertised);

        // The pending request is listed like any other, and the list and the status
        // endpoint agree about it.
        var pendingList = await nodeA.Registrations.GetAsync(cancellationToken: token);
        var listedPending = Assert.Single(pendingList ?? []);
        Assert.Equal(Pending, listedPending.Status);
        Assert.Equal(UnitType, listedPending.UnitType);
        Assert.Equal(UnitId, listedPending.UnitId);

        // Node A answers for itself as soon as it looks at the request; node B is
        // expected and offline, so it stays pending. This is the whole point: the
        // platform names the machine it is waiting for rather than going quiet.
        var barrier = await PollStatusAsync(
            nodeA,
            view => InstanceOf(view, "node-a").Status == Accepted,
            "node-a to confirm its own request",
            token);
        Assert.Equal(Pending, barrier.Status);
        Assert.Equal(Accepted, InstanceOf(barrier, "node-a").Status);
        Assert.Equal(Pending, InstanceOf(barrier, "node-b").Status);
        Assert.Equal("127.0.0.2", InstanceOf(barrier, "node-b").Ip);

        // -- Pending is proven. Tell the harness to start node B. ----------------------

        await File.WriteAllTextAsync(
            Path.Combine(controlDir!, PendingObservedMarker),
            "node-a reported pending with node-b offline",
            token);

        // -- Node B joins and the site can accept. -------------------------------------

        // The client polls where it asked. That is its only confirmation mechanism:
        // nothing is pushed, and node B answers on its own schedule.
        var accepted = await PollStatusAsync(
            nodeA,
            view => view.Status == Accepted,
            "node-b to start and the site to accept the request",
            token);
        Assert.Equal("node-a", accepted.Machine);
        Assert.Equal("127.0.0.1", accepted.Ip);
        Assert.Equal("Billing", accepted.UnitTypeNameAdvertised);
        Assert.Null(accepted.Role);
        Assert.Equal(Accepted, InstanceOf(accepted, "node-a").Status);
        Assert.Equal(Accepted, InstanceOf(accepted, "node-b").Status);

        // Both machines list it identically: the list is the site's state, and both
        // machines carry it.
        foreach (var client in new[] { nodeA, nodeB })
        {
            var listed = Assert.Single(await client.Registrations.GetAsync(cancellationToken: token) ?? []);
            AssertSameRegistration(accepted, listed);
        }

        // Node B holds the same registration and still answers 404 for its status: it
        // is not the machine the client asked.
        var notFound = await Assert.ThrowsAsync<Error>(
            () => nodeB.Registrations[UnitType][UnitId].Status.GetAsync(cancellationToken: token));
        Assert.Equal(404, notFound.ResponseStatusCode);
        Assert.Equal("registration_not_found", notFound.Code);

        // -- Claiming the key again. ---------------------------------------------------

        // The exact same request is the same claim, so it is answered and changes
        // nothing.
        await nodeA.Registrations.PostAsync(
            new RegistrationRequest
            {
                UnitType = UnitType,
                UnitId = UnitId,
                UnitTypeNameAdvertised = "Billing",
            },
            cancellationToken: token);
        var afterRetry = await nodeA.Registrations[UnitType][UnitId].Status.GetAsync(cancellationToken: token);
        Assert.NotNull(afterRetry);
        AssertSameRegistration(accepted, afterRetry!);

        // A different advertised name on the same key is a different claim, even from
        // the machine that holds it.
        var sameMachineConflict = await Assert.ThrowsAsync<Error>(
            () => nodeA.Registrations.PostAsync(
                new RegistrationRequest
                {
                    UnitType = UnitType,
                    UnitId = UnitId,
                    UnitTypeNameAdvertised = "Payments",
                },
                cancellationToken: token));
        Assert.Equal(409, sameMachineConflict.ResponseStatusCode);
        Assert.Equal("registration_key_conflict", sameMachineConflict.Code);

        // The key is the site's, not a machine's, so the same claim from node B is a
        // different claim too.
        var crossMachineConflict = await Assert.ThrowsAsync<Error>(
            () => nodeB.Registrations.PostAsync(
                new RegistrationRequest
                {
                    UnitType = UnitType,
                    UnitId = UnitId,
                    UnitTypeNameAdvertised = "Billing",
                },
                cancellationToken: token));
        Assert.Equal(409, crossMachineConflict.ResponseStatusCode);
        Assert.Equal("registration_key_conflict", crossMachineConflict.Code);

        // Every refused claim left the registration exactly as it was, on both
        // machines.
        foreach (var client in new[] { nodeA, nodeB })
        {
            var listed = Assert.Single(await client.Registrations.GetAsync(cancellationToken: token) ?? []);
            AssertSameRegistration(accepted, listed);
            Assert.Equal(Accepted, InstanceOf(listed, "node-a").Status);
            Assert.Equal(Accepted, InstanceOf(listed, "node-b").Status);
        }
    }

    /// <summary>Builds a client for one machine's API. The base URL is a deployment
    /// fact, so it is set on the adapter rather than baked into the contract.</summary>
    private static PlatformClient NewClient(string baseUrl) =>
        new(new HttpClientRequestAdapter(new AnonymousAuthenticationProvider()) { BaseUrl = baseUrl });

    /// <summary>
    /// Polls the status endpoint until the request satisfies <paramref name="reached"/>.
    /// </summary>
    /// <remarks>
    /// Polling is bounded, and a timeout reports the last view it saw, because "never
    /// reached accepted" is not worth reading without knowing what it did reach.
    /// </remarks>
    private static async Task<Registration> PollStatusAsync(
        PlatformClient client,
        Func<Registration, bool> reached,
        string what,
        CancellationToken token)
    {
        var deadline = DateTime.UtcNow + StatusTimeout;
        Registration? last = null;
        while (DateTime.UtcNow < deadline)
        {
            last = await client.Registrations[UnitType][UnitId].Status.GetAsync(cancellationToken: token);
            if (last is not null && reached(last))
            {
                return last;
            }
            await Task.Delay(StatusPollInterval, token);
        }

        Assert.Fail($"timed out after {StatusTimeout} waiting for {what}; last status was {Describe(last)}");
        throw new InvalidOperationException("unreachable");
    }

    /// <summary>Returns one expected platform instance's entry, failing when the
    /// projection does not cover that machine at all.</summary>
    private static PlatformInstanceRegistrationStatus InstanceOf(Registration view, string machine)
    {
        var instance = (view.PlatformInstances ?? []).SingleOrDefault(i => i.Machine == machine);
        Assert.True(instance is not null, $"no platform instance entry for {machine} in {Describe(view)}");
        return instance!;
    }

    /// <summary>Asserts two views describe the same registration, including its
    /// per-instance progress.</summary>
    private static void AssertSameRegistration(Registration expected, Registration actual)
    {
        Assert.Equal(Describe(expected), Describe(actual));
    }

    /// <summary>Renders a registration for an assertion message, and as the value two
    /// views are compared on.</summary>
    private static string Describe(Registration? view)
    {
        if (view is null)
        {
            return "(none)";
        }
        var instances = (view.PlatformInstances ?? [])
            .Select(i => $"{i.Machine}({i.Ip})={i.Status}{(i.Reason is null ? "" : $":{i.Reason}")}");
        return $"{view.UnitType}/{view.UnitId} {view.UnitTypeNameAdvertised} role={view.Role ?? "(none)"} "
            + $"origin={view.Machine}({view.Ip}) status={view.Status}{(view.Reason is null ? "" : $":{view.Reason}")} "
            + $"instances=[{string.Join(", ", instances)}]";
    }
}
