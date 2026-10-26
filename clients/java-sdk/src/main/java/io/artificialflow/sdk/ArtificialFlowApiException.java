package io.artificialflow.sdk;

import com.fasterxml.jackson.databind.JsonNode;
import com.fasterxml.jackson.databind.ObjectMapper;

public class ArtificialFlowApiException extends RuntimeException {
    private static final ObjectMapper MAPPER = new ObjectMapper();
    private final int status;
    private final String statusText;
    private final String body;
    private final String method;
    private final String path;
    private final String correlationId;
    private final ProblemDetails problem;

    public ArtificialFlowApiException(String method, String path, int status, String statusText, String body, String correlationHeader) {
        this(method, path, status, statusText, body, parseProblem(body), correlationHeader);
    }

    private ArtificialFlowApiException(
        String method,
        String path,
        int status,
        String statusText,
        String body,
        ProblemDetails problem,
        String correlationHeader
    ) {
        super(buildMessage(method, path, status, statusText, body, problem));
        this.method = method;
        this.path = path;
        this.status = status;
        this.statusText = statusText == null ? "" : statusText;
        this.body = body == null ? "" : body;
        this.problem = problem;
        this.correlationId = problem != null && problem.correlationId != null ? problem.correlationId : correlationHeader;
    }

    public int getStatus() {
        return status;
    }

    public String getStatusText() {
        return statusText;
    }

    public String getBody() {
        return body;
    }

    public String getMethod() {
        return method;
    }

    public String getPath() {
        return path;
    }

    public String getCorrelationId() {
        return correlationId;
    }

    public ProblemDetails getProblem() {
        return problem;
    }

    private static ProblemDetails parseProblem(String body) {
        if (body == null || body.isBlank()) {
            return null;
        }
        try {
            JsonNode node = MAPPER.readTree(body);
            if (node != null && node.isObject() && node.has("status") && node.get("status").isNumber()
                && node.has("title") && node.get("title").isTextual()) {
                return MAPPER.treeToValue(node, ProblemDetails.class);
            }
        } catch (Exception ignored) {
            return null;
        }
        return null;
    }

    private static String buildMessage(String method, String path, int status, String statusText, String body, ProblemDetails problem) {
        if (problem != null && problem.title != null) {
            return method + " " + path + " returned " + status + " " + problem.title
                + (problem.detail == null || problem.detail.isEmpty() ? "" : ": " + problem.detail);
        }
        return method + " " + path + " returned " + status + " " + (statusText == null ? "" : statusText)
            + (body == null || body.isEmpty() ? "" : ": " + body);
    }
}
