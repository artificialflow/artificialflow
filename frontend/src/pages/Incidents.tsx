import { useCallback, useEffect, useState } from "react";
import { Link } from "react-router";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import { api, type Incident } from "@/lib/api";
import { AlertTriangle, RefreshCw } from "lucide-react";

function formatDate(value?: string): string {
  if (!value) return "—";
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return value;
  return date.toLocaleString();
}

export default function Incidents() {
  const [incidents, setIncidents] = useState<Incident[]>([]);
  const [loading, setLoading] = useState(true);
  const [refreshing, setRefreshing] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [actionKey, setActionKey] = useState<string | null>(null);
  const [actionMessage, setActionMessage] = useState<string | null>(null);

  const fetchIncidents = useCallback(async (showLoading = true) => {
    if (showLoading) {
      setLoading(true);
    } else {
      setRefreshing(true);
    }
    try {
      const rows = await api.listIncidents({ limit: 200 });
      setIncidents(rows);
      setError(null);
    } catch (err) {
      console.error("Failed to load incidents", err);
      setError("Failed to load incidents");
    } finally {
      setLoading(false);
      setRefreshing(false);
    }
  }, []);

  useEffect(() => {
    void fetchIncidents();
  }, [fetchIncidents]);

  const retry = async (incident: Incident) => {
    if (!incident.jobKey) return;
    setActionKey(incident.key);
    setError(null);
    setActionMessage(null);
    try {
      await api.retryJob(incident.jobKey);
      setActionMessage(`Job ${incident.jobKey} scheduled for retry.`);
      await fetchIncidents(false);
    } catch (err) {
      setError(err instanceof Error ? err.message : "Failed to retry job");
    } finally {
      setActionKey(null);
    }
  };

  const resolve = async (incident: Incident) => {
    setActionKey(incident.key);
    setError(null);
    setActionMessage(null);
    try {
      const resolved = await api.resolveIncident(incident.key);
      setIncidents((current) => current.map((item) => item.key === incident.key ? resolved : item));
      setActionMessage(`Incident ${incident.key} resolved.`);
    } catch (err) {
      setError(err instanceof Error ? err.message : "Failed to resolve incident");
    } finally {
      setActionKey(null);
    }
  };

  if (loading) {
    return <div role="status" aria-live="polite" className="p-4">Loading incidents...</div>;
  }

  return (
    <div className="space-y-4 p-4">
      <div className="flex items-center justify-between gap-3">
        <div>
          <h1 className="text-2xl font-semibold tracking-tight">Incidents</h1>
          <p className="text-sm text-muted-foreground">
            Engine incidents including SLA due-date breaches (`SLA_DUE_DATE_BREACHED`).
          </p>
        </div>
        <Button
          variant="outline"
          size="sm"
          disabled={refreshing}
          onClick={() => void fetchIncidents(false)}
        >
          <RefreshCw className={`mr-2 h-4 w-4 ${refreshing ? "animate-spin" : ""}`} />
          Refresh
        </Button>
      </div>

      {error && (
        <Card role="alert">
          <CardContent className="flex items-center gap-2 py-4 text-sm text-destructive">
            <AlertTriangle className="h-4 w-4" />
            {error}
          </CardContent>
        </Card>
      )}
      {actionMessage && <p role="status" aria-live="polite" className="text-sm text-muted-foreground">{actionMessage}</p>}

      <Card>
        <CardHeader>
          <CardTitle>Open and recent incidents</CardTitle>
          <CardDescription>{incidents.length} incident(s)</CardDescription>
        </CardHeader>
        <CardContent>
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>Type</TableHead>
                <TableHead>State</TableHead>
                <TableHead>Message</TableHead>
                <TableHead>Instance</TableHead>
                <TableHead>Job</TableHead>
                <TableHead>Created</TableHead>
                <TableHead>Actions</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {incidents.length === 0 ? (
                <TableRow>
                  <TableCell colSpan={7} className="text-center text-muted-foreground">
                    No incidents require attention.
                  </TableCell>
                </TableRow>
              ) : (
                incidents.map((inc) => (
                  <TableRow key={inc.key || inc.id}>
                    <TableCell className="font-mono text-xs">{inc.errorType}</TableCell>
                    <TableCell>
                      <Badge variant={inc.state === "CREATED" ? "destructive" : "secondary"}>
                        {inc.state}
                      </Badge>
                    </TableCell>
                    <TableCell className="max-w-md truncate text-sm">{inc.errorMessage}</TableCell>
                    <TableCell>
                      {inc.processInstanceKey ? (
                        <Link
                          className="font-mono text-xs text-primary underline-offset-2 hover:underline"
                          to={`/instances/${inc.processInstanceKey}`}
                        >
                          {inc.processInstanceKey}
                        </Link>
                      ) : (
                        "—"
                      )}
                    </TableCell>
                    <TableCell className="font-mono text-xs">{inc.jobKey || "—"}</TableCell>
                    <TableCell className="text-xs">{formatDate(inc.createdAt)}</TableCell>
                    <TableCell>
                      <div className="flex gap-2">
                        {inc.jobKey && inc.state !== "RESOLVED" ? (
                          <Button
                            size="sm"
                            variant="outline"
                            disabled={actionKey === inc.key}
                            aria-label={`Retry job ${inc.jobKey} for incident ${inc.key}`}
                            onClick={() => void retry(inc)}
                          >
                            Retry job
                          </Button>
                        ) : null}
                        {inc.state !== "RESOLVED" ? (
                          <Button
                            size="sm"
                            disabled={actionKey === inc.key}
                            aria-label={`Resolve incident ${inc.key}`}
                            onClick={() => void resolve(inc)}
                          >
                            Resolve
                          </Button>
                        ) : null}
                      </div>
                    </TableCell>
                  </TableRow>
                ))
              )}
            </TableBody>
          </Table>
        </CardContent>
      </Card>
    </div>
  );
}
