import io.artificialflow.sdk.ArtificialFlowClient;
import io.artificialflow.sdk.ArtificialFlowClientOptions;
import io.artificialflow.sdk.ListWorkflowsOptions;
import io.artificialflow.sdk.auth.OAuthClientCredentialsOptions;
import io.artificialflow.sdk.worker.JobWorker;
import io.artificialflow.sdk.worker.JobWorkerOptions;
import java.util.Map;

public final class SdkSmokeTest {
    public static void main(String[] args) {
        String token = System.getenv("ARTIFICIALFLOW_TOKEN");
        String tokenUrl = first(System.getenv("OIDC_TOKEN_URL"), System.getenv("ARTIFICIALFLOW_OIDC_TOKEN_URL"));
        String clientId = first(System.getenv("OIDC_CLIENT_ID"), System.getenv("ARTIFICIALFLOW_CLIENT_ID"));
        String clientSecret = first(System.getenv("OIDC_CLIENT_SECRET"), System.getenv("ARTIFICIALFLOW_CLIENT_SECRET"));
        ArtificialFlowClientOptions options = new ArtificialFlowClientOptions()
            .baseUrl(first(System.getenv("ARTIFICIALFLOW_BASE_URL"), "http://localhost:9100/api"));
        if (token != null && !token.isBlank()) {
            options.token(token);
        } else if (tokenUrl != null && clientId != null && clientSecret != null) {
            options.auth(new OAuthClientCredentialsOptions().tokenUrl(tokenUrl).clientId(clientId).clientSecret(clientSecret));
        }

        try (ArtificialFlowClient client = new ArtificialFlowClient(options)) {
            System.out.println(client.getIdentity(null).principal.subject);
            System.out.println(client.listWorkflows(new ListWorkflowsOptions().page(1).pageSize(20)).size());
            String jobType = System.getenv("ARTIFICIALFLOW_WORKER_JOB_TYPE");
            if (jobType != null && !jobType.isBlank() && !jobType.startsWith("<")) {
                JobWorker worker = client.createWorker(jobType, (job, context) -> Map.of("handledBy", "java-sdk-smoke-test"), new JobWorkerOptions().workerName("java-sdk-smoke-worker").maxJobs(1));
                System.out.println("Completed jobs: " + worker.runOnce());
            }
        }
    }

    private static String first(String... values) {
        for (String value : values) {
            if (value != null && !value.isBlank()) {
                return value;
            }
        }
        return null;
    }
}
