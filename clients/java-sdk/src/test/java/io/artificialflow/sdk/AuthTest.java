package io.artificialflow.sdk;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertThrows;
import static org.junit.jupiter.api.Assertions.assertTrue;

import com.sun.net.httpserver.HttpServer;
import io.artificialflow.sdk.auth.OAuthClientCredentialsException;
import io.artificialflow.sdk.auth.OAuthClientCredentialsOptions;
import io.artificialflow.sdk.auth.OAuthClientCredentialsTokenProvider;
import java.net.InetSocketAddress;
import java.nio.charset.StandardCharsets;
import java.util.ArrayList;
import java.util.List;
import java.util.concurrent.atomic.AtomicInteger;
import org.junit.jupiter.api.AfterEach;
import org.junit.jupiter.api.Test;

class AuthTest {
    private HttpServer server;
    private final List<String> calls = new ArrayList<>();

    @AfterEach
    void tearDown() {
        if (server != null) {
            server.stop(0);
        }
    }

    @Test
    void cachesAndRefreshesClientCredentials() throws Exception {
        AtomicInteger tokens = new AtomicInteger();
        start(exchange -> {
            int n = tokens.incrementAndGet();
            return "{\"access_token\":\"oauth-" + n + "\",\"token_type\":\"Bearer\",\"expires_in\":60}";
        });
        long[] now = {1_700_000_000_000L};
        OAuthClientCredentialsTokenProvider provider = new OAuthClientCredentialsTokenProvider(
            new OAuthClientCredentialsOptions()
                .tokenUrl("http://127.0.0.1:" + server.getAddress().getPort() + "/token")
                .clientId("artificialflow-sdk-client")
                .clientSecret("private-client-secret")
                .scopes(List.of("openid"))
                .refreshSkewMs(10_000)
                .clock(() -> now[0])
        );
        assertEquals("oauth-1", provider.getToken());
        assertEquals("oauth-1", provider.getToken());
        now[0] += 50_001;
        assertEquals("oauth-2", provider.getToken());
        assertEquals(2, calls.size());
        assertTrue(calls.get(0).contains("grant_type=client_credentials"));
        assertTrue(calls.get(0).contains("client_id=artificialflow-sdk-client"));
    }

    @Test
    void rejectsNonHttpsTokenUrl() {
        assertThrows(OAuthClientCredentialsException.class, () -> new OAuthClientCredentialsTokenProvider(
            new OAuthClientCredentialsOptions()
                .tokenUrl("http://iam.example/token")
                .clientId("id")
                .clientSecret("secret")
        ));
    }

    @Test
    void clientUsesAccessToken() throws Exception {
        start(exchange -> {
            if (exchange.startsWith("POST /token")) {
                return "{\"access_token\":\"oauth-client-token\",\"token_type\":\"Bearer\",\"expires_in\":60}";
            }
            return "{\"status\":\"ok\"}";
        });
        int port = server.getAddress().getPort();
        ArtificialFlowClient client = new ArtificialFlowClient(new ArtificialFlowClientOptions()
            .baseUrl("http://127.0.0.1:" + port + "/api")
            .auth(new OAuthClientCredentialsOptions()
                .tokenUrl("http://127.0.0.1:" + port + "/token")
                .clientId("artificialflow-sdk-client")
                .clientSecret("private-client-secret")));
        assertEquals("ok", client.commandHealth(null).status());
        assertTrue(calls.stream().anyMatch(call -> call.contains("Bearer oauth-client-token")));
    }

    private void start(java.util.function.Function<String, String> handler) throws Exception {
        server = HttpServer.create(new InetSocketAddress("127.0.0.1", 0), 0);
        server.createContext("/", exchange -> {
            String body = new String(exchange.getRequestBody().readAllBytes(), StandardCharsets.UTF_8);
            StringBuilder recorded = new StringBuilder(exchange.getRequestMethod() + " " + exchange.getRequestURI().getPath());
            exchange.getRequestHeaders().forEach((name, values) ->
                recorded.append("\n").append(name).append(": ").append(String.join(",", values)));
            recorded.append("\n").append(body);
            calls.add(recorded.toString());
            String response = handler.apply(recorded.toString());
            byte[] bytes = response.getBytes(StandardCharsets.UTF_8);
            exchange.getResponseHeaders().add("Content-Type", "application/json");
            exchange.sendResponseHeaders(200, bytes.length);
            exchange.getResponseBody().write(bytes);
            exchange.close();
        });
        server.start();
    }
}
