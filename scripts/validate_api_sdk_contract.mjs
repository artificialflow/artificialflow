#!/usr/bin/env node
import fs from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..");
const read = (file) => fs.readFileSync(path.join(root, file), "utf8");
const failures = [];

const openapi = read("docs/openapi.yaml");
const sdks = [
  {
    name: "nodejs",
    source: read("clients/nodejs-sdk/src/Client.ts"),
    exclusions: JSON.parse(read("clients/nodejs-sdk/openapi-exclusions.json")),
    methodPattern: /public async ([A-Za-z][A-Za-z0-9_]*)\s*\(/g,
  },
  {
    name: "dotnet",
    source: read("clients/dotnet-sdk/src/ArtificialFlow.Sdk/ArtificialFlowClient.cs"),
    exclusions: JSON.parse(read("clients/dotnet-sdk/openapi-exclusions.json")),
    methodPattern: /public (?:async )?Task[\w.<>,\s\[\]]*\s+([A-Za-z][A-Za-z0-9_]*)Async\s*\(/g,
  },
  {
    name: "java",
    source: read("clients/java-sdk/src/main/java/io/artificialflow/sdk/ArtificialFlowClient.java"),
    exclusions: JSON.parse(read("clients/java-sdk/openapi-exclusions.json")),
    methodPattern: /public (?:[\w.<>,\[\] ?]+ )([A-Za-z][A-Za-z0-9_]*)\s*\(/g,
  },
];
const operationIds = [...openapi.matchAll(/^\s+operationId:\s+([A-Za-z][A-Za-z0-9_]*)\s*$/gm)].map((match) => match[1]);

for (const sdk of sdks) {
  const methods = new Set([...sdk.source.matchAll(sdk.methodPattern)].map((match) => match[1]));
  for (const operationId of operationIds) {
    if (!methods.has(operationId) && typeof sdk.exclusions[operationId] !== "string") {
      failures.push(`OpenAPI operation ${operationId} has no same-named ${sdk.name} SDK method or documented exclusion`);
    }
  }
  for (const [operationId, reason] of Object.entries(sdk.exclusions)) {
    if (!operationIds.includes(operationId)) failures.push(`stale ${sdk.name} SDK exclusion ${operationId}`);
    if (typeof reason !== "string" || !reason.trim()) failures.push(`${sdk.name} SDK exclusion ${operationId} requires a reason`);
  }
}

if (!/\/incidents:[\s\S]*?required: \[incidents\][\s\S]*?items:\s*\n\s+\$ref: "#\/components\/schemas\/Incident"/.test(openapi)) {
  failures.push("OpenAPI incidents response must match the runtime { incidents: [...] } wrapper");
}
if (!openapi.includes("application/problem+json") || !openapi.includes("correlationId:")) {
  failures.push("OpenAPI must define the shared RFC 9457 correlation-aware error");
}
if (!read("gateway/nginx.conf").includes("location = /api/openapi.yaml")) {
  failures.push("gateway must serve the authoritative checked-in OpenAPI contract");
}

const connectorModule = read("frontend/src/lib/connector-descriptors.ts");
if (!connectorModule.includes('connectors/internal/common/descriptors.json')) {
  failures.push("frontend connector descriptors must consume the canonical JSON source");
}
const connectorGo = read("connectors/internal/common/descriptors.go");
if (!connectorGo.includes("//go:embed descriptors.json")) {
  failures.push("Go connector descriptors must consume the canonical JSON source");
}
JSON.parse(read("connectors/internal/common/descriptors.json"));

const releaseVersion = JSON.parse(read("clients/nodejs-sdk/package.json")).version;
const frontendVersion = JSON.parse(read("frontend/package.json")).version;
if (frontendVersion !== releaseVersion) {
  failures.push(`frontend package version ${frontendVersion} must match release version ${releaseVersion}`);
}

if (failures.length) {
  console.error("API/SDK contract validation failed:");
  failures.forEach((failure) => console.error(`- ${failure}`));
  process.exit(1);
}
console.log(`API/SDK contract validation passed (${operationIds.length} operations across ${sdks.length} SDKs)`);
