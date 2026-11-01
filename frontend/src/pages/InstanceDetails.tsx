import { useEffect, useState, useCallback } from "react";
import { useParams, useNavigate } from "react-router";
import { api, type IdentityResponse, type InstanceJob, type UserTask, type WorkflowInstance, type InstanceMigrationResult } from "@/lib/api";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Badge } from "@/components/ui/badge";
import { ArrowLeft, Save, RefreshCw } from "lucide-react";
import BpmnViewer from "@/components/bpmn/BpmnViewer";
import VariablesEditor from "@/components/VariablesEditor";
import { isAdmin } from "@/lib/roles";

export default function InstanceDetails() {
  const { id } = useParams<{ id: string }>();
  const navigate = useNavigate();
  const [instance, setInstance] = useState<WorkflowInstance | null>(null);
  const [loading, setLoading] = useState(true);
  const [refreshing, setRefreshing] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [variables, setVariables] = useState<Record<string, unknown>>({});
  const [saving, setSaving] = useState(false);
  const [bpmnXml, setBpmnXml] = useState<string>("");
  const [processName, setProcessName] = useState<string>("");
  const [processId, setProcessId] = useState<string>("");
  const [processVersion, setProcessVersion] = useState<number | null>(null);
  const [identity, setIdentity] = useState<IdentityResponse | null>(null);
  const [jobs, setJobs] = useState<InstanceJob[]>([]);
  const [migrationTarget, setMigrationTarget] = useState("");
  const [migrationMapping, setMigrationMapping] = useState("{}");
  const [migrationOperationId, setMigrationOperationId] = useState("");
  const [migrationResult, setMigrationResult] = useState<InstanceMigrationResult | null>(null);
  const [migrating, setMigrating] = useState(false);

  const fetchInstance = useCallback(async (isRefresh = false) => {
    if (!id) return;
    
    if (isRefresh) {
        setRefreshing(true);
    } else {
        setLoading(true);
    }

    try {
      const data = await api.getInstance(id);
      setInstance(data);
      setVariables(data.context || {});

      // Fetch workflow definition for XML
      try {
          const currentIdentity = await api.getIdentity();
          setIdentity(currentIdentity);
          if (isAdmin(currentIdentity)) {
            try {
              setJobs(await api.listInstanceJobs(id));
            } catch (jobsErr) {
              console.error("Failed to load jobs:", jobsErr);
              setJobs([]);
            }
          } else {
            setJobs([]);
          }
      } catch (identityErr) {
          console.error("Failed to load identity:", identityErr);
      }

      try {
          const workflow = await api.getWorkflow(data.workflow_id);
          setProcessName(workflow.name || workflow.process_definition_id || data.workflow_id);
          setProcessId(workflow.process_definition_id || data.workflow_id);
          setProcessVersion(
            typeof workflow.version === "number" && Number.isFinite(workflow.version)
              ? workflow.version
              : null,
          );
          if (workflow.bpmn_xml) {
              setBpmnXml(workflow.bpmn_xml);
          }
      } catch (wfErr) {
          console.error("Failed to load workflow definition:", wfErr);
          setProcessName(`Process ${data.workflow_id}`);
          setProcessId(data.workflow_id);
          setProcessVersion(null);
      }

    } catch (err) {
      console.error(err);
      setError("Failed to load instance details");
    } finally {
      setLoading(false);
      setRefreshing(false);
    }
  }, [id]);

  useEffect(() => {
    fetchInstance();
  }, [fetchInstance]);

  const handleCompleteTask = async (stepId: string) => {
    if (!id) return;
    try {
        await api.completeTask(id, stepId);
        // Refresh data
        const data = await api.getInstance(id);
        setInstance(data);
        setVariables(data.context || {});
    } catch (err) {
        console.error("Failed to complete task:", err);
        alert("Failed to complete task");
    }
  };

  const handleClaimUserTask = async (executionId: string) => {
    if (!id) return;
    try {
      await api.claimUserTask(id, executionId);
      const data = await api.getInstance(id);
      setInstance(data);
      setVariables(data.context || {});
    } catch (err) {
      console.error("Failed to take task:", err);
      alert(err instanceof Error ? err.message : "Failed to take task");
    }
  };

  const handleCompleteUserTask = async (executionId: string) => {
    if (!id) return;
    try {
      await api.completeUserTask(id, executionId);
      const data = await api.getInstance(id);
      setInstance(data);
      setVariables(data.context || {});
    } catch (err) {
      console.error("Failed to complete user task:", err);
      alert(err instanceof Error ? err.message : "Failed to complete user task");
    }
  };

  const handleSaveVariables = async () => {
    if (!id) return;
    setSaving(true);
    try {
      await api.updateInstanceVariables(id, variables);
      // Refresh data
      const data = await api.getInstance(id);
      setInstance(data);
      setVariables(data.context || {});
      alert("Variables updated successfully");
    } catch (err) {
      console.error(err);
      alert(err instanceof Error ? err.message : "Failed to update variables.");
    } finally {
      setSaving(false);
    }
  };

  const runMigration = async (dryRun: boolean) => {
    if (!id || !instance || !migrationTarget.trim()) return;
    setMigrating(true);
    try {
      const elementMapping = JSON.parse(migrationMapping) as Record<string, string>;
      const result = await api.migrateInstance(id, {
        sourceDefinitionKey: instance.workflow_id,
        targetDefinitionKey: migrationTarget.trim(),
        elementMapping,
        operationId: migrationOperationId.trim() || undefined,
      }, dryRun);
      setMigrationResult(result);
      if (result.operationId) setMigrationOperationId(result.operationId);
      if (!dryRun && result.status === "APPLIED") await fetchInstance(true);
    } catch (err) {
      setMigrationResult({
        operationId: "", instanceKey: id, dryRun, status: "REJECTED",
        message: err instanceof Error ? err.message : "Migration failed",
      });
    } finally {
      setMigrating(false);
    }
  };

  if (loading) return <div className="p-8">Loading...</div>;
  if (error || !instance) return <div className="p-8 text-red-500">{error || "Instance not found"}</div>;

  const canManageTasks = isAdmin(identity);
  const canEditVariables = canManageTasks && instance.status !== "COMPLETED";

  return (
    <div className="space-y-6">
      <div className="flex items-center space-x-4">
        <Button variant="ghost" size="icon" onClick={() => navigate("/instances")}>
          <ArrowLeft className="h-4 w-4" />
        </Button>
        <div className="min-w-0">
          <h1 className="text-2xl font-bold truncate">{processName || "Instance"}</h1>
          <p className="text-sm text-muted-foreground font-mono truncate">
            Instance ID: {instance.id}
            {" · "}
            Business Process ID: {processId || instance.workflow_id}
            {" · "}
            Definition ID: {instance.workflow_id}
          </p>
        </div>
        <div className="ml-auto flex items-center gap-3 shrink-0">
            {processVersion != null ? (
              <Badge variant="secondary" className="font-mono text-sm px-3 py-1">
                Version {processVersion}
              </Badge>
            ) : null}
            <Button variant="outline" size="sm" onClick={() => fetchInstance(true)} disabled={loading || refreshing}>
                <RefreshCw className={`mr-2 h-4 w-4 ${refreshing ? "animate-spin" : ""}`} />
                Refresh
            </Button>
        </div>
      </div>

      {/* BPMN Visualization */}
      {bpmnXml && (
        <Card className="h-[500px] flex flex-col">
            <CardHeader className="py-3 px-4 border-b flex flex-row items-center justify-between">
                <CardTitle className="text-base">Process Visualization</CardTitle>
                <div className="flex gap-2">
                   <Badge variant="outline" className="text-blue-600 border-blue-200 bg-blue-50">Active</Badge>
                   <Badge variant="outline" className="text-green-600 border-green-200 bg-green-50">Completed</Badge>
                </div>
            </CardHeader>
            <div className="flex-1 bg-gray-50">
                <BpmnViewer 
                  xml={bpmnXml} 
                  activeStepId={instance.current_step} 
                  executions={instance.executions}
                />
            </div>
        </Card>
      )}

      <div className="grid gap-6 md:grid-cols-2">
        <div className="space-y-6">
            <Card>
              <CardHeader>
                <CardTitle>Details</CardTitle>
              </CardHeader>
              <CardContent className="space-y-4">
                <div className="grid grid-cols-2 gap-2 text-sm">
                  <div className="font-medium">Instance ID:</div>
                  <div className="font-mono text-xs break-all">{instance.id}</div>

                  <div className="font-medium">Process Name:</div>
                  <div>{processName || "—"}</div>

                  <div className="font-medium">Business Process ID:</div>
                  <div className="font-mono text-xs break-all">{processId || instance.workflow_id}</div>

                  <div className="font-medium">Definition ID:</div>
                  <div className="font-mono text-xs break-all text-muted-foreground">{instance.workflow_id}</div>
                  
                  <div className="font-medium">Status:</div>
                  <div>
                    <Badge
                      variant={
                        instance.status === "COMPLETED"
                          ? "success"
                          : instance.status === "FAILED"
                          ? "destructive"
                          : "default"
                      }
                    >
                      {instance.status}
                    </Badge>
                  </div>
                  
                  <div className="font-medium">Current Step(s):</div>
                  <div>
                    {instance.executions
                      .filter((e) => e.status === "ACTIVE")
                      .map((e) => e.step_id)
                      .join(", ") || "None"}
                  </div>
                  
                  <div className="font-medium">Created At:</div>
                  <div>{new Date(instance.created_at).toLocaleString()}</div>
                  
                  <div className="font-medium">Updated At:</div>
                  <div>{new Date(instance.updated_at).toLocaleString()}</div>
                </div>
              </CardContent>
            </Card>

            <Card>
                <CardHeader>
                    <CardTitle>Active Tasks</CardTitle>
                    <div className="text-xs text-muted-foreground">
                      Signed in as {identity?.principal?.email || identity?.principal?.name || identity?.principal?.subject || "unknown user"}
                    </div>
                </CardHeader>
                <CardContent>
                    {instance.executions.filter(e => e.status === 'ACTIVE').length === 0 ? (
                        <div className="text-sm text-muted-foreground">No active tasks.</div>
                    ) : (
                        <div className="space-y-3">
                            {instance.executions.filter(e => e.status === 'ACTIVE').map(exec => (
                                <div key={exec.id} className="flex items-center justify-between p-3 border rounded-md bg-white">
                                    <div className="space-y-1">
                                        <div className="text-sm font-medium">{exec.step_id}</div>
                                        <div className="text-xs text-muted-foreground">Started: {new Date(exec.start_time).toLocaleTimeString()}</div>
                                        <div className="text-[10px] text-gray-400">ID: {exec.id}</div>
                                        {exec.task ? <TaskAssignment task={exec.task} /> : null}
                                    </div>
                                    {exec.task ? (
                                      <div className="flex flex-col items-end gap-2">
                                        {canManageTasks && exec.task.canComplete ? (
                                          <Button size="sm" onClick={() => handleCompleteUserTask(exec.id)}>
                                            Complete
                                          </Button>
                                        ) : canManageTasks && exec.task.canClaim ? (
                                          <Button size="sm" variant="outline" onClick={() => handleClaimUserTask(exec.id)}>
                                            Take Task
                                          </Button>
                                        ) : (
                                          <Button size="sm" disabled>
                                            Not Assigned
                                          </Button>
                                        )}
                                        {exec.task.claimedBy ? (
                                          <div className="text-[10px] text-muted-foreground">Claimed by {exec.task.claimedBy}</div>
                                        ) : null}
                                      </div>
                                    ) : canManageTasks ? (
                                      <Button size="sm" onClick={() => handleCompleteTask(exec.id)}>
                                          Complete
                                      </Button>
                                    ) : (
                                      <Button size="sm" disabled>
                                        Waiting for worker
                                      </Button>
                                    )}
                                </div>
                            ))}
                        </div>
                    )}
                </CardContent>
            </Card>

            {canManageTasks && (
              <Card>
                <CardHeader>
                  <CardTitle>Jobs</CardTitle>
                  <div className="text-xs text-muted-foreground">
                    Retry failed or unlocked jobs. Fail stuck activated jobs from ops.
                  </div>
                </CardHeader>
                <CardContent className="space-y-3">
                  {jobs.length === 0 ? (
                    <div className="text-sm text-muted-foreground">No jobs for this instance.</div>
                  ) : (
                    jobs.map((job) => (
                      <div key={String(job.key)} className="flex items-center justify-between rounded-md border p-3">
                        <div className="space-y-1 text-sm">
                          <div className="font-medium">{job.type || "job"} · {job.elementId}</div>
                          <div className="text-xs text-muted-foreground">
                            {job.state}
                            {job.worker ? ` · worker ${job.worker}` : ""}
                            {` · retries ${job.retries}`}
                          </div>
                        </div>
                        <div className="flex gap-2">
                          {(job.state === "FAILED" || job.state === "CREATED" || job.state === "ACTIVATED") && (
                            <Button
                              size="sm"
                              variant="outline"
                              onClick={async () => {
                                try {
                                  await api.retryJob(job.key);
                                  await fetchInstance(true);
                                } catch (err) {
                                  alert(err instanceof Error ? err.message : "Retry failed");
                                }
                              }}
                            >
                              Retry
                            </Button>
                          )}
                          {job.state === "ACTIVATED" && (
                            <Button
                              size="sm"
                              variant="destructive"
                              onClick={async () => {
                                try {
                                  await api.failJob(job.key);
                                  await fetchInstance(true);
                                } catch (err) {
                                  alert(err instanceof Error ? err.message : "Fail job failed");
                                }
                              }}
                            >
                              Fail
                            </Button>
                          )}
                        </div>
                      </div>
                    ))
                  )}
                </CardContent>
              </Card>
            )}
            {canManageTasks && instance.status !== "COMPLETED" && (
              <Card>
                <CardHeader>
                  <CardTitle>Instance Migration</CardTitle>
                  <div className="text-xs text-muted-foreground">
                    Validate a complete active wait-state mapping before applying a newer definition. Completed history is never rewritten.{" "}
                    <a
                      className="underline"
                      href="https://github.com/artificialflow/artificialflow/blob/main/docs/INSTANCE_MIGRATION.md"
                      target="_blank"
                      rel="noreferrer"
                    >
                      Support and undo guidance
                    </a>
                  </div>
                </CardHeader>
                <CardContent className="space-y-3">
                  <input
                    className="w-full rounded-md border px-3 py-2 text-sm"
                    placeholder="Target definition key"
                    value={migrationTarget}
                    onChange={(event) => {
                      setMigrationTarget(event.target.value);
                      setMigrationResult(null);
                    }}
                  />
                  <input
                    className="w-full rounded-md border px-3 py-2 text-sm"
                    aria-label="Migration operation ID"
                    placeholder="Operation ID (generated by dry run)"
                    value={migrationOperationId}
                    onChange={(event) => {
                      setMigrationOperationId(event.target.value);
                      setMigrationResult(null);
                    }}
                  />
                  <textarea
                    className="min-h-28 w-full rounded-md border px-3 py-2 font-mono text-xs"
                    aria-label="Element mapping JSON"
                    value={migrationMapping}
                    onChange={(event) => {
                      setMigrationMapping(event.target.value);
                      setMigrationResult(null);
                    }}
                  />
                  <div className="flex gap-2">
                    <Button variant="outline" disabled={migrating} onClick={() => runMigration(true)}>Dry run</Button>
                    <Button disabled={migrating || migrationResult?.status !== "VALIDATED"} onClick={() => runMigration(false)}>Apply</Button>
                  </div>
                  {migrationResult ? (
                    <div className="rounded-md border p-2 text-xs">
                      {migrationResult.status}
                      {migrationResult.reasonCode ? ` · ${migrationResult.reasonCode}` : ""}
                      {migrationResult.message ? ` · ${migrationResult.message}` : ""}
                    </div>
                  ) : null}
                </CardContent>
              </Card>
            )}
        </div>

        <Card>
          <CardHeader>
            <CardTitle>Variables (Context)</CardTitle>
            <div className="text-xs text-muted-foreground">
              {canEditVariables
                ? "Review values, change their type or value, and add new variables."
                : "Variables are shown as they were stored for this instance."}
            </div>
          </CardHeader>
          <CardContent className="space-y-4">
            <VariablesEditor
              value={variables}
              editable={canEditVariables}
              onChange={setVariables}
            />
            {canEditVariables ? (
              <Button onClick={handleSaveVariables} disabled={saving}>
                <Save className="mr-2 h-4 w-4" />
                {saving ? "Saving..." : "Update Variables"}
              </Button>
            ) : (
              <div className="text-sm text-muted-foreground">
                {instance.status === "COMPLETED"
                  ? "Completed instance variables are read-only."
                  : "Variables are read-only in the web console for business roles."}
              </div>
            )}
          </CardContent>
        </Card>
      </div>
    </div>
  );
}

function TaskAssignment({ task }: { task: UserTask }) {
  const candidateGroups = task.candidateGroups || [];
  const candidateUsers = task.candidateUsers || [];
  return (
    <div className="space-y-1">
      <div className="flex flex-wrap gap-1">
        {task.assignee ? <Badge variant="outline">Assignee: {task.assignee}</Badge> : null}
        {candidateGroups.map((group) => (
          <Badge key={group} variant="secondary">Group: {group}</Badge>
        ))}
        {candidateUsers.map((user) => (
          <Badge key={user} variant="secondary">User: {user}</Badge>
        ))}
        {!task.assignee && candidateGroups.length === 0 && candidateUsers.length === 0 ? (
          <Badge variant="outline">Open task</Badge>
        ) : null}
      </div>
    </div>
  );
}
