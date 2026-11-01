{{- define "artificialflow.name" -}}
{{- if .Values.compatibility.legacyResourceNames.enabled -}}
{{- required "compatibility.legacyResourceNames.selectorNameOverride is required when legacy resource names are enabled" .Values.compatibility.legacyResourceNames.selectorNameOverride | trunc 63 | trimSuffix "-" -}}
{{- else -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" -}}
{{- end -}}
{{- end -}}

{{- define "artificialflow.fullname" -}}
{{- if .Values.compatibility.legacyResourceNames.enabled -}}
{{- required "compatibility.legacyResourceNames.fullnameOverride is required when legacy resource names are enabled" .Values.compatibility.legacyResourceNames.fullnameOverride | trunc 63 | trimSuffix "-" -}}
{{- else if .Values.fullnameOverride -}}
{{- .Values.fullnameOverride | trunc 63 | trimSuffix "-" -}}
{{- else -}}
{{- $name := default .Chart.Name .Values.nameOverride -}}
{{- if contains $name .Release.Name -}}
{{- .Release.Name | trunc 63 | trimSuffix "-" -}}
{{- else -}}
{{- printf "%s-%s" .Release.Name $name | trunc 63 | trimSuffix "-" -}}
{{- end -}}
{{- end -}}
{{- end -}}

{{- define "artificialflow.image" -}}
{{- if .digest -}}
{{- if not (regexMatch "^sha256:[0-9a-f]{64}$" .digest) -}}
{{- fail "image digest must be sha256:<64 lowercase hex>" -}}
{{- end -}}
{{- printf "%s@%s" .repository .digest -}}
{{- else -}}
{{- printf "%s:%s" .repository (toString .tag) -}}
{{- end -}}
{{- end -}}

{{- define "artificialflow.selectorLabels" -}}
app.kubernetes.io/name: {{ include "artificialflow.name" . }}
{{- if .Values.compatibility.legacyResourceNames.enabled }}
app.kubernetes.io/instance: {{ required "compatibility.legacyResourceNames.selectorInstanceOverride is required when legacy resource names are enabled" .Values.compatibility.legacyResourceNames.selectorInstanceOverride }}
{{- else }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end }}
{{- end -}}

{{- define "artificialflow.labels" -}}
helm.sh/chart: {{ printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" }}
{{ include "artificialflow.selectorLabels" . }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
{{- end -}}

{{- define "artificialflow.serviceAccountName" -}}
{{- if .Values.serviceAccount.create -}}
{{- default (include "artificialflow.fullname" .) .Values.serviceAccount.name -}}
{{- else -}}
{{- default "default" .Values.serviceAccount.name -}}
{{- end -}}
{{- end -}}

{{- define "artificialflow.envConfigName" -}}
{{- printf "%s-env" (include "artificialflow.fullname" .) -}}
{{- end -}}

{{- define "artificialflow.envSecretName" -}}
{{- .Values.postgresql.existingSecret | default (printf "%s-env" (include "artificialflow.fullname" .)) -}}
{{- end -}}

{{- define "artificialflow.postgresHost" -}}
{{- if .Values.postgresql.bundled.enabled -}}
{{- printf "%s-postgresql" (include "artificialflow.fullname" .) -}}
{{- else -}}
{{- .Values.postgresql.host -}}
{{- end -}}
{{- end -}}

{{- define "artificialflow.postgresDsn" -}}
{{- if .Values.postgresql.dsn -}}
{{- .Values.postgresql.dsn -}}
{{- else -}}
{{- $sslMode := ternary "disable" .Values.postgresql.sslMode .Values.postgresql.bundled.enabled -}}
{{- printf "host=%s user=%s password=%s dbname=%s port=%v sslmode=%s TimeZone=UTC" (include "artificialflow.postgresHost" .) .Values.postgresql.user .Values.postgresql.password .Values.postgresql.database .Values.postgresql.port $sslMode -}}
{{- end -}}
{{- end -}}

{{- define "artificialflow.searchAddress" -}}
{{- ternary (printf "http://%s-elasticsearch:9200" (include "artificialflow.fullname" .)) .Values.search.address .Values.search.bundled.enabled -}}
{{- end -}}

{{- define "artificialflow.kafkaBrokers" -}}
{{- ternary (printf "%s-kafka:9092" (include "artificialflow.fullname" .)) .Values.kafka.brokers .Values.kafka.bundled.enabled -}}
{{- end -}}

{{- define "artificialflow.connectUrl" -}}
{{- ternary (printf "http://%s-connect:8083" (include "artificialflow.fullname" .)) .Values.connect.url .Values.connect.bundled.enabled -}}
{{- end -}}

{{- define "artificialflow.validateValues" -}}
{{- $v := .Values -}}
{{- if hasKey $v "migration" -}}
{{- fail "migration.* was removed: command and runtime apply schema migrations at startup; tune startupProbe for long migrations" -}}
{{- end -}}
{{- if not (kindIs "bool" $v.iam.mode) -}}
{{- fail (printf "iam.mode must be a boolean: allowed values are true and false (got %v)" $v.iam.mode) -}}
{{- end -}}
{{- if $v.iam.mode -}}
{{- range $key := list "issuerInternalUrl" "issuerPublicUrl" "clientId" "claimRolesPath" -}}
{{- if not (index $v.iam.auth $key) -}}
{{- fail (printf "iam.mode=true requires iam.auth.%s" $key) -}}
{{- end -}}
{{- end -}}
{{- if not $v.iam.frontend.oidcClientId -}}
{{- fail "iam.mode=true requires iam.frontend.oidcClientId" -}}
{{- end -}}
{{- else if or (not $v.iam.consoleAdmin.username) (not $v.iam.consoleAdmin.password) -}}
{{- fail "iam.mode=false requires iam.consoleAdmin.username and iam.consoleAdmin.password" -}}
{{- end -}}
{{- if and (not $v.postgresql.bundled.enabled) (not $v.postgresql.host) (not $v.postgresql.dsn) -}}
{{- fail "postgresql.host or postgresql.dsn is required when postgresql.bundled.enabled is false" -}}
{{- end -}}
{{- if and (not $v.search.bundled.enabled) (not $v.search.address) -}}
{{- fail "search.address is required when search.bundled.enabled is false" -}}
{{- end -}}
{{- if and (not $v.kafka.bundled.enabled) (not $v.kafka.brokers) -}}
{{- fail "kafka.brokers is required when kafka.bundled.enabled is false" -}}
{{- end -}}
{{- if and (not $v.connect.bundled.enabled) (not $v.connect.url) -}}
{{- fail "connect.url is required when connect.bundled.enabled is false" -}}
{{- end -}}
{{- if and (eq $v.iam.auth.tokenMode "introspection") (not $v.postgresql.existingSecret) (not $v.iam.auth.introspectionClientSecret) -}}
{{- fail "iam.auth.tokenMode=introspection requires iam.auth.introspectionClientSecret or postgresql.existingSecret containing AUTH_INTROSPECTION_CLIENT_SECRET" -}}
{{- end -}}
{{- if and $v.monitoring.enabled $v.monitoring.prometheusRule.enabled (not $v.monitoring.prometheusRule.runbookBaseUrl) -}}
{{- fail "monitoring.prometheusRule.runbookBaseUrl is required when Prometheus rules are enabled" -}}
{{- end -}}
{{- range $key := list "PG_DSN" "POSTGRES_PASSWORD" "AUTH_INTROSPECTION_CLIENT_SECRET" "CONNECTOR_DATABASE_PASSWORD" "CONSOLE_ADMIN_PASSWORD" -}}
{{- if hasKey $v.env $key -}}
{{- fail (printf "env.%s would be stored in a ConfigMap; provide it through postgresql.existingSecret instead" $key) -}}
{{- end -}}
{{- end -}}
{{- if has "*" (splitList "," $v.cors.allowedOrigins) -}}
{{- fail "cors.allowedOrigins must be an exact production allowlist; wildcard origins are forbidden" -}}
{{- end -}}
{{- if and $v.gateway.enabled $v.gateway.hsts.enabled (not (and $v.ingress.enabled $v.ingress.tls.enabled)) -}}
{{- fail "gateway.hsts.enabled requires ingress.enabled and ingress.tls.enabled; disable HSTS only when TLS is assured elsewhere and injected by that trusted edge" -}}
{{- end -}}
{{- end -}}

{{/*
Service plus Deployment/StatefulSet for one component described in
artificialflow.components. Spec keys: kind, replicas, image, container, ports,
servicePorts, serviceType, app (shared env + pod annotations), noSharedEnv, security
("app" or uid number), args, env, extraEnv, probePort, liveDelay, readyDelay,
readinessProbe, volumeMounts, volumes, persistence, podManagementPolicy.
*/}}
{{- define "artificialflow.workload" -}}
{{- $root := .root -}}
{{- $s := .spec -}}
{{- $name := printf "%s-%s" (include "artificialflow.fullname" $root) .component -}}
{{- $kind := $s.kind | default "Deployment" -}}
{{- $component := .component -}}
apiVersion: v1
kind: Service
metadata:
  name: {{ $name }}
  labels:
    {{- include "artificialflow.labels" $root | nindent 4 }}
    app.kubernetes.io/component: {{ $component }}
spec:
  type: {{ $s.serviceType | default "ClusterIP" }}
  {{- if $s.headless }}
  clusterIP: None
  publishNotReadyAddresses: true
  {{- end }}
  ports:
    {{- range $port, $number := ($s.servicePorts | default $s.ports) }}
    - {name: {{ $port }}, port: {{ $number }}, targetPort: {{ $port }}}
    {{- end }}
  selector:
    {{- include "artificialflow.selectorLabels" $root | nindent 4 }}
    app.kubernetes.io/component: {{ $component }}
---
apiVersion: apps/v1
kind: {{ $kind }}
metadata:
  name: {{ $name }}
  labels:
    {{- include "artificialflow.labels" $root | nindent 4 }}
    app.kubernetes.io/component: {{ $component }}
spec:
  {{- if eq $kind "StatefulSet" }}
  serviceName: {{ $name }}
  {{- with $s.podManagementPolicy }}
  podManagementPolicy: {{ . }}
  {{- end }}
  {{- if $s.persistence }}
  persistentVolumeClaimRetentionPolicy: {whenDeleted: Retain, whenScaled: Retain}
  {{- end }}
  {{- end }}
  replicas: {{ hasKey $s "replicas" | ternary $s.replicas 1 }}
  {{- if and $s.app (eq $kind "Deployment") }}
  # Old pods keep serving while a new pod migrates or crash-loops on a bad migration.
  strategy: {type: RollingUpdate, rollingUpdate: {maxUnavailable: 0, maxSurge: 1}}
  # Longer than the startup probe window, so a slow image pull plus migration
  # is not marked a failed rollout while /livez is still allowed to fail.
  progressDeadlineSeconds: {{ add (mul (int $root.Values.startupProbe.periodSeconds) (int $root.Values.startupProbe.failureThreshold)) 600 }}
  {{- end }}
  selector:
    matchLabels:
      {{- include "artificialflow.selectorLabels" $root | nindent 6 }}
      app.kubernetes.io/component: {{ $component }}
  template:
    metadata:
      labels:
        {{- include "artificialflow.selectorLabels" $root | nindent 8 }}
        app.kubernetes.io/component: {{ $component }}
      {{- if $s.app }}
      {{- with $root.Values.global.podAnnotations }}
      annotations:
        {{- toYaml . | nindent 8 }}
      {{- end }}
      {{- end }}
    spec:
      serviceAccountName: {{ include "artificialflow.serviceAccountName" $root }}
      automountServiceAccountToken: false
      {{- if eq (toString $s.security) "app" }}
      securityContext:
        {{- toYaml $root.Values.global.podSecurityContext | nindent 8 }}
      {{- else if $s.security }}
      securityContext: {runAsNonRoot: true, runAsUser: {{ $s.security }}, runAsGroup: {{ $s.security }}, fsGroup: {{ $s.security }}, seccompProfile: {type: RuntimeDefault}}
      {{- end }}
      {{- with $root.Values.global.imagePullSecrets }}
      imagePullSecrets:
        {{- toYaml . | nindent 8 }}
      {{- end }}
      containers:
        - name: {{ $s.container | default $component }}
          image: {{ include "artificialflow.image" $s.image | quote }}
          imagePullPolicy: {{ $s.image.pullPolicy | default "IfNotPresent" }}
          {{- if eq (toString $s.security) "app" }}
          securityContext:
            {{- toYaml $root.Values.global.containerSecurityContext | nindent 12 }}
          {{- else if $s.security }}
          securityContext: {runAsUser: {{ $s.security }}, runAsNonRoot: true, readOnlyRootFilesystem: false, allowPrivilegeEscalation: false, capabilities: {drop: [ALL]}}
          {{- end }}
          {{- with $s.args }}
          args: {{ toJson . }}
          {{- end }}
          ports:
            {{- range $port, $number := $s.ports }}
            - {name: {{ $port }}, containerPort: {{ $number }}}
            {{- end }}
          {{- if and $s.app (not $s.noSharedEnv) }}
          envFrom:
            - configMapRef:
                name: {{ include "artificialflow.envConfigName" $root }}
          {{- end }}
          {{- if or $s.env $s.extraEnv }}
          env:
            {{- range $s.env }}
            - {{ toJson . }}
            {{- end }}
            {{- range $key, $value := $s.extraEnv }}
            - {name: {{ $key }}, value: {{ toString $value | quote }}}
            {{- end }}
          {{- end }}
          {{- if or $s.persistence $s.volumeMounts }}
          volumeMounts:
            {{- with $s.persistence }}
            - {name: data, mountPath: {{ .mountPath }}}
            {{- end }}
            {{- with $s.volumeMounts }}
            {{- toYaml . | nindent 12 }}
            {{- end }}
          {{- end }}
          {{- if $s.probePort }}
          startupProbe:
            httpGet: {path: /livez, port: {{ $s.probePort }}}
            periodSeconds: {{ $root.Values.startupProbe.periodSeconds }}
            failureThreshold: {{ $root.Values.startupProbe.failureThreshold }}
          livenessProbe:
            httpGet: {path: /livez, port: {{ $s.probePort }}}
            initialDelaySeconds: {{ $s.liveDelay | default 20 }}
            periodSeconds: 20
          readinessProbe:
            httpGet: {path: /readyz, port: {{ $s.probePort }}}
            initialDelaySeconds: {{ $s.readyDelay | default 5 }}
            periodSeconds: 10
          {{- else if $s.readinessProbe }}
          readinessProbe:
            {{- toYaml $s.readinessProbe | nindent 12 }}
          {{- end }}
          resources:
            {{- toYaml $s.resources | nindent 12 }}
      {{- with $s.volumes }}
      volumes:
        {{- toYaml . | nindent 8 }}
      {{- end }}
      {{- with $root.Values.global.nodeSelector }}
      nodeSelector:
        {{- toYaml . | nindent 8 }}
      {{- end }}
      {{- with $root.Values.global.affinity }}
      affinity:
        {{- toYaml . | nindent 8 }}
      {{- end }}
      {{- with $root.Values.global.tolerations }}
      tolerations:
        {{- toYaml . | nindent 8 }}
      {{- end }}
  {{- with $s.persistence }}
  volumeClaimTemplates:
    - metadata:
        name: data
        annotations: {helm.sh/resource-policy: keep}
      spec:
        accessModes: [ReadWriteOnce]
        resources: {requests: {storage: {{ .size }}}}
        {{- with .storageClassName }}
        storageClassName: {{ . | quote }}
        {{- end }}
  {{- end }}
{{- end -}}
