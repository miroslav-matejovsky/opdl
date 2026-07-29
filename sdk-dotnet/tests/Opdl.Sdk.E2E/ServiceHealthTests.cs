using Microsoft.Kiota.Abstractions.Authentication;
using Microsoft.Kiota.Http.HttpClientLibrary;
using Opdl.Sdk.Client;

namespace Opdl.Sdk.E2E;

/// <summary>
/// End-to-end tests that drive a running OPDL instance through the generated
/// SDK, as a consumer would: the generated client is the only way these tests
/// reach the platform, and every call passes a cancellation token.
/// </summary>
/// <remarks>
/// <para>
/// The scenarios module owns everything around these tests. It builds a machine
/// from a blueprint, starts it, and passes its address in
/// <c>OPDL_PLATFORM_BASEURL</c>. Without that variable there is no instance to
/// reach, so the tests skip instead of failing.
/// </para>
/// <para>
/// The subject is the generated client rather than the platform. The service
/// health view is the platform's first nested response — a list of services,
/// each with a list of observations — so what is being checked is that the
/// contract survives generation into usable models, not that the platform can
/// count. The scenario asserts the platform's side separately.
/// </para>
/// </remarks>
public class ServiceHealthTests
{
    private const string BaseUrlVariable = "OPDL_PLATFORM_BASEURL";

    /// <summary>Bounds one call to an instance that is already running.</summary>
    private static readonly TimeSpan CallTimeout = TimeSpan.FromSeconds(30);

    [SkippableFact]
    public async Task ServiceViewDeserializesIntoTheGeneratedModels()
    {
        var client = Connect();
        using var cancellation = new CancellationTokenSource(CallTimeout);

        var view = await client.Health.Services.GetAsync(cancellationToken: cancellation.Token);

        Assert.NotNull(view);
        Assert.False(string.IsNullOrWhiteSpace(view!.Project));
        Assert.False(string.IsNullOrWhiteSpace(view.Environment));
        Assert.False(string.IsNullOrWhiteSpace(view.Site));
        Assert.False(string.IsNullOrWhiteSpace(view.Machine));
        Assert.False(string.IsNullOrWhiteSpace(view.Role));
        Assert.False(string.IsNullOrWhiteSpace(view.GeneratedAtUtc));

        // Every machine a scenario deploys authors one service, so a site view
        // with nothing in it means the inventory never reached the descriptor.
        var services = view.Services ?? [];
        Assert.NotEmpty(services);

        var service = services[0];
        Assert.False(string.IsNullOrWhiteSpace(service.Machine));
        Assert.False(string.IsNullOrWhiteSpace(service.MachineProfile));
        Assert.False(string.IsNullOrWhiteSpace(service.Service));
        Assert.False(string.IsNullOrWhiteSpace(service.ServiceRole));
        Assert.NotEmpty(service.ExpectedObservers ?? []);

        // The status is whatever the probes have found by now, which depends on
        // how far into its interval the instance is. That it is one of the four
        // the contract names is the part a client can rely on.
        Assert.Contains(service.Status, new[] { "Healthy", "Unhealthy", "Degraded", "Unknown" });

        Assert.NotNull(view.Summary);
        Assert.Equal(services.Count,
            (view.Summary!.Healthy ?? 0) + (view.Summary.Unhealthy ?? 0)
                + (view.Summary.Degraded ?? 0) + (view.Summary.Unknown ?? 0));

        // Every reason is rendered, including the ones at zero, so a client can
        // show the whole tally without first proving a reason exists.
        Assert.NotNull(view.Distribution);
        Assert.False(string.IsNullOrWhiteSpace(view.Distribution!.State));
        Assert.NotEmpty(view.Distribution.Rejected ?? []);
        Assert.NotEmpty(view.Distribution.Dropped ?? []);
        Assert.All(view.Distribution.Dropped ?? [], drop =>
            Assert.False(string.IsNullOrWhiteSpace(drop.Reason)));
    }

    /// <summary>
    /// A failing target service is data, not a failure to answer. This is the
    /// coupling the platform must not have: nothing a probe finds may reach the
    /// instance's own health, because both of a machine's instances probe the
    /// same targets and moving ownership repairs none of them.
    /// </summary>
    [SkippableFact]
    public async Task FailingTargetsDoNotMakeTheInstanceUnhealthy()
    {
        var client = Connect();
        using var cancellation = new CancellationTokenSource(CallTimeout);
        var token = cancellation.Token;

        // Nothing listens on the scenario blueprint's health-check port, so this
        // instance is watching a service it cannot reach.
        var view = await client.Health.Services.GetAsync(cancellationToken: token);
        Assert.NotNull(view);
        Assert.NotEmpty(view!.Services ?? []);

        var health = await client.Health.GetAsync(cancellationToken: token);
        Assert.NotNull(health);
        Assert.NotEqual("Unhealthy", health!.Status);

        var ready = await client.Health.Ready.GetAsync(cancellationToken: token);
        Assert.NotNull(ready);
        Assert.Equal("Healthy", ready!.Status);
    }

    /// <summary>
    /// Builds a client for the instance the scenarios module started, or skips
    /// when it is being run standalone with nothing to reach.
    /// </summary>
    private static PlatformClient Connect()
    {
        var baseUrl = Environment.GetEnvironmentVariable(BaseUrlVariable);
        Skip.If(
            string.IsNullOrWhiteSpace(baseUrl),
            $"{BaseUrlVariable} is not set; these tests are driven by the scenarios module.");

        var adapter = new HttpClientRequestAdapter(new AnonymousAuthenticationProvider())
        {
            BaseUrl = baseUrl,
        };
        return new PlatformClient(adapter);
    }
}
