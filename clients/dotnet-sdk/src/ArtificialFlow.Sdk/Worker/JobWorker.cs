using ArtificialFlow.Sdk.Models;

namespace ArtificialFlow.Sdk.Worker;

public sealed class JobWorkerOptions
{
    public int MaxJobs { get; init; } = 32;
    public int PollInterval { get; init; } = 1000;
    public int Timeout { get; init; } = 5000;
    public int LockDuration { get; init; } = 30_000;
    public string? WorkerName { get; init; }
    public Func<Job, Exception, int?>? FailRetries { get; init; }
    public Action<Exception>? OnError { get; init; }
    public bool AutoStart { get; init; }
}

public interface IJobWorkerContext
{
    string WorkerName { get; }
    Task CompleteAsync(IReadOnlyDictionary<string, object?>? variables = null, RequestOptions? options = null, CancellationToken cancellationToken = default);
    Task FailAsync(string errorMessage, int? retries = null, RequestOptions? options = null, CancellationToken cancellationToken = default);
    Task ExtendLockAsync(int lockDuration, RequestOptions? options = null, CancellationToken cancellationToken = default);
}

public delegate Task<IReadOnlyDictionary<string, object?>?> JobHandler(Job job, IJobWorkerContext context, CancellationToken cancellationToken);

public sealed class JobWorker : IDisposable
{
    private readonly ArtificialFlowClient _client;
    private readonly string _type;
    private readonly JobHandler _handler;
    private readonly JobWorkerOptions _options;
    private readonly string _workerName;
    private readonly CancellationTokenSource _cts = new();
    private volatile bool _running;

    public JobWorker(ArtificialFlowClient client, string type, JobHandler handler, JobWorkerOptions? options = null)
    {
        _client = client;
        _type = type;
        _handler = handler;
        _options = options ?? new JobWorkerOptions();
        _workerName = string.IsNullOrWhiteSpace(_options.WorkerName)
            ? $"dotnet-worker-{Environment.ProcessId}"
            : _options.WorkerName!;
        if (_options.AutoStart)
        {
            Start();
        }
    }

    public bool IsRunning => _running;

    public void Start()
    {
        if (_running)
        {
            return;
        }

        _running = true;
        _ = PollAsync();
    }

    public void Stop()
    {
        _running = false;
        try
        {
            _cts.Cancel();
        }
        catch (ObjectDisposedException)
        {
        }
    }

    public async Task<int> RunOnceAsync(CancellationToken cancellationToken = default)
    {
        var response = await _client.activateJobsAsync(new ActivateJobsRequest
        {
            Type = _type,
            Worker = _workerName,
            MaxJobs = _options.MaxJobs,
            TimeoutMs = _options.Timeout,
            LockDurationMs = _options.LockDuration,
        }, cancellationToken: cancellationToken).ConfigureAwait(false);

        var jobs = response.Jobs ?? new List<Job>();
        await Task.WhenAll(jobs.Select(job => HandleJobAsync(job, cancellationToken))).ConfigureAwait(false);
        return jobs.Count;
    }

    public void Dispose()
    {
        Stop();
        _cts.Dispose();
    }

    private async Task PollAsync()
    {
        while (_running)
        {
            try
            {
                await RunOnceAsync(_cts.Token).ConfigureAwait(false);
            }
            catch (OperationCanceledException) when (!_running)
            {
                return;
            }
            catch (Exception error)
            {
                _options.OnError?.Invoke(error);
            }

            if (!_running)
            {
                return;
            }

            try
            {
                await Task.Delay(_options.PollInterval, _cts.Token).ConfigureAwait(false);
            }
            catch (OperationCanceledException)
            {
                return;
            }
        }
    }

    private async Task HandleJobAsync(Job job, CancellationToken cancellationToken)
    {
        var context = new JobWorkerContext(_client, job.Key, _workerName);
        try
        {
            var result = await _handler(job, context, cancellationToken).ConfigureAwait(false);
            await context.CompleteAsync(result, cancellationToken: cancellationToken).ConfigureAwait(false);
        }
        catch (Exception error)
        {
            var retries = _options.FailRetries != null
                ? _options.FailRetries(job, error)
                : Math.Max((job.Retries == 0 ? 1 : job.Retries) - 1, 0);
            await context.FailAsync(error.Message, retries, cancellationToken: cancellationToken).ConfigureAwait(false);
        }
    }

    private sealed class JobWorkerContext : IJobWorkerContext
    {
        private readonly ArtificialFlowClient _client;
        private readonly string _jobKey;

        public JobWorkerContext(ArtificialFlowClient client, string jobKey, string workerName)
        {
            _client = client;
            _jobKey = jobKey;
            WorkerName = workerName;
        }

        public string WorkerName { get; }

        public Task CompleteAsync(IReadOnlyDictionary<string, object?>? variables = null, RequestOptions? options = null, CancellationToken cancellationToken = default)
        {
            return _client.completeJobAsync(_jobKey, new CompleteJobRequest
            {
                Worker = WorkerName,
                Variables = variables == null ? new Dictionary<string, object?>() : new Dictionary<string, object?>(variables),
            }, options, cancellationToken);
        }

        public Task FailAsync(string errorMessage, int? retries = null, RequestOptions? options = null, CancellationToken cancellationToken = default)
        {
            return _client.failJobAsync(_jobKey, new FailJobRequest
            {
                Worker = WorkerName,
                ErrorMessage = errorMessage,
                Retries = retries,
            }, options, cancellationToken);
        }

        public Task ExtendLockAsync(int lockDuration, RequestOptions? options = null, CancellationToken cancellationToken = default)
        {
            return _client.extendJobLockAsync(_jobKey, new ExtendJobLockRequest
            {
                Worker = WorkerName,
                LockDurationMs = lockDuration,
            }, options, cancellationToken);
        }
    }
}
