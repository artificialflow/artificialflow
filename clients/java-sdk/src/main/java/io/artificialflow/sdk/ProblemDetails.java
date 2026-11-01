package io.artificialflow.sdk;

import com.fasterxml.jackson.annotation.JsonIgnoreProperties;
import com.fasterxml.jackson.annotation.JsonProperty;

@JsonIgnoreProperties(ignoreUnknown = true)
public class ProblemDetails {
    @JsonProperty("type")
    public String type;
    @JsonProperty("title")
    public String title;
    @JsonProperty("status")
    public Integer status;
    @JsonProperty("detail")
    public String detail;
    @JsonProperty("instance")
    public String instance;
    @JsonProperty("correlationId")
    public String correlationId;
}
