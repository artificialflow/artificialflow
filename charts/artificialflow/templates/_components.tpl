{{/*
Every workload in the chart, rendered by artificialflow.workload (see workloads.yaml).
*/}}
{{- define "artificialflow.components" -}}
{{- $v := .Values -}}
{{- $secret := include "artificialflow.envSecretName" . | quote -}}
{{- $pgDsn := printf "{name: PG_DSN, valueFrom: {secretKeyRef: {name: %s, key: %s}}}" $secret ($v.postgresql.existingSecretKey | default "PG_DSN") -}}
{{- $introspection := printf "{name: AUTH_INTROSPECTION_CLIENT_SECRET, valueFrom: {secretKeyRef: {name: %s, key: AUTH_INTROSPECTION_CLIENT_SECRET, optional: true}}}" $secret -}}
command:
  enabled: true
  kind: StatefulSet
  podManagementPolicy: Parallel
  replicas: {{ $v.replicaCount.command }}
  image: {{ toJson $v.images.command }}
  container: workflow-command
  app: true
  security: app
  ports: {http: 8080, metrics: 9090}
  probePort: http
  env:
    - {name: POD_NAME, valueFrom: {fieldRef: {fieldPath: metadata.name}}}
    - {name: RUNTIME_ENABLED, value: "false"}
    - {name: OUTBOX_RELAY_ENABLED, value: "false"}
    - {{ $pgDsn }}
    - {{ $introspection }}
    {{- if not $v.iam.mode }}
    - {name: CONSOLE_ADMIN_USERNAME, value: {{ $v.iam.consoleAdmin.username | quote }}}
    - {name: CONSOLE_ADMIN_PASSWORD, valueFrom: {secretKeyRef: {name: {{ $secret }}, key: CONSOLE_ADMIN_PASSWORD}}}
    {{- end }}
  extraEnv: {{ toJson $v.command.env }}
  resources: {{ toJson $v.command.resources }}
query:
  enabled: true
  replicas: {{ $v.replicaCount.query }}
  image: {{ toJson $v.images.query }}
  container: workflow-query
  app: true
  security: app
  ports: {http: 8081, metrics: 9090}
  probePort: http
  env:
    - {{ $introspection }}
  extraEnv: {{ toJson $v.query.env }}
  resources: {{ toJson $v.query.resources }}
runtime:
  enabled: {{ $v.runtime.enabled }}
  replicas: {{ $v.replicaCount.runtime }}
  image: {{ toJson $v.images.runtime }}
  container: workflow-runtime
  app: true
  security: app
  ports: {metrics: 9090}
  probePort: metrics
  env:
    - {name: RUNTIME_ENABLED, value: "true"}
    - {name: RUNTIME_SCHEDULER_MODE, value: {{ $v.runtime.schedulerMode | quote }}}
    - {name: METRICS_ADDR, value: ":9090"}
    - {{ $pgDsn }}
  extraEnv: {{ toJson $v.runtime.env }}
  resources: {{ toJson $v.runtime.resources }}
sync-worker:
  enabled: {{ $v.syncWorker.enabled }}
  replicas: {{ $v.replicaCount.syncWorker }}
  image: {{ toJson $v.images.syncWorker }}
  app: true
  security: app
  ports: {http: 8092}
  probePort: http
  liveDelay: 30
  readyDelay: 10
  env:
    - {name: CONNECTOR_DATABASE_PASSWORD, valueFrom: {secretKeyRef: {name: {{ $secret }}, key: POSTGRES_PASSWORD, optional: true}}}
  extraEnv: {{ toJson $v.syncWorker.env }}
  resources: {{ toJson $v.syncWorker.resources }}
frontend:
  enabled: true
  replicas: {{ $v.replicaCount.frontend }}
  image: {{ toJson $v.images.frontend }}
  app: true
  ports: {http: 80}
  probePort: http
  resources: {{ toJson $v.frontend.resources }}
gateway:
  enabled: {{ $v.gateway.enabled }}
  replicas: {{ $v.replicaCount.gateway }}
  image: {{ toJson $v.images.gateway }}
  app: true
  noSharedEnv: true
  security: app
  serviceType: {{ $v.gateway.serviceType | default "ClusterIP" }}
  ports: {http: 8080}
  servicePorts: {http: 80}
  probePort: http
  volumeMounts:
    - {name: nginx-config, mountPath: /etc/nginx/conf.d/default.conf, subPath: default.conf, readOnly: true}
    - {name: nginx-cache, mountPath: /var/cache/nginx}
    - {name: nginx-run, mountPath: /var/run}
    - {name: tmp, mountPath: /tmp}
  volumes:
    - {name: nginx-config, configMap: {name: {{ include "artificialflow.fullname" . }}-gateway-nginx}}
    - {name: nginx-cache, emptyDir: {}}
    - {name: nginx-run, emptyDir: {}}
    - {name: tmp, emptyDir: {}}
  resources: {{ toJson $v.gateway.resources }}
postgresql:
  enabled: {{ $v.postgresql.bundled.enabled }}
  kind: StatefulSet
  image: {{ toJson $v.postgresql.bundled.image }}
  args: [postgres, -c, wal_level=logical, -c, max_wal_senders=10, -c, max_replication_slots=10]
  ports: {postgres: 5432}
  servicePorts: {postgres: {{ $v.postgresql.port }}}
  env:
    - {name: POSTGRES_DB, value: {{ $v.postgresql.database | quote }}}
    - {name: POSTGRES_USER, value: {{ $v.postgresql.user | quote }}}
    - {name: POSTGRES_PASSWORD, valueFrom: {secretKeyRef: {name: {{ $secret }}, key: POSTGRES_PASSWORD}}}
  readinessProbe: {exec: {command: [/bin/sh, -ec, 'pg_isready -U "$POSTGRES_USER" -d "$POSTGRES_DB"']}, initialDelaySeconds: 10, periodSeconds: 10}
  persistence: {size: {{ $v.postgresql.bundled.size }}, storageClassName: {{ $v.postgresql.bundled.storageClassName | quote }}, mountPath: /var/lib/postgresql/data}
  resources: {{ toJson $v.postgresql.bundled.resources }}
elasticsearch:
  enabled: {{ $v.search.bundled.enabled }}
  kind: StatefulSet
  image: {{ toJson $v.search.bundled.image }}
  security: 1000
  ports: {http: 9200}
  env:
    - {name: discovery.type, value: single-node}
    - {name: xpack.security.enabled, value: "false"}
    - {name: ES_JAVA_OPTS, value: {{ $v.search.bundled.javaOpts | quote }}}
  readinessProbe: {exec: {command: [/bin/sh, -ec, "curl -s http://localhost:9200 >/dev/null"]}, initialDelaySeconds: 20, periodSeconds: 10}
  persistence: {size: {{ $v.search.bundled.size }}, storageClassName: {{ $v.search.bundled.storageClassName | quote }}, mountPath: /usr/share/elasticsearch/data}
  resources: {{ toJson $v.search.bundled.resources }}
{{- $kafkaHost := printf "%s-kafka" (include "artificialflow.fullname" .) }}
kafka:
  enabled: {{ $v.kafka.bundled.enabled }}
  kind: StatefulSet
  headless: true
  image: {{ toJson $v.kafka.bundled.image }}
  security: 1000
  ports: {kafka: 9092, controller: 9093}
  env:
    - {name: CLUSTER_ID, value: MkU3OEVBNTcwNTJENDM2Qg}
    - {name: KAFKA_NODE_ID, value: "1"}
    - {name: KAFKA_PROCESS_ROLES, value: "broker,controller"}
    - {name: KAFKA_CONTROLLER_QUORUM_VOTERS, value: "1@{{ $kafkaHost }}-0.{{ $kafkaHost }}:9093"}
    - {name: KAFKA_LISTENERS, value: "PLAINTEXT://0.0.0.0:9092,CONTROLLER://0.0.0.0:9093"}
    - {name: KAFKA_ADVERTISED_LISTENERS, value: "PLAINTEXT://{{ $kafkaHost }}:9092"}
    - {name: KAFKA_LISTENER_SECURITY_PROTOCOL_MAP, value: "CONTROLLER:PLAINTEXT,PLAINTEXT:PLAINTEXT"}
    - {name: KAFKA_CONTROLLER_LISTENER_NAMES, value: CONTROLLER}
    - {name: KAFKA_INTER_BROKER_LISTENER_NAME, value: PLAINTEXT}
    - {name: KAFKA_NUM_PARTITIONS, value: {{ $v.kafka.topicPartitions | quote }}}
    - {name: KAFKA_OFFSETS_TOPIC_REPLICATION_FACTOR, value: "1"}
    - {name: KAFKA_TRANSACTION_STATE_LOG_MIN_ISR, value: "1"}
    - {name: KAFKA_TRANSACTION_STATE_LOG_REPLICATION_FACTOR, value: "1"}
    - {name: KAFKA_LOG_DIRS, value: /var/lib/kafka/data}
  readinessProbe: {tcpSocket: {port: kafka}, initialDelaySeconds: 20, periodSeconds: 10}
  persistence: {size: {{ $v.kafka.bundled.size }}, storageClassName: {{ $v.kafka.bundled.storageClassName | quote }}, mountPath: /var/lib/kafka/data}
  resources: {{ toJson $v.kafka.bundled.resources }}
connect:
  enabled: {{ $v.connect.bundled.enabled }}
  image: {{ toJson $v.connect.bundled.image }}
  security: 1001
  ports: {http: 8083}
  env:
    - {name: BOOTSTRAP_SERVERS, value: {{ include "artificialflow.kafkaBrokers" . | quote }}}
    - {name: GROUP_ID, value: artificialflow-debezium}
    - {name: CONFIG_STORAGE_TOPIC, value: artificialflow_connect_configs}
    - {name: OFFSET_STORAGE_TOPIC, value: artificialflow_connect_offsets}
    - {name: STATUS_STORAGE_TOPIC, value: artificialflow_connect_statuses}
    - {name: CONFIG_STORAGE_REPLICATION_FACTOR, value: "1"}
    - {name: OFFSET_STORAGE_REPLICATION_FACTOR, value: "1"}
    - {name: STATUS_STORAGE_REPLICATION_FACTOR, value: "1"}
    - {name: KEY_CONVERTER, value: org.apache.kafka.connect.json.JsonConverter}
    - {name: VALUE_CONVERTER, value: org.apache.kafka.connect.json.JsonConverter}
    - {name: KEY_CONVERTER_SCHEMAS_ENABLE, value: "false"}
    - {name: VALUE_CONVERTER_SCHEMAS_ENABLE, value: "false"}
    - {name: ENABLE_DEBEZIUM_SCRIPTING, value: "true"}
  readinessProbe: {httpGet: {path: /connectors, port: http}, initialDelaySeconds: 20, periodSeconds: 10}
  volumeMounts: [{name: tmp, mountPath: /tmp}]
  volumes: [{name: tmp, emptyDir: {}}]
  resources: {{ toJson $v.connect.bundled.resources }}
{{- end -}}
