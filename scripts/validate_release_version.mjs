#!/usr/bin/env node
import fs from "node:fs";
import path from "node:path";
import process from "node:process";
import { fileURLToPath } from "node:url";

import { validateTransitionCompatibility } from "./validate_transition_compatibility.mjs";

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..");
const failures = [];

function read(relativePath) {
  return fs.readFileSync(path.join(root, relativePath), "utf8");
}

function readJSON(relativePath) {
  return JSON.parse(read(relativePath));
}

function equal(label, actual, expected) {
  if (actual !== expected) {
    failures.push(`${label} must be ${JSON.stringify(expected)}; got ${JSON.stringify(actual)}`);
  }
}

function capture(relativePath, pattern, label) {
  const match = read(relativePath).match(pattern);
  if (!match) {
    failures.push(`${label} is missing from ${relativePath}`);
    return "";
  }
  return match[1];
}

function releaseTagArgument() {
  const index = process.argv.indexOf("--tag");
  if (index >= 0) return process.argv[index + 1] || "";
  const inline = process.argv.find((argument) => argument.startsWith("--tag="));
  if (inline) return inline.slice("--tag=".length);
  if (process.env.RELEASE_TAG) return process.env.RELEASE_TAG;
  if (process.env.GITHUB_REF_TYPE === "tag") return process.env.GITHUB_REF_NAME || "";
  return "";
}

function validateDocumentedVersions(relativePath, linePattern, expected, allowPrereleaseExample = false) {
  const matchingLines = read(relativePath)
    .split(/\r?\n/)
    .filter((line) => linePattern.test(line));

  if (matchingLines.length === 0) {
    failures.push(`no documented release defaults found in ${relativePath}`);
    return;
  }

  for (const line of matchingLines) {
    const versions = [...line.matchAll(/\bv?(\d+\.\d+\.\d+(?:-[0-9A-Za-z.-]+)?)/g)];
    if (versions.length === 0) {
      failures.push(`could not parse release version from ${relativePath}: ${line.trim()}`);
      continue;
    }
    for (const match of versions) {
      const actual = allowPrereleaseExample ? match[1].split("-", 1)[0] : match[1];
      const wanted = allowPrereleaseExample ? expected.split("-", 1)[0] : expected;
      equal(`${relativePath} documented release default`, actual, wanted);
    }
  }
}

const canonicalPackage = readJSON("clients/nodejs-sdk/package.json");
const version = canonicalPackage.version;
if (!/^\d+\.\d+\.\d+(?:-[0-9A-Za-z.-]+)?$/.test(version || "")) {
  failures.push(`clients/nodejs-sdk/package.json version is not SemVer: ${JSON.stringify(version)}`);
}
failures.push(...validateTransitionCompatibility(root, version));

const lock = readJSON("clients/nodejs-sdk/package-lock.json");
equal("clients/nodejs-sdk/package-lock.json version", lock.version, version);
equal("clients/nodejs-sdk/package-lock.json root package version", lock.packages?.[""]?.version, version);

const frontendPackage = readJSON("frontend/package.json");
const frontendLock = readJSON("frontend/package-lock.json");
equal("frontend/package.json version", frontendPackage.version, version);
equal("frontend/package-lock.json version", frontendLock.version, version);
equal("frontend/package-lock.json root package version", frontendLock.packages?.[""]?.version, version);
equal(
  "docs/openapi.yaml info.version",
  capture("docs/openapi.yaml", /^\s{2}version:\s*"?([^"\s]+)"?\s*$/m, "OpenAPI version"),
  version,
);

const chartVersion = capture("charts/artificialflow/Chart.yaml", /^version:\s*"?([^"\s]+)"?\s*$/m, "Helm chart version");
const appVersion = capture("charts/artificialflow/Chart.yaml", /^appVersion:\s*"?([^"\s]+)"?\s*$/m, "Helm appVersion");
equal("charts/artificialflow/Chart.yaml version", chartVersion, version);
equal("charts/artificialflow/Chart.yaml appVersion", appVersion, version);

const chartValues = read("charts/artificialflow/values.yaml").split(/\r?\n/);
const applicationImages = new Set(["command", "query", "runtime", "syncWorker", "frontend"]);
const imageTags = new Map();
let inImages = false;
let currentImage = "";
for (const line of chartValues) {
  if (line === "images:") {
    inImages = true;
    continue;
  }
  if (inImages && /^\S/.test(line)) break;
  const imageMatch = line.match(/^  ([A-Za-z][A-Za-z0-9]*):\s*$/);
  if (imageMatch) {
    currentImage = imageMatch[1];
    continue;
  }
  const tagMatch = line.match(/^    tag:\s*"?([^"\s]+)"?\s*$/);
  if (tagMatch && applicationImages.has(currentImage)) imageTags.set(currentImage, tagMatch[1]);
}
for (const image of applicationImages) {
  equal(`charts/artificialflow/values.yaml images.${image}.tag`, imageTags.get(image), version);
}

const compose = read("docker-compose.yml");
const composeDefaults = [
  ...compose.matchAll(/ARTIFICIALFLOW_IMAGE_TAG:-([^}]+)/g),
].map((match) => match[1]);
if (composeDefaults.length < applicationImages.size) {
  failures.push(`docker-compose.yml must version every application image; got ${composeDefaults.length} defaults`);
}
composeDefaults.forEach((value, index) => {
  equal(`docker-compose.yml application image default ${index + 1}`, value, version);
});
equal(
  "clients/dotnet-sdk Version",
  capture("clients/dotnet-sdk/src/ArtificialFlow.Sdk/ArtificialFlow.Sdk.csproj", /<Version>([^<]+)<\/Version>/, ".NET SDK version"),
  version,
);
equal(
  "clients/java-sdk version",
  capture("clients/java-sdk/pom.xml", /^\s{2}<version>([^<]+)<\/version>/m, "Java SDK version"),
  version,
);

equal(
  "scripts/release_dry_run.sh default",
  capture(
    "scripts/release_dry_run.sh",
    /^VERSION="\$\{VERSION:-([^}]+)\}"$/m,
    "release dry-run default",
  ),
  `${version}-dry-run`,
);
equal(
  ".github/workflows/release-dry-run.yml VERSION",
  capture(
    ".github/workflows/release-dry-run.yml",
    /^\s+VERSION:\s*"?([^"\s]+)"?\s*$/m,
    "release dry-run workflow version",
  ),
  `${version}-dry-run`,
);

validateDocumentedVersions(
  "README.md",
  /pin release|--branch v|ARTIFICIALFLOW_IMAGE_TAG=/,
  version,
);
validateDocumentedVersions(
  "docs/deployment.md",
  /ARTIFICIALFLOW_IMAGE_TAG=/,
  version,
);
validateDocumentedVersions(
  "docs/DOCKER_IMAGES.md",
  /v\d+\.\d+\.\d+/,
  version,
);
validateDocumentedVersions(
  "docs/RELEASE_CHECKLIST.md",
  /validate_release_version.*--tag|ARTIFICIALFLOW_IMAGE_TAG=|git tag|git push/,
  version,
  true,
);

const changelogHeading = `## [${version}]`;
if (!read("CHANGELOG.md").split(/\r?\n/).some((line) => line.startsWith(changelogHeading))) {
  failures.push(`CHANGELOG.md must contain a ${JSON.stringify(changelogHeading)} release heading`);
}

const dispatchTag = capture(
  ".github/workflows/release.yml",
  /^\s+default:\s*v(\d+\.\d+\.\d+(?:-[0-9A-Za-z.-]+)?)\s*$/m,
  "release orchestrator workflow dispatch tag",
);
equal(".github/workflows/release.yml dispatch tag", dispatchTag, version);

for (const reusableWorkflow of [
  ".github/workflows/release-images.yml",
  ".github/workflows/release-npm.yml",
]) {
  const reusableDispatchTag = capture(
    reusableWorkflow,
    /^\s+default:\s*v(\d+\.\d+\.\d+(?:-[0-9A-Za-z.-]+)?)\s*$/m,
    `${reusableWorkflow} direct dispatch tag`,
  );
  equal(`${reusableWorkflow} direct dispatch tag`, reusableDispatchTag, version);
}

const releaseTag = releaseTagArgument();
if (releaseTag) {
  equal("release tag", releaseTag, `v${version}`);
}

if (failures.length > 0) {
  console.error("Release version consistency validation failed:");
  failures.forEach((failure) => console.error(`- ${failure}`));
  process.exit(1);
}

console.log(`Release version consistency validation passed for ${version}${releaseTag ? ` (${releaseTag})` : ""}`);
