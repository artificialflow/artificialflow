package io.artificialflow.sdk;

import java.util.List;

public class SearchTasksOptions extends RequestOptions {
    private String assignee;
    private String candidateUser;
    private List<String> candidateGroups;
    private Boolean includeUnassigned;
    private String processInstanceKey;
    private String state;
    private Integer limit;

    public String getAssignee() {
        return assignee;
    }

    /** Tasks assigned to or claimed by this user. */
    public SearchTasksOptions assignee(String assignee) {
        this.assignee = assignee;
        return this;
    }

    public String getCandidateUser() {
        return candidateUser;
    }

    /** Tasks this user may act on: assignee, claim owner or candidate user. */
    public SearchTasksOptions candidateUser(String candidateUser) {
        this.candidateUser = candidateUser;
        return this;
    }

    public List<String> getCandidateGroups() {
        return candidateGroups;
    }

    /** Tasks offered to any of these groups. */
    public SearchTasksOptions candidateGroups(List<String> candidateGroups) {
        this.candidateGroups = candidateGroups;
        return this;
    }

    public Boolean getIncludeUnassigned() {
        return includeUnassigned;
    }

    /** Also return open tasks with no assignee, candidates or claim. */
    public SearchTasksOptions includeUnassigned(Boolean includeUnassigned) {
        this.includeUnassigned = includeUnassigned;
        return this;
    }

    public String getProcessInstanceKey() {
        return processInstanceKey;
    }

    public SearchTasksOptions processInstanceKey(String processInstanceKey) {
        this.processInstanceKey = processInstanceKey;
        return this;
    }

    public String getState() {
        return state;
    }

    /** active (default), completed or all. */
    public SearchTasksOptions state(String state) {
        this.state = state;
        return this;
    }

    public Integer getLimit() {
        return limit;
    }

    public SearchTasksOptions limit(Integer limit) {
        this.limit = limit;
        return this;
    }
}
