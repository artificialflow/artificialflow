package io.artificialflow.sdk;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertThrows;
import static org.junit.jupiter.api.Assertions.assertTrue;

import com.sun.net.httpserver.HttpServer;
import io.artificialflow.sdk.model.Models;
import io.artificialflow.sdk.worker.JobWorker;
import io.artificialflow.sdk.worker.JobWorkerOptions;
import java.net.InetSocketAddress;
import java.nio.charset.StandardCharsets;
import java.util.ArrayList;
import java.util.List;
import java.util.Map;
import java.util.concurrent.atomic.AtomicInteger;
import org.junit.jupiter.api.AfterEach;
import org.junit.jupiter.api.Test;

class ClientTest {
    private HttpServer server;
    private final List<String> calls = new ArrayList<>();

    @AfterEach
    void tearDown() {
        if (server != null) {
            server.stop(0);
        }
    }

    @Test
    void healthAndIdentity() throws Exception {
        start(Map.of(
            "GET /api/health", json(200, "{\"status\":\"ok\"}"),
            "GET /api/identity/me", json(200, "{\"authenticated\":true,\"principal\":{\"subject\":\"admin\",\"roles\":[\"artificialflowadmin\"]}}"),
            "GET /api/identity/config", json(200, "{\"deployment_mode\":true,\"provider_name\":\"OIDC\"}")
        ));
        ArtificialFlowClient client = client();
        assertEquals("ok", client.commandHealth(null).status());
        assertEquals("admin", client.getIdentity(null).principal.subject);
        assertEquals(Boolean.TRUE, client.getIdentityConfig(null).deploymentMode);
        assertTrue(calls.get(0).contains("Authorization: Bearer token-1"));
    }

    @Test
    void workflowAndWorkerOperations() throws Exception {
        start(Map.of(
            "GET /api/query/workflows", json(200, "{\"workflows\":[{\"id\":\"order\"}]}"),
            "POST /api/workflows", json(200, "{\"id\":\"order\"}"),
            "POST /api/jobs/activate", json(200, "{\"jobs\":[{\"key\":\"201\",\"type\":\"payment\",\"retries\":3}]}"),
            "POST /api/jobs/201/complete", text(204, "")
        ));
        ArtificialFlowClient client = client();
        assertEquals("order", client.listWorkflows(new ListWorkflowsOptions()).get(0).id);
        assertEquals("order", client.deployWorkflow("<bpmn/>", null).id);
        JobWorker worker = client.createWorker("payment", (job, context) -> Map.of("paid", true), new JobWorkerOptions().workerName("worker-1"));
        assertEquals(1, worker.runOnce());
        assertTrue(calls.stream().anyMatch(call -> call.contains("POST /api/jobs/201/complete")));
    }

    @Test
    void userTaskSearchClaimAndCompletePassEndUserAsParameters() throws Exception {
        start(Map.of(
            "GET /api/tasks", json(200, "{\"tasks\":[{\"key\":\"9\",\"processInstanceKey\":\"42\",\"executionId\":\"exec-1\",\"canClaim\":true}]}"),
            "POST /api/instances/42/tasks/exec-1/claim", json(200, "{\"key\":\"9\",\"executionId\":\"exec-1\"}"),
            "POST /api/instances/42/tasks/exec-1/complete", text(204, "")
        ));
        ArtificialFlowClient client = client();
        List<Models.UserTask> tasks = client.searchTasks(new SearchTasksOptions()
            .candidateUser("alice@example.com")
            .candidateGroups(List.of("accountant", "finance"))
            .includeUnassigned(true)
            .limit(10));
        assertEquals("42", tasks.get(0).processInstanceKey);
        assertTrue(calls.get(0).contains("?candidateUser=alice%40example.com&candidateGroup=accountant&candidateGroup=finance&includeUnassigned=true&limit=10"));

        client.claimUserTask("42", "exec-1", "alice@example.com", null);
        assertTrue(calls.get(1).contains("\"assignee\":\"alice@example.com\""));
        client.completeUserTask("42", "exec-1", null, null);
        assertTrue(calls.get(2).endsWith("\n{}"));
    }

    @Test
    void problemDetails() throws Exception {
        start(Map.of("GET /api/health", json(401, "{\"type\":\"about:blank\",\"title\":\"Unauthorized\",\"status\":401,\"detail\":\"token required\",\"correlationId\":\"corr-1\"}")));
        ArtificialFlowApiException error = assertThrows(ArtificialFlowApiException.class, () -> client().commandHealth(null));
        assertEquals(401, error.getStatus());
        assertEquals("token required", error.getProblem().detail);
        assertEquals("corr-1", error.getCorrelationId());
    }

    private ArtificialFlowClient client() {
        return new ArtificialFlowClient(new ArtificialFlowClientOptions()
            .baseUrl("http://127.0.0.1:" + server.getAddress().getPort() + "/api")
            .token("token-1"));
    }

    private void start(Map<String, Exchange> routes) throws Exception {
        if (server != null) {
            server.stop(0);
        }
        calls.clear();
        server = HttpServer.create(new InetSocketAddress("127.0.0.1", 0), 0);
        server.createContext("/", exchange -> {
            String key = exchange.getRequestMethod() + " " + exchange.getRequestURI().getPath();
            String rawQuery = exchange.getRequestURI().getRawQuery();
            StringBuilder recorded = new StringBuilder(key).append(rawQuery == null ? "" : "?" + rawQuery);
            exchange.getRequestHeaders().forEach((name, values) -> recorded.append("\n").append(name).append(": ").append(String.join(",", values)));
            String requestBody = new String(exchange.getRequestBody().readAllBytes(), StandardCharsets.UTF_8);
            if (!requestBody.isEmpty()) {
                recorded.append("\n").append(requestBody);
            }
            calls.add(recorded.toString());
            Exchange response = routes.getOrDefault(key, text(404, "missing " + key));
            byte[] body = response.body.getBytes(StandardCharsets.UTF_8);
            exchange.getResponseHeaders().add("Content-Type", response.contentType);
            exchange.sendResponseHeaders(response.status, body.length == 0 ? -1 : body.length);
            if (body.length > 0) {
                exchange.getResponseBody().write(body);
            }
            exchange.close();
        });
        server.start();
    }

    private static Exchange json(int status, String body) {
        return new Exchange(status, "application/json", body);
    }

    private static Exchange text(int status, String body) {
        return new Exchange(status, "text/plain", body);
    }

    private record Exchange(int status, String contentType, String body) {}
}
