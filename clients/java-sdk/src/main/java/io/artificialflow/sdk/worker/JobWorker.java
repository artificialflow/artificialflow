package io.artificialflow.sdk.worker;

import io.artificialflow.sdk.ArtificialFlowClient;
import io.artificialflow.sdk.RequestOptions;
import io.artificialflow.sdk.model.Models;
import java.util.List;
import java.util.Map;
import java.util.concurrent.Executors;
import java.util.concurrent.ScheduledExecutorService;
import java.util.concurrent.TimeUnit;

public class JobWorker implements AutoCloseable {
    public interface Handler {
        Map<String, Object> handle(Models.Job job, Context context) throws Exception;
    }

    public interface Context {
        String workerName();
        void complete(Map<String, Object> variables, RequestOptions options);
        void fail(String errorMessage, Integer retries, RequestOptions options);
        void extendLock(int lockDuration, RequestOptions options);
    }

    private final ArtificialFlowClient client;
    private final String type;
    private final Handler handler;
    private final JobWorkerOptions options;
    private final String workerName;
    private final ScheduledExecutorService executor = Executors.newSingleThreadScheduledExecutor();
    private volatile boolean running;

    public JobWorker(ArtificialFlowClient client, String type, Handler handler, JobWorkerOptions options) {
        this.client = client;
        this.type = type;
        this.handler = handler;
        this.options = options == null ? new JobWorkerOptions() : options;
        this.workerName = this.options.getWorkerName() == null || this.options.getWorkerName().isBlank()
            ? "java-worker-" + ProcessHandle.current().pid()
            : this.options.getWorkerName();
        if (this.options.isAutoStart()) {
            start();
        }
    }

    public boolean isRunning() {
        return running;
    }

    public void start() {
        if (running) {
            return;
        }
        running = true;
        executor.scheduleWithFixedDelay(this::safePoll, 0, options.getPollInterval(), TimeUnit.MILLISECONDS);
    }

    public void stop() {
        running = false;
        executor.shutdownNow();
    }

    public int runOnce() {
        Models.ActivateJobsRequest request = new Models.ActivateJobsRequest();
        request.type = type;
        request.worker = workerName;
        request.maxJobs = options.getMaxJobs();
        request.timeoutMs = options.getTimeout();
        request.lockDurationMs = options.getLockDuration();
        Models.ActivateJobsResponse response = client.activateJobs(request, null);
        List<Models.Job> jobs = response == null || response.jobs == null ? List.of() : response.jobs;
        for (Models.Job job : jobs) {
            handleJob(job);
        }
        return jobs.size();
    }

    @Override
    public void close() {
        stop();
    }

    private void safePoll() {
        if (!running) {
            return;
        }
        try {
            runOnce();
        } catch (Exception error) {
            if (options.getOnError() != null) {
                options.getOnError().accept(error);
            }
        }
    }

    private void handleJob(Models.Job job) {
        Context context = new WorkerContext(job.key);
        try {
            Map<String, Object> result = handler.handle(job, context);
            context.complete(result == null ? Map.of() : result, null);
        } catch (Exception error) {
            Integer retries = options.getFailRetries() != null
                ? options.getFailRetries().apply(job, error)
                : Math.max((job.retries == 0 ? 1 : job.retries) - 1, 0);
            context.fail(error.getMessage() == null ? "Job failed" : error.getMessage(), retries, null);
        }
    }

    private final class WorkerContext implements Context {
        private final String jobKey;

        private WorkerContext(String jobKey) {
            this.jobKey = jobKey;
        }

        @Override
        public String workerName() {
            return workerName;
        }

        @Override
        public void complete(Map<String, Object> variables, RequestOptions options) {
            Models.CompleteJobRequest request = new Models.CompleteJobRequest();
            request.worker = workerName;
            request.variables = variables == null ? Map.of() : variables;
            client.completeJob(jobKey, request, options);
        }

        @Override
        public void fail(String errorMessage, Integer retries, RequestOptions options) {
            Models.FailJobRequest request = new Models.FailJobRequest();
            request.worker = workerName;
            request.errorMessage = errorMessage;
            request.retries = retries;
            client.failJob(jobKey, request, options);
        }

        @Override
        public void extendLock(int lockDuration, RequestOptions options) {
            Models.ExtendJobLockRequest request = new Models.ExtendJobLockRequest();
            request.worker = workerName;
            request.lockDurationMs = lockDuration;
            client.extendJobLock(jobKey, request, options);
        }
    }
}
