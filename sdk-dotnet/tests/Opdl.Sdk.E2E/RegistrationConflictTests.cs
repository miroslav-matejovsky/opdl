using System.Text;
using Microsoft.Kiota.Serialization.Json;
using Opdl.Sdk.Client.Models;

namespace Opdl.Sdk.E2E;

/// <summary>Tests the generated conflict model independently of a running site.</summary>
public class RegistrationConflictTests
{
    [Fact]
    public async Task ConflictPayloadReadsWinnerAndLoserDetails()
    {
        const string payload = """
            {
              "unit_type": 7,
              "unit_id": 42,
              "resolution_status": "resolved",
              "winner": {
                "unit_type": 7,
                "unit_id": 42,
                "unit_type_name_advertised": "First",
                "machine": "node-a",
                "ip": "127.0.0.1",
                "status": "accepted",
                "platform_instances": []
              },
              "losers": [{
                "unit_type": 7,
                "unit_id": 42,
                "unit_type_name_advertised": "Second",
                "machine": "node-b",
                "ip": "127.0.0.2",
                "status": "rejected",
                "reason": "registration_key_conflict",
                "platform_instances": []
              }]
            }
            """;
        await using var content = new MemoryStream(Encoding.UTF8.GetBytes(payload));
        var root = await new JsonParseNodeFactory().GetRootParseNodeAsync("application/json", content);
        var conflict = root.GetObjectValue(RegistrationConflict.CreateFromDiscriminatorValue);

        Assert.NotNull(conflict);
        Assert.Equal(7, conflict!.UnitType);
        Assert.Equal(42, conflict.UnitId);
        Assert.Equal("resolved", conflict.ResolutionStatus);
        Assert.NotNull(conflict.Winner);
        Assert.Equal("First", conflict.Winner!.UnitTypeNameAdvertised);
        Assert.Equal("accepted", conflict.Winner.Status);
        var loser = Assert.Single(conflict.Losers ?? []);
        Assert.Equal("Second", loser.UnitTypeNameAdvertised);
        Assert.Equal("rejected", loser.Status);
        Assert.Equal("registration_key_conflict", loser.Reason);
    }
}
