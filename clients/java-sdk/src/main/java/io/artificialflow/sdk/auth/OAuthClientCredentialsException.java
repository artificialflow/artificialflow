package io.artificialflow.sdk.auth;

public class OAuthClientCredentialsException extends RuntimeException {
    private final Integer status;

    public OAuthClientCredentialsException(String message) {
        this(message, null);
    }

    public OAuthClientCredentialsException(String message, Integer status) {
        super(message);
        this.status = status;
    }

    public Integer getStatus() {
        return status;
    }
}
