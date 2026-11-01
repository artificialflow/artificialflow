package io.artificialflow.sdk;

public class ListWorkflowsOptions extends RequestOptions {
    private Integer page;
    private Integer pageSize;
    private String source;

    public Integer getPage() {
        return page;
    }

    public ListWorkflowsOptions page(Integer page) {
        this.page = page;
        return this;
    }

    public Integer getPageSize() {
        return pageSize;
    }

    public ListWorkflowsOptions pageSize(Integer pageSize) {
        this.pageSize = pageSize;
        return this;
    }

    public String getSource() {
        return source;
    }

    public ListWorkflowsOptions source(String source) {
        this.source = source;
        return this;
    }
}
