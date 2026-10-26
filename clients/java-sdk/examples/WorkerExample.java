import io.artificialflow.sdk.ArtificialFlowClient;
import io.artificialflow.sdk.ArtificialFlowClientOptions;
import io.artificialflow.sdk.worker.JobWorker;
import io.artificialflow.sdk.worker.JobWorkerOptions;
import java.util.Map;

public final class WorkerExample {
    public static void main(String[] args) {
        String token = System.getenv("ARTIFICIALFLOW_TOKEN");
        String jobType = System.getenv("ARTIFICIALFLOW_WORKER_JOB_TYPE");
        if (jobType == null || jobType.isBlank()) {
            throw new IllegalStateException("Set ARTIFICIALFLOW_WORKER_JOB_TYPE");
        }
        ArtificialFlowClientOptions options = new ArtificialFlowClientOptions()
            .baseUrl(System.getenv().getOrDefault("ARTIFICIALFLOW_BASE_URL", "http://localhost:9100/api"));
        if (token != null && !token.isBlank()) {
            options.token(token);
        }
        ArtificialFlowClient client = new ArtificialFlowClient(options);
        JobWorker worker = client.createWorker(jobType, (job, context) -> Map.of(
            "processedBy", "java-sdk-worker-example",
            "ok", true
        ), new JobWorkerOptions().workerName("java-sdk-example-worker").maxJobs(1));
        worker.runOnce();
    }
}
