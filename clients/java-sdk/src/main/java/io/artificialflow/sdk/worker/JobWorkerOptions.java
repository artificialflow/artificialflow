package io.artificialflow.sdk.worker;

import io.artificialflow.sdk.model.Models;
import java.util.function.BiFunction;
import java.util.function.Consumer;

public class JobWorkerOptions {
    private int maxJobs = 32;
    private int pollInterval = 1000;
    private int timeout = 5000;
    private int lockDuration = 30_000;
    private String workerName;
    private BiFunction<Models.Job, Exception, Integer> failRetries;
    private Consumer<Exception> onError;
    private boolean autoStart;

    public int getMaxJobs() { return maxJobs; }
    public JobWorkerOptions maxJobs(int maxJobs) { this.maxJobs = maxJobs; return this; }
    public int getPollInterval() { return pollInterval; }
    public JobWorkerOptions pollInterval(int pollInterval) { this.pollInterval = pollInterval; return this; }
    public int getTimeout() { return timeout; }
    public JobWorkerOptions timeout(int timeout) { this.timeout = timeout; return this; }
    public int getLockDuration() { return lockDuration; }
    public JobWorkerOptions lockDuration(int lockDuration) { this.lockDuration = lockDuration; return this; }
    public String getWorkerName() { return workerName; }
    public JobWorkerOptions workerName(String workerName) { this.workerName = workerName; return this; }
    public BiFunction<Models.Job, Exception, Integer> getFailRetries() { return failRetries; }
    public JobWorkerOptions failRetries(BiFunction<Models.Job, Exception, Integer> failRetries) { this.failRetries = failRetries; return this; }
    public Consumer<Exception> getOnError() { return onError; }
    public JobWorkerOptions onError(Consumer<Exception> onError) { this.onError = onError; return this; }
    public boolean isAutoStart() { return autoStart; }
    public JobWorkerOptions autoStart(boolean autoStart) { this.autoStart = autoStart; return this; }
}
