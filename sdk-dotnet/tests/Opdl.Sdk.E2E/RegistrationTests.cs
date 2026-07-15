using Microsoft.Kiota.Abstractions.Authentication;
using Microsoft.Kiota.Http.HttpClientLibrary;
using Opdl.Sdk.Client;
using Opdl.Sdk.Client.Models;

namespace Opdl.Sdk.E2E;

/// <summary>
/// End-to-end tests that talk to a running platform through the generated SDK.
/// The scenarios module builds the platform, starts it, and passes its address in
/// the OPDL_PLATFORM_BASEURL environment variable. Without that variable there is
/// no platform to reach, so the test skips instead of failing.
/// </summary>
public class RegistrationTests
{
    private const string BaseUrlVariable = "OPDL_PLATFORM_BASEURL";

    [SkippableFact]
    public async Task RegistersUnitAndReadsMatchingStatusAndListViews()
    {
        var baseUrl = Environment.GetEnvironmentVariable(BaseUrlVariable);
        Skip.If(
            string.IsNullOrWhiteSpace(baseUrl),
            $"{BaseUrlVariable} not set; this test is driven by the scenarios module.");

        var adapter = new HttpClientRequestAdapter(new AnonymousAuthenticationProvider())
        {
            BaseUrl = baseUrl,
        };
        var client = new PlatformClient(adapter);
        using var cancellation = new CancellationTokenSource();

        var before = await client.Registrations.GetAsync(cancellationToken: cancellation.Token);
        Assert.Empty(before ?? []);

        await client.Registrations.PostAsync(new RegistrationRequest
        {
            UnitType = 7,
            UnitId = 42,
            UnitTypeNameAdvertised = "Scenario service",
            Role = "Master",
        }, cancellationToken: cancellation.Token);

        var status = await client.Registrations[7][42].Status.GetAsync(cancellationToken: cancellation.Token);
        var registrations = await client.Registrations.GetAsync(cancellationToken: cancellation.Token);

        Assert.NotNull(status);
        Assert.Single(registrations ?? []);
        Assert.Equal(status!.UnitType, registrations![0].UnitType);
        Assert.Equal(status.UnitId, registrations[0].UnitId);
        Assert.Equal("accepted", status.Status);
        Assert.Equal("Master", status.Role);
        Assert.Equal("node", status.Machine);
        Assert.Equal("127.0.0.1", status.Ip);
        var instance = Assert.Single(status.PlatformInstances ?? []);
        Assert.Equal("node", instance.Machine);
        Assert.Equal("127.0.0.1", instance.Ip);
        Assert.Equal("accepted", instance.Status);
    }
}
