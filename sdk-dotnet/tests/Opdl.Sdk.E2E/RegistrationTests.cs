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
/// the first half: a proposal taken by node A must stay pending, and say that node
/// B is the machine it is waiting for, until node B exists and confirms it. The
/// handshake is what makes that a fact rather than a race.
/// </para>
/// <para>
/// Registration is asynchronous. A POST returns the proposal's identity and
/// nothing about the outcome, and the site decides afterwards, so every question
/// this test asks about a registration it asks by polling that identity.
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
    private const string Rejected = "rejected";
    private const string KeyConflict = "registration_key_conflict";

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
        var proposal = await nodeA.Registrations.PostAsync(
            new RegistrationRequest
            {
                UnitType = UnitType,
                UnitId = UnitId,
                UnitTypeNameAdvertised = "Billing",
            },
            cancellationToken: token);

        // 202 says the journal took the proposal and hands back the handle to poll.
        // It deliberately gives a consumer nothing to mistake for a registration.
        Assert.NotNull(proposal);
        Assert.False(string.IsNullOrWhiteSpace(proposal!.ProposalId));
        Assert.True(proposal.Sequence > 0, "the proposal has a place in the site's history");
        var proposalId = proposal.ProposalId!;

        var pending = await nodeA.Registrations[proposalId].GetAsync(cancellationToken: token);
        Assert.NotNull(pending);
        Assert.Equal(proposalId, pending!.ProposalId);
        Assert.Equal(Pending, pending.Status);
        Assert.Equal("node-a", pending.Machine);
        Assert.Equal("127.0.0.1", pending.Ip);
        Assert.Null(pending.Role);
        Assert.Equal("Billing", pending.UnitTypeNameAdvertised);

        // The pending proposal is listed like any other, and the list and the status
        // endpoint agree about it.
        var pendingList = await nodeA.Registrations.GetAsync(cancellationToken: token);
        var listedPending = Assert.Single(pendingList ?? []);
        Assert.Equal(Pending, listedPending.Status);
        Assert.Equal(UnitType, listedPending.UnitType);
        Assert.Equal(UnitId, listedPending.UnitId);

        // Node A confirms for itself as soon as it handles the proposal; node B is
        // expected and offline, so it stays pending. This is the whole point: the
        // platform names the machine it is waiting for rather than going quiet.
        var barrier = await PollStatusAsync(
            nodeA,
            proposalId,
            view => InstanceOf(view, "node-a").Status == Accepted,
            "node-a to confirm its own proposal",
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

        // The client polls the proposal it was given. That is its only confirmation
        // mechanism: nothing is pushed, and node B answers on its own schedule.
        var accepted = await PollStatusAsync(
            nodeA,
            proposalId,
            view => view.Status == Accepted,
            "node-b to start and the site to accept the proposal",
            token);
        Assert.Equal("node-a", accepted.Machine);
        Assert.Equal("127.0.0.1", accepted.Ip);
        Assert.Equal("Billing", accepted.UnitTypeNameAdvertised);
        Assert.Null(accepted.Role);
        Assert.Equal(Accepted, InstanceOf(accepted, "node-a").Status);
        Assert.Equal(Accepted, InstanceOf(accepted, "node-b").Status);

        // Both machines list it identically: the list is the site's state, folded from
        // the same journal on each machine.
        foreach (var client in new[] { nodeA, nodeB })
        {
            var listed = Assert.Single(await client.Registrations.GetAsync(cancellationToken: token) ?? []);
            AssertSameRegistration(accepted, listed);
        }

        // A proposal's status is the same answer wherever it is asked. The client is
        // not tied to the machine it posted to: every node folds the same journal.
        var fromNodeB = await PollStatusAsync(
            nodeB,
            proposalId,
            view => view.Status == Accepted,
            "node-b to project the accepted proposal",
            token);
        AssertSameRegistration(accepted, fromNodeB);

        // A proposal nobody made is not found, on any machine. huma reports errors
        // as RFC 9457 problem+json; Kiota surfaces that as the ErrorModel exception,
        // and the platform's stable machine code is carried in detail.
        var notFound = await Assert.ThrowsAsync<ErrorModel>(
            () => nodeB.Registrations["0000000000000000000000000000000000000000000000000000000000000000"]
                .GetAsync(cancellationToken: token));
        Assert.Equal(404, notFound.ResponseStatusCode);
        Assert.Equal("registration_not_found", notFound.Detail);

        // -- Claiming the key again. ---------------------------------------------------

        // The exact same request is the same claim, so it hands back the same proposal
        // and changes nothing.
        var retry = await nodeA.Registrations.PostAsync(
            new RegistrationRequest
            {
                UnitType = UnitType,
                UnitId = UnitId,
                UnitTypeNameAdvertised = "Billing",
            },
            cancellationToken: token);
        Assert.NotNull(retry);
        Assert.Equal(proposalId, retry!.ProposalId);

        var afterRetry = await nodeA.Registrations[proposalId].GetAsync(cancellationToken: token);
        Assert.NotNull(afterRetry);
        AssertSameRegistration(accepted, afterRetry!);
        Assert.Single(await nodeA.Registrations.GetAsync(cancellationToken: token) ?? []);

        // A different advertised name on the same key is a different claim. The POST
        // cannot refuse it: at the moment the journal takes it nothing has decided
        // anything, and the site resolves the conflict afterwards.
        var contender = await nodeA.Registrations.PostAsync(
            new RegistrationRequest
            {
                UnitType = UnitType,
                UnitId = UnitId,
                UnitTypeNameAdvertised = "Payments",
            },
            cancellationToken: token);
        Assert.NotNull(contender);
        Assert.NotEqual(proposalId, contender!.ProposalId);

        var refused = await PollStatusAsync(
            nodeA,
            contender.ProposalId!,
            view => view.Status == Rejected,
            "the site to reject the competing claim",
            token);
        Assert.Equal(KeyConflict, refused.Reason);

        // The key is the site's, not a machine's, so the same claim from node B is a
        // different claim too: a different origin is a different proposal.
        var crossMachine = await nodeB.Registrations.PostAsync(
            new RegistrationRequest
            {
                UnitType = UnitType,
                UnitId = UnitId,
                UnitTypeNameAdvertised = "Billing",
            },
            cancellationToken: token);
        Assert.NotNull(crossMachine);
        Assert.NotEqual(proposalId, crossMachine!.ProposalId);

        var refusedFromB = await PollStatusAsync(
            nodeB,
            crossMachine.ProposalId!,
            view => view.Status == Rejected,
            "the site to reject the claim from the other machine",
            token);
        Assert.Equal(KeyConflict, refusedFromB.Reason);

        // Every losing claim left the accepted registration exactly as it was, on both
        // machines, and the conflicts query reports the resolution.
        foreach (var client in new[] { nodeA, nodeB })
        {
            var winner = await PollStatusAsync(
                client, proposalId, view => view.Status == Accepted, "the incumbent to survive", token);
            AssertSameRegistration(accepted, winner);
            Assert.Equal(Accepted, InstanceOf(winner, "node-a").Status);
            Assert.Equal(Accepted, InstanceOf(winner, "node-b").Status);

            var conflicts = await client.Registrations.Conflicts.GetAsync(cancellationToken: token);
            var conflict = Assert.Single(conflicts ?? []);
            Assert.Equal("resolved", conflict.ResolutionStatus);
            Assert.Equal(proposalId, conflict.Winner?.ProposalId);
            Assert.Equal(2, (conflict.Losers ?? []).Count);
        }
    }

    /// <summary>Builds a client for one machine's API. The base URL is a deployment
    /// fact, so it is set on the adapter rather than baked into the contract.</summary>
    private static PlatformClient NewClient(string baseUrl) =>
        new(new HttpClientRequestAdapter(new AnonymousAuthenticationProvider()) { BaseUrl = baseUrl });

    /// <summary>
    /// Polls one proposal's status until it satisfies <paramref name="reached"/>.
    /// </summary>
    /// <remarks>
    /// Polling is bounded, and a timeout reports the last view it saw, because "never
    /// reached accepted" is not worth reading without knowing what it did reach.
    /// </remarks>
    private static async Task<Registration> PollStatusAsync(
        PlatformClient client,
        string proposalId,
        Func<Registration, bool> reached,
        string what,
        CancellationToken token)
    {
        var deadline = DateTime.UtcNow + StatusTimeout;
        Registration? last = null;
        while (DateTime.UtcNow < deadline)
        {
            last = await client.Registrations[proposalId].GetAsync(cancellationToken: token);
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
