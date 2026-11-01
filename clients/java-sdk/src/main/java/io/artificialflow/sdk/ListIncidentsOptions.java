package io.artificialflow.sdk;

public class ListIncidentsOptions extends RequestOptions {
    private String processInstanceKey;
    private Integer limit;

    public String getProcessInstanceKey() {
        return processInstanceKey;
    }

    public ListIncidentsOptions processInstanceKey(String processInstanceKey) {
        this.processInstanceKey = processInstanceKey;
        return this;
    }

    public Integer getLimit() {
        return limit;
    }

    public ListIncidentsOptions limit(Integer limit) {
        this.limit = limit;
        return this;
    }
}
