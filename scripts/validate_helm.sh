#!/usr/bin/env bash
set -euo pipefail

CHART_DIR="${CHART_DIR:-charts/artificialflow}"

ruby -e 'require "yaml"; ARGV.each { |f| YAML.load_file(f); puts "ok #{f}" }' \
  "${CHART_DIR}/Chart.yaml" \
  "${CHART_DIR}/values.yaml" \
  "${CHART_DIR}/values-kind-smoke.yaml" \
  "${CHART_DIR}/examples/values-managed-deps.yaml"

ruby -e '
  require "yaml"
  values = YAML.load_file(ARGV[0])
  abort "values.yaml must set iam.mode: true" unless values.dig("iam", "mode") == true
  abort "values.yaml must enable bundled postgresql" unless values.dig("postgresql", "bundled", "enabled") == true
  abort "values.yaml must enable bundled search" unless values.dig("search", "bundled", "enabled") == true
  abort "values.yaml must enable bundled kafka" unless values.dig("kafka", "bundled", "enabled") == true
  abort "values.yaml must enable bundled connect" unless values.dig("connect", "bundled", "enabled") == true
  smoke = YAML.load_file(ARGV[1])
  abort "values-kind-smoke.yaml must set iam.mode: false" unless smoke.dig("iam", "mode") == false
  managed = YAML.load_file(ARGV[2])
  abort "values-managed-deps.yaml must disable bundled postgresql" unless managed.dig("postgresql", "bundled", "enabled") == false
  abort "values-managed-deps.yaml must set postgresql.host" if managed.dig("postgresql", "host").to_s.empty?
  puts "ok helm default and fixture values"
' "${CHART_DIR}/values.yaml" "${CHART_DIR}/values-kind-smoke.yaml" "${CHART_DIR}/examples/values-managed-deps.yaml"

if ! command -v helm >/dev/null 2>&1; then
  echo "helm not installed; skipped helm lint/template"
  exit 0
fi

helm lint "${CHART_DIR}"
helm template artificialflow "${CHART_DIR}" >/tmp/artificialflow-default.yaml
helm template artificialflow "${CHART_DIR}" -f "${CHART_DIR}/values-kind-smoke.yaml" >/tmp/artificialflow-kind.yaml
helm template artificialflow "${CHART_DIR}" -f "${CHART_DIR}/examples/values-managed-deps.yaml" >/tmp/artificialflow-managed.yaml
helm template artificialflow "${CHART_DIR}" --set monitoring.enabled=true >/tmp/artificialflow-monitoring.yaml

ruby -e '
  %w[/tmp/artificialflow-default.yaml /tmp/artificialflow-kind.yaml].each do |path|
    rendered = File.read(path)
    abort "#{path} is missing bundled postgresql StatefulSet" unless rendered.include?("kind: StatefulSet") && rendered.include?("artificialflow-postgresql")
    abort "#{path} is missing bundled kafka StatefulSet" unless rendered.include?("artificialflow-kafka")
    abort "#{path} is missing bundled elasticsearch StatefulSet" unless rendered.include?("artificialflow-elasticsearch")
    abort "#{path} is missing bundled connect Deployment" unless rendered.include?("artificialflow-connect")
    abort "#{path} must not render a schema migration Job; services own migrations" if rendered.include?("kind: Job") || rendered.include?("migration-env")
    abort "#{path} is missing startup probes for migrating services" unless rendered.include?("startupProbe:")
    abort "#{path} app Deployments must roll out with maxUnavailable 0" unless rendered.include?("maxUnavailable: 0")
    abort "#{path} app Deployments must outlive the startup probe window" unless rendered.include?("progressDeadlineSeconds: 1200")
  end
  default_render = File.read("/tmp/artificialflow-default.yaml")
  abort "iam.mode=true render must set IAM_DEPLOYMENT_MODE \"true\"" unless default_render.include?(%(IAM_DEPLOYMENT_MODE: "true"))
  abort "iam.mode=true render must not pass CONSOLE_ADMIN_*" if default_render.include?("CONSOLE_ADMIN_")
  abort "iam.mode=true gateway CSP must allow the OIDC origin" unless default_render.include?("connect-src \u0027self\u0027 https://login.example.com;")
  kind_render = File.read("/tmp/artificialflow-kind.yaml")
  abort "iam.mode=false render must set IAM_DEPLOYMENT_MODE \"false\"" unless kind_render.include?(%(IAM_DEPLOYMENT_MODE: "false"))
  abort "iam.mode=false render must set AUTH_PRODUCTION \"false\"" unless kind_render.include?(%(AUTH_PRODUCTION: "false"))
  abort "iam.mode=false render must pass CONSOLE_ADMIN_USERNAME to the command service" unless kind_render.include?(%("name":"CONSOLE_ADMIN_USERNAME"))
  abort "iam.mode=false render must pass CONSOLE_ADMIN_PASSWORD from the Secret" unless kind_render.include?(%("key":"CONSOLE_ADMIN_PASSWORD"))
  abort "iam.mode=false render must not configure an OIDC issuer" if kind_render.include?("AUTH_ISSUER_") || kind_render.include?("FRONTEND_AUTH_OIDC_")
  abort "iam.mode=false gateway CSP must only allow self" unless kind_render.include?("connect-src \u0027self\u0027;")
  managed = File.read("/tmp/artificialflow-managed.yaml")
  abort "managed-deps render must not include bundled postgresql" if managed.include?("name: artificialflow-postgresql")
  abort "managed-deps render must not include bundled kafka" if managed.include?("name: artificialflow-kafka")
  puts "ok helm bundled data-plane objects"
'

ruby -e '
  rendered = File.read(ARGV[0])
  abort "monitoring render is missing ServiceMonitor" unless rendered.scan("kind: ServiceMonitor").length == 4
  abort "monitoring render is missing PrometheusRule" unless rendered.include?("kind: PrometheusRule")
  abort "monitoring render is missing dashboard ConfigMap" unless rendered.include?("artificialflow-overview.json")
  abort "command probe does not use /readyz" unless rendered.include?("path: /readyz")
  abort "liveness probe does not use /livez" unless rendered.include?("path: /livez")
  ingress = rendered.split(/^---$/).select { |doc| doc.include?("kind: Ingress") }.join
  abort "metrics path must not be exposed by ingress" if ingress.include?("/metrics")
  puts "ok helm private monitoring and health resources"
' /tmp/artificialflow-monitoring.yaml

ruby -e '
  require "yaml"
  docs = File.read(ARGV[0]).split(/^---\s*$/).map do |raw|
    begin
      YAML.safe_load(raw, aliases: true)
    rescue Psych::SyntaxError
      nil
    end
  end.compact
  policies = docs.select { |doc| doc["kind"] == "NetworkPolicy" }
  abort "render is missing default-deny NetworkPolicy" unless policies.any? { |doc| doc.dig("spec", "podSelector") == {} && doc.dig("spec", "policyTypes") == ["Ingress", "Egress"] }
  workloads = docs.select { |doc| ["Deployment", "StatefulSet"].include?(doc["kind"]) }
  protected = workloads.select { |doc| %w[command query runtime sync-worker gateway].include?(doc.dig("metadata", "labels", "app.kubernetes.io/component")) }
  abort "expected protected workloads were not rendered" if protected.empty?
  protected.each do |doc|
    spec = doc.dig("spec", "template", "spec")
    abort "#{doc.dig("metadata", "name")} automounts a service account token" unless spec["automountServiceAccountToken"] == false
    sc = spec.dig("containers", 0, "securityContext") || {}
    abort "#{doc.dig("metadata", "name")} permits privilege escalation" unless sc["allowPrivilegeEscalation"] == false
    abort "#{doc.dig("metadata", "name")} does not drop all capabilities" unless sc.dig("capabilities", "drop")&.include?("ALL")
  end
  sensitive = %w[PG_DSN POSTGRES_PASSWORD AUTH_INTROSPECTION_CLIENT_SECRET CONSOLE_ADMIN_PASSWORD]
  docs.select { |doc| doc["kind"] == "ConfigMap" }.each do |doc|
    leaked = (doc["data"] || {}).keys & sensitive
    abort "ConfigMap contains sensitive keys: #{leaked.join(", ")}" unless leaked.empty?
  end
  puts "ok helm restricted workloads, network policy, and secret separation"
' /tmp/artificialflow-default.yaml

if helm template artificialflow "${CHART_DIR}" --set postgresql.bundled.enabled=false >/dev/null 2>&1; then
  echo "bundled postgresql off without host was accepted" >&2
  exit 1
fi

for legacy_mode in external disabled true false; do
  if helm template artificialflow "${CHART_DIR}" --set-string iam.mode="${legacy_mode}" >/dev/null 2>&1; then
    echo "non-boolean iam.mode=${legacy_mode} was accepted" >&2
    exit 1
  fi
done

if helm template artificialflow "${CHART_DIR}" --set env.PG_DSN=leak >/dev/null 2>&1; then
  echo "secret key in ConfigMap env was accepted" >&2
  exit 1
fi

if helm template artificialflow "${CHART_DIR}" --set-string cors.allowedOrigins='*' >/dev/null 2>&1; then
  echo "wildcard production CORS was accepted" >&2
  exit 1
fi

if helm template artificialflow "${CHART_DIR}" --set monitoring.enabled=true --set monitoring.prometheusRule.runbookBaseUrl= >/dev/null 2>&1; then
  echo "monitoring values validation accepted an empty runbook URL" >&2
  exit 1
fi

if helm template artificialflow "${CHART_DIR}" --set-string images.gateway.digest=sha256:not-a-digest >/dev/null 2>&1; then
  echo "invalid image digest was accepted" >&2
  exit 1
fi

echo "helm validation passed"
