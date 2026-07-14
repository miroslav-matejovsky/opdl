using Microsoft.Kiota.Abstractions.Authentication;
using Microsoft.Kiota.Http.HttpClientLibrary;
using Opdl.Sdk.Client;

namespace Opdl.Sdk.E2E;

/// <summary>
/// End-to-end tests that talk to a running platform through the generated SDK.
/// The scenarios module builds the platform, starts it, and passes its address in
/// the OPDL_PLATFORM_BASEURL environment variable. Without that variable there is
/// no platform to reach, so the test skips instead of failing.
/// </summary>
public class PlatformStatusTests
{
    private const string BaseUrlVariable = "OPDL_PLATFORM_BASEURL";

    [SkippableFact]
    public async Task ReportsPlatformRunning()
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

        var status = await client.GetAsync();

        Assert.NotNull(status);
        Assert.Equal("ok", status!.StatusProp);
        Assert.Equal("platform is running", status.Message);
    }
}
