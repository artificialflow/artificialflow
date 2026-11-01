using System.Text.Json;
using System.Text.Json.Serialization;

namespace ArtificialFlow.Sdk;

internal static class JsonUtil
{
    public static readonly JsonSerializerOptions Options = new()
    {
        PropertyNameCaseInsensitive = true,
        DefaultIgnoreCondition = JsonIgnoreCondition.WhenWritingNull,
        PropertyNamingPolicy = JsonNamingPolicy.CamelCase,
    };
}
