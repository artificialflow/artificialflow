package io.artificialflow.sdk;

public class SearchInstancesOptions extends RequestOptions {
    private String workflowId;
    private String state;
    private Integer page;
    private Integer pageSize;

    public String getWorkflowId() {
        return workflowId;
    }

    public SearchInstancesOptions workflowId(String workflowId) {
        this.workflowId = workflowId;
        return this;
    }

    public String getState() {
        return state;
    }

    public SearchInstancesOptions state(String state) {
        this.state = state;
        return this;
    }

    public Integer getPage() {
        return page;
    }

    public SearchInstancesOptions page(Integer page) {
        this.page = page;
        return this;
    }

    public Integer getPageSize() {
        return pageSize;
    }

    public SearchInstancesOptions pageSize(Integer pageSize) {
        this.pageSize = pageSize;
        return this;
    }
}
