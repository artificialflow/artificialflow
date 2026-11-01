namespace ArtificialFlow.Sdk.Auth;

public interface ITokenProvider
{
    Task<string> GetTokenAsync(CancellationToken cancellationToken = default);
}
