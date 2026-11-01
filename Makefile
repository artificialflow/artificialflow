.PHONY: up up-release down restart logs ps clean smoke smoke-release demo build-backend build-frontend sdk-dotnet sdk-java validate-helm validate-legacy-branding validate-release-version validate-migration-release test-schema-migrations test-recovery release-dry-run validate-release-evidence release-evidence-report kind-release-smoke release-chaos release-soak release-data-recovery post-soak-functional cqrs-parity-check cqrs-e2e-smoke worker-conformance validate-bpmn-capabilities test-bpmn-matrix test-bpmn-exhaustive test-deployment-matrix test-uat-videos test-uat-mega-bpmn test-unit test-integration test-e2e test-frontend test-perf test-security test-dast test-release-suite test-report test-all test-all-functionality quality-maintainability quality-coverage quality-race quality-fuzz quality-mutation quality-fast


# Docker Compose Commands
COMPOSE := docker compose

up:
	$(COMPOSE) up -d --build

up-release:
	$(COMPOSE) pull
	$(COMPOSE) up -d --no-build

smoke:
	$(COMPOSE) config > /dev/null
	bash scripts/validate_compose_kafka_wiring.sh -f docker-compose.yml

smoke-release: smoke

sdk-dotnet:
	dotnet test clients/dotnet-sdk/ArtificialFlow.Sdk.sln

sdk-java:
	mvn -B -f clients/java-sdk/pom.xml verify

validate-helm:
	bash scripts/validate_helm.sh

validate-legacy-branding:
	node scripts/validate_legacy_branding.mjs

validate-release-version:
	node scripts/validate_release_version.mjs

validate-migration-release:
	bash scripts/validate_migration_release.sh

test-schema-migrations:
	bash scripts/test_schema_migrations.sh

test-recovery:
	bash scripts/test_recovery.sh

release-dry-run:
	bash scripts/release_dry_run.sh

validate-release-evidence:
	python3 scripts/release/evidence.py validate-manifest

release-evidence-report:
	python3 scripts/release/evidence.py report

kind-release-smoke:
	bash scripts/release/kind_helm_smoke.sh

release-chaos:
	bash scripts/release/chaos.sh

release-soak:
	bash scripts/release/run_soak.sh

release-data-recovery:
	bash scripts/release/data_recovery_gate.sh

post-soak-functional:
	bash scripts/release/post_soak_functional.sh

cqrs-parity-check:
	./scripts/cqrs_parity_check.sh

cqrs-e2e-smoke:
	./scripts/cqrs_e2e_smoke.sh

worker-conformance:
	bash ./scripts/worker_conformance_smoke.sh

init-connector:
	./scripts/init_connector.sh

down:
	$(COMPOSE) down

restart: down up

logs:
	$(COMPOSE) logs -f

ps:
	$(COMPOSE) ps

clean:
	$(COMPOSE) down -v

# Demo
demo:
	./demo.sh

# Tests
validate-bpmn-capabilities:
	python3 scripts/qa/validate_bpmn_capabilities.py

test-bpmn-matrix:
	@$(MAKE) validate-bpmn-capabilities
	go test ./backend/services/workflow-command/internal/domain/bpmn -run 'TestParse_(ElementTypeMatrix|MapsExtendedElementsAndProperties|MapsPlainAttributeAliasesForCompatibility|MergesExtensionPropertiesWithoutOverridingMappedKeys|CanonicalizesExtensionPropertyAliases|BoundaryCancelActivityAttributeTakesPrecedenceOverExtensionAlias|PopulatesIncomingForGatewayJoin|FailsForUnsupportedElementReferences|SupportsSendTask|MessageAndSignalStartProps|TerminateAndLink|SupportsEscalationAndEventSubProcess|MultiInstance|IOMappingsAndListeners)' -count=1
	go test ./backend/services/workflow-command/tests -run 'TestDeployWorkflowFromBPMN_(CallActivityBusinessRuleAndManualTask|EventBasedGatewayReceiveAndTimer|BoundaryTimerInterruptsTask|ThrowSignalTriggersCatch|ThrowMessageUsesCorrelationKey|ThrowMessageUsesPlainCorrelationKeyAlias|BoundaryTimerNonInterruptingKeepsTaskActive|BoundaryCancelActivityExtensionAliasKeepsTaskActive|BoundaryCancelActivityAttributeTakesPrecedenceOverExtensionAlias|ServiceTaskPlainTaskTypeAliasMapsImplementation|ServiceTaskExtensionPropertyMapsImplementation|UserTaskAssignmentFromExtensionProperties|FailsForUnsupportedElementReferences|SupportsSendTask|LinkAndTerminate|EscalationBoundary|ConditionalCatch|MessageStart|TimerStart|TimerStartDate|SignalStart|TransactionCancel|CompensationFromXML|BoundaryErrorFromXML|EventSubProcessMessage|EventSubProcessTimer|EventSubProcessSignal|SignalBoundary|MultiInstance|ExclusiveDefault|ParallelJoin|InclusiveJoin)|TestManualTaskWaitsForComplete' -count=1
	go test ./connectors/http ./connectors/internal/common -count=1

test-bpmn-exhaustive:
	@mkdir -p reports
	@$(MAKE) validate-bpmn-capabilities
	python3 scripts/qa/run_bpmn_matrix.py --reports-dir reports --output-json reports/bpmn-matrix-report.json --output-md reports/bpmn-matrix-report.md

test-deployment-matrix:
	@mkdir -p reports
	bash scripts/test-all-functionality.sh --skip-ui --skip-sdk-live --skip-perf --skip-security

test-uat-videos:
	bash scripts/qa/run_uat_video_suite.sh both

# Mega BPMN UAT (requires ARTIFICIALFLOW_TOKEN + healthy stack)
test-uat-mega-bpmn:
	@mkdir -p reports
	cd tests/e2e/playwright && npm install --silent && npx playwright test specs/uat-mega-bpmn.spec.ts --reporter=list

# -----------------------------------------------------------------------
# Test Targets
# -----------------------------------------------------------------------

test-unit:
	@mkdir -p reports
	bash -o pipefail -c 'go test ./backend/... -coverprofile=reports/coverage.out -covermode=atomic -json 2>&1 | tee reports/unit-raw.json'
	go tool cover -func=reports/coverage.out > reports/coverage.txt
	@echo "Unit tests complete. Coverage in reports/coverage.txt"

quality-maintainability:
	python3 scripts/quality/maintainability.py

quality-coverage:
	bash scripts/quality/coverage_gate.sh

quality-race:
	bash scripts/quality/race.sh

quality-fuzz:
	bash scripts/quality/fuzz.sh

quality-mutation:
	MUTATION_REQUIRED=true bash scripts/quality/mutation.sh

quality-fast: quality-maintainability quality-coverage quality-race quality-fuzz

test-integration:
	@mkdir -p reports
	curl -fsS http://localhost:8080/health >/dev/null 2>&1 || (echo "Stack not running. Run make up first." && exit 1)
	bash -o pipefail -c 'go test ./tests/integration/... -tags integration -v -json 2>&1 | tee reports/integration-raw.json'
	@echo "Integration tests complete."

test-e2e:
	@mkdir -p reports
	chmod +x scripts/cqrs_e2e_smoke.sh scripts/worker_conformance_smoke.sh scripts/cqrs_parity_check.sh
	bash scripts/test-e2e.sh

test-frontend:
	@mkdir -p reports
	bash -o pipefail -c 'cd frontend && npm install --silent && npx vitest run --reporter=json --outputFile=../reports/frontend-vitest.json 2>&1 | tee ../reports/frontend-vitest.txt'
	bash -o pipefail -c 'cd tests/e2e/playwright && npm install --silent && npx playwright install --with-deps chromium 2>/dev/null && npx playwright test specs/workflow.spec.ts --reporter=json 2>&1 | tee ../../../reports/playwright-raw.json'

test-perf:
	@mkdir -p reports
	bash -o pipefail -c 'docker run --rm --network host \
		-e COMMAND_URL="$(or $(COMMAND_URL),http://localhost:8080)" \
		-e QUERY_URL="$(or $(QUERY_URL),http://localhost:8081)" \
		-e AUTH_TOKEN="$(AUTH_TOKEN)" \
		-e WORKFLOW_ID="$(WORKFLOW_ID)" \
		-v "$(PWD)/tests/performance/k6:/scripts" \
		-v "$(PWD)/reports:/reports" \
		grafana/k6:0.57.0 run --out json=/reports/k6.json /scripts/run-all.js 2>&1 | tee reports/perf-raw.txt'
	@echo "Performance tests complete."

test-security:
	@mkdir -p reports
	bash scripts/test-security.sh
	@echo "[security] npm audit (frontend + nodejs-sdk + playwright)"
	npm --prefix frontend audit --audit-level=high --omit=dev
	npm --prefix clients/nodejs-sdk audit --audit-level=high --omit=dev
	@if [ -f tests/e2e/playwright/package.json ]; then npm --prefix tests/e2e/playwright audit --audit-level=high --omit=dev; fi
	@echo "[security] gitleaks (required)"
	@command -v gitleaks >/dev/null 2>&1 || { echo "gitleaks is required" >&2; exit 1; }
	bash -o pipefail -c 'gitleaks detect --source . --redact --no-git -v 2>&1 | tee reports/gitleaks.txt'
	@echo "CodeQL runs in .github/workflows/security.yml and is release-blocking."

test-dast:
	@mkdir -p reports/dast
	chmod +x scripts/test-dast.sh
	bash scripts/test-dast.sh

# Unified release gate (Phase T) — run against a healthy stack where noted.
test-release-suite:
	@mkdir -p reports
	@echo "== release suite: unit =="
	@$(MAKE) test-unit
	@echo "== release suite: bpmn matrix =="
	@$(MAKE) test-bpmn-matrix
	@echo "== release suite: bpmn exhaustive =="
	@$(MAKE) test-bpmn-exhaustive
	@echo "== release suite: smoke + helm =="
	@$(MAKE) smoke
	@$(MAKE) validate-helm
	@echo "== release suite: security (SAST) =="
	@$(MAKE) test-security
	@echo "== release suite: worker conformance (requires stack) =="
	@$(MAKE) worker-conformance
	@echo "== release suite: frontend unit =="
	@mkdir -p reports && cd frontend && npm install --silent && npx vitest run --reporter=json --outputFile=../reports/frontend-vitest.json
	@echo "Release suite core complete. Also run: make test-all, make test-all-functionality, make test-perf, make test-dast, make test-uat-mega-bpmn."

test-report:
	@mkdir -p reports
	bash scripts/test-report.sh

test-all:
	@mkdir -p reports
	bash scripts/test-all.sh

test-all-functionality:
	@mkdir -p reports
	bash scripts/test-all-functionality.sh

# Local Development Helpers
build-command:
	go build -o bin/workflow-command backend/services/workflow-command/cmd/server/main.go

build-runtime:
	go build -o bin/workflow-runtime backend/services/workflow-command/cmd/runtime/main.go

build-query:
	go build -o bin/workflow-query backend/services/workflow-query/cmd/server/main.go

build-worker:
	go build -o bin/sync-worker backend/services/sync-worker/cmd/main.go

build-backend: build-command build-runtime build-query build-worker

build-frontend:
	cd frontend && npm install && npm run build
