import { useCallback, useEffect, useState } from "react";
import { api, type DecisionDefinition } from "@/lib/api";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Textarea } from "@/components/ui/textarea";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import { Modal } from "@/components/ui/modal";
import { Plus, RefreshCw, Upload } from "lucide-react";

const SAMPLE = `{
  "id": "invoice_decision",
  "hitPolicy": "FIRST",
  "rules": [
    {"when": {"amount": {"op": "gt", "value": 1000}}, "then": {"result": "manager"}},
    {"when": {}, "then": {"result": "auto"}}
  ]
}`;

export default function Decisions() {
  const [decisions, setDecisions] = useState<DecisionDefinition[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [open, setOpen] = useState(false);
  const [decisionId, setDecisionId] = useState("invoice_decision");
  const [name, setName] = useState("Invoice routing");
  const [resource, setResource] = useState(SAMPLE);
  const [saving, setSaving] = useState(false);
  const [evalOpen, setEvalOpen] = useState(false);
  const [evalId, setEvalId] = useState("");
  const [evalInputs, setEvalInputs] = useState('{"amount": 1500}');
  const [evalResult, setEvalResult] = useState<string | null>(null);
  const [actionError, setActionError] = useState<string | null>(null);

  const load = useCallback(async () => {
    setLoading(true);
    setError(null);
    try {
      setDecisions(await api.listDecisions());
    } catch (err) {
      setError(err instanceof Error ? err.message : "Failed to load decisions");
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    void load();
  }, [load]);

  const deploy = async () => {
    setSaving(true);
    setActionError(null);
    try {
      JSON.parse(resource);
      await api.deployDecision({
        decision_id: decisionId.trim(),
        name: name.trim() || decisionId.trim(),
        resource,
      });
      setOpen(false);
      await load();
    } catch (err) {
      setActionError(err instanceof Error ? err.message : "Deploy failed");
    } finally {
      setSaving(false);
    }
  };

  const onFile = async (file: File | null) => {
    if (!file) return;
    const text = await file.text();
    setResource(text);
    try {
      const parsed = JSON.parse(text) as { id?: string };
      if (parsed.id) {
        setDecisionId(parsed.id);
        if (!name) setName(parsed.id);
      }
    } catch {
      // keep raw text; deploy will validate
    }
  };

  const evaluate = async () => {
    setActionError(null);
    try {
      const inputs = JSON.parse(evalInputs) as Record<string, unknown>;
      const outputs = await api.evaluateDecision(evalId, inputs);
      setEvalResult(JSON.stringify(outputs, null, 2));
    } catch (err) {
      setActionError(err instanceof Error ? err.message : "Evaluate failed");
    }
  };

  return (
    <div className="space-y-4">
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-2xl font-bold">Decisions</h1>
          <p className="text-sm text-muted-foreground">
            Upload JSON decision tables referenced by <code>artificialflow:decisionRef</code>.
          </p>
        </div>
        <div className="flex gap-2">
          <Button variant="outline" size="sm" onClick={() => void load()} disabled={loading}>
            <RefreshCw className={`mr-2 h-4 w-4 ${loading ? "animate-spin" : ""}`} />
            Refresh
          </Button>
          <Button onClick={() => setOpen(true)}>
            <Plus className="mr-2 h-4 w-4" />
            Upload decision
          </Button>
        </div>
      </div>

      {error && (
        <div role="alert" className="rounded-md border border-destructive/40 bg-destructive/10 p-3 text-sm text-destructive">
          {error}
        </div>
      )}

      <div className="rounded-md border">
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>Decision ID</TableHead>
              <TableHead>Name</TableHead>
              <TableHead>Version</TableHead>
              <TableHead>Updated</TableHead>
              <TableHead>Actions</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {decisions.length === 0 ? (
              <TableRow>
                <TableCell colSpan={5} className="text-center text-muted-foreground">
                  {loading ? "Loading…" : "No decisions deployed yet."}
                </TableCell>
              </TableRow>
            ) : (
              decisions.map((d) => (
                <TableRow key={d.decision_id}>
                  <TableCell className="font-mono text-sm">{d.decision_id}</TableCell>
                  <TableCell>{d.name}</TableCell>
                  <TableCell>{d.version}</TableCell>
                  <TableCell className="text-sm text-muted-foreground">
                    {d.updated_at ? new Date(d.updated_at).toLocaleString() : "—"}
                  </TableCell>
                  <TableCell>
                    <Button
                      size="sm"
                      variant="outline"
                      onClick={() => {
                        setEvalId(d.decision_id);
                        setEvalResult(null);
                        setEvalOpen(true);
                      }}
                    >
                      Evaluate
                    </Button>
                  </TableCell>
                </TableRow>
              ))
            )}
          </TableBody>
        </Table>
      </div>

      <Modal isOpen={open} onClose={() => setOpen(false)} title="Upload decision table">
        <div className="space-y-4">
          {actionError && <p role="alert" className="text-sm text-destructive">{actionError}</p>}
          <div className="space-y-2">
            <Label htmlFor="decision-id">Decision ID</Label>
            <Input id="decision-id" value={decisionId} onChange={(e) => setDecisionId(e.target.value)} />
          </div>
          <div className="space-y-2">
            <Label htmlFor="decision-name">Name</Label>
            <Input id="decision-name" value={name} onChange={(e) => setName(e.target.value)} />
          </div>
          <div className="space-y-2">
            <Label htmlFor="decision-file">Decision table file</Label>
            <Input
              id="decision-file"
              type="file"
              accept="application/json,application/xml,text/xml,.json,.dmn,.xml"
              onChange={(e) => void onFile(e.target.files?.[0] || null)}
            />
          </div>
          <div className="space-y-2">
            <Label htmlFor="decision-resource">Decision JSON or DMN XML</Label>
            <Textarea
              id="decision-resource"
              className="font-mono h-56"
              value={resource}
              onChange={(e) => setResource(e.target.value)}
            />
          </div>
          <div className="flex justify-end gap-2">
            <Button variant="outline" onClick={() => setOpen(false)}>
              Cancel
            </Button>
            <Button onClick={() => void deploy()} disabled={saving || !decisionId.trim()}>
              <Upload className="mr-2 h-4 w-4" />
              {saving ? "Deploying…" : "Deploy"}
            </Button>
          </div>
        </div>
      </Modal>

      <Modal isOpen={evalOpen} onClose={() => setEvalOpen(false)} title={`Evaluate ${evalId}`}>
        <div className="space-y-4">
          {actionError && <p role="alert" className="text-sm text-destructive">{actionError}</p>}
          <div className="space-y-2">
            <Label htmlFor="decision-inputs">Inputs JSON</Label>
            <Textarea id="decision-inputs" className="font-mono h-32" value={evalInputs} onChange={(e) => setEvalInputs(e.target.value)} />
          </div>
          <Button onClick={() => void evaluate()}>Run</Button>
          {evalResult && (
            <Card>
              <CardHeader className="py-3">
                <CardTitle className="text-sm">Outputs</CardTitle>
              </CardHeader>
              <CardContent>
                <pre className="text-xs overflow-auto">{evalResult}</pre>
              </CardContent>
            </Card>
          )}
        </div>
      </Modal>
    </div>
  );
}
