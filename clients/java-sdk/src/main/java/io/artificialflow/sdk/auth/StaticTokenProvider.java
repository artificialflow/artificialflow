package io.artificialflow.sdk.auth;

import java.util.Objects;
import java.util.function.Supplier;

public final class StaticTokenProvider implements TokenProvider {
    private final Supplier<String> factory;

    public StaticTokenProvider(String token) {
        Objects.requireNonNull(token, "token");
        this.factory = () -> token;
    }

    public StaticTokenProvider(Supplier<String> factory) {
        this.factory = Objects.requireNonNull(factory, "factory");
    }

    @Override
    public String getToken() {
        return factory.get();
    }
}
