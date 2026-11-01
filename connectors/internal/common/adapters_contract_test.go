package common

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestShippedConnectorsAreThinRuntimeAdapters(t *testing.T) {
	for _, connector := range []string{"http", "webhook", "kafka", "email", "s3"} {
		source, err := os.ReadFile(filepath.Join("..", "..", connector, "main.go"))
		if err != nil {
			t.Fatal(err)
		}
		text := string(source)
		if !strings.Contains(text, "common.NewRuntime") {
			t.Errorf("%s connector does not use the shared runtime", connector)
		}
		for _, duplicated := range []string{"worker.NewWorker", "common.LoadInstanceVars"} {
			if strings.Contains(text, duplicated) {
				t.Errorf("%s connector duplicates runtime concern %s", connector, duplicated)
			}
		}
	}
}

func TestEveryDescriptorBindsAValidMinimalInput(t *testing.T) {
	inputs := map[string]map[string]any{
		"io.artificialflow.connector.http":    {"url": "https://example.test"},
		"io.artificialflow.connector.webhook": {"webhookUrl": "https://example.test"},
		"io.artificialflow.connector.kafka":   {"kafkaTopic": "events"},
		"io.artificialflow.connector.email":   {"emailTo": "ops@example.test", "emailSubject": "subject"},
		"io.artificialflow.connector.s3":      {"s3Bucket": "bucket", "s3Key": "key"},
	}
	if len(inputs) != len(Descriptors) {
		t.Fatalf("minimal input fixtures=%d descriptors=%d", len(inputs), len(Descriptors))
	}
	for _, descriptor := range Descriptors {
		if _, err := BindConnectorInputs(t.Context(), descriptor.JobType, inputs[descriptor.JobType], staticSecrets{}); err != nil {
			t.Errorf("%s input contract: %v", descriptor.JobType, err)
		}
	}
}
