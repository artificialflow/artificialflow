import descriptorSource from "../../../connectors/internal/common/descriptors.json";

export type ConnectorFieldKind = "string" | "number" | "boolean" | "json";

export interface ConnectorField {
  key: string;
  label: string;
  required: boolean;
  description: string;
  kind: ConnectorFieldKind;
  default?: string;
}

export interface ConnectorDescriptor {
  jobType: string;
  name: string;
  description: string;
  fields: ConnectorField[];
}

/** Official connector job types and inputs from the canonical shared descriptor. */
export const CONNECTOR_DESCRIPTORS = descriptorSource as ConnectorDescriptor[];

export function getConnectorDescriptor(jobType: string): ConnectorDescriptor | undefined {
  return CONNECTOR_DESCRIPTORS.find((d) => d.jobType === jobType);
}

export function allConnectorInputKeys(): string[] {
  const seen = new Set<string>();
  const keys: string[] = [];
  for (const d of CONNECTOR_DESCRIPTORS) {
    for (const f of d.fields) {
      if (seen.has(f.key)) continue;
      seen.add(f.key);
      keys.push(f.key);
    }
  }
  return keys;
}

/** Read connector inputs from node data (connectorInputs) or extension properties. */
export function readConnectorInputs(
  data: Record<string, unknown> | null | undefined,
  jobType: string,
): Record<string, string> {
  const descriptor = getConnectorDescriptor(jobType);
  if (!descriptor) return {};

  const fromNode = (data?.connectorInputs as Record<string, unknown> | undefined) || {};
  const props = getExtensionPropertyMap(data);
  const out: Record<string, string> = {};
  for (const field of descriptor.fields) {
    const raw = fromNode[field.key] ?? props[field.key] ?? field.default ?? "";
    out[field.key] = raw == null ? "" : String(raw);
  }
  return out;
}

function getExtensionPropertyMap(data: Record<string, unknown> | null | undefined): Record<string, string> {
  const ext = data?.["bpmn:extensionElements"] as Record<string, unknown> | undefined;
  const properties = ext?.["artificialflow:properties"] as Record<string, unknown> | undefined;
  const property = properties?.["artificialflow:property"];
  const list = Array.isArray(property) ? property : property ? [property] : [];
  const out: Record<string, string> = {};
  for (const item of list) {
    if (!item || typeof item !== "object") continue;
    const rec = item as Record<string, unknown>;
    const name = String(rec["@_name"] ?? "").trim();
    if (!name) continue;
    out[name] = String(rec["@_value"] ?? "");
  }
  return out;
}

/**
 * Persist connector field values under node `connectorInputs` and as
 * `artificialflow:property` name/value pairs (so deploy/parser merges them onto step.Properties).
 * Start-instance variables with the same keys can still supply or override at runtime.
 */
export function applyConnectorInputs(
  data: Record<string, unknown>,
  jobType: string,
  inputs: Record<string, string>,
): Record<string, unknown> {
  const descriptor = getConnectorDescriptor(jobType);
  const keys = new Set(descriptor?.fields.map((f) => f.key) ?? Object.keys(inputs));
  const cleaned: Record<string, string> = {};
  for (const [k, v] of Object.entries(inputs)) {
    if (!keys.has(k)) continue;
    cleaned[k] = v;
  }

  const next: Record<string, unknown> = {
    ...data,
    connectorInputs: cleaned,
  };

  const ext = (next["bpmn:extensionElements"] as Record<string, unknown> | undefined) || {};
  const properties = (ext["artificialflow:properties"] as Record<string, unknown> | undefined) || {};
  const existing = properties["artificialflow:property"];
  const list = Array.isArray(existing) ? [...existing] : existing ? [existing] : [];

  const kept = list.filter((item) => {
    if (!item || typeof item !== "object") return true;
    const name = String((item as Record<string, unknown>)["@_name"] ?? "");
    return !keys.has(name);
  });

  for (const [name, value] of Object.entries(cleaned)) {
    if (!value.trim()) continue;
    kept.push({ "@_name": name, "@_value": value });
  }

  const nextExt = { ...ext };
  if (kept.length > 0) {
    nextExt["artificialflow:properties"] = { "artificialflow:property": kept };
    next["bpmn:extensionElements"] = nextExt;
  } else {
    delete nextExt["artificialflow:properties"];
    if (Object.keys(nextExt).length > 0) {
      next["bpmn:extensionElements"] = nextExt;
    } else {
      delete next["bpmn:extensionElements"];
    }
  }

  return next;
}
