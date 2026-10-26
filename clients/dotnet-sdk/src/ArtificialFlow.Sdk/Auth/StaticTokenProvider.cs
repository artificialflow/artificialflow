namespace ArtificialFlow.Sdk.Auth;

public sealed class StaticTokenProvider : ITokenProvider
{
    private readonly Func<CancellationToken, Task<string>> _factory;

    public StaticTokenProvider(string token)
    {
        ArgumentException.ThrowIfNullOrEmpty(token);
        _factory = _ => Task.FromResult(token);
    }

    public StaticTokenProvider(Func<CancellationToken, Task<string>> factory)
    {
        _factory = factory ?? throw new ArgumentNullException(nameof(factory));
    }

    public Task<string> GetTokenAsync(CancellationToken cancellationToken = default)
    {
        return _factory(cancellationToken);
    }
}
