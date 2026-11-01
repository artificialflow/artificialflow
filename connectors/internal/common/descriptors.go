package common

import (
	_ "embed"
	"encoding/json"
	"strings"
)

// ConnectorField describes a single connector input variable.
type ConnectorField struct {
	Key         string `json:"key"`
	Label       string `json:"label"`
	Required    bool   `json:"required"`
	Description string `json:"description"`
	// Kind is "string", "number", "boolean", or "json".
	Kind    string `json:"kind"`
	Default string `json:"default,omitempty"`
}

// ConnectorDescriptor documents a shipped connector job type and its inputs.
type ConnectorDescriptor struct {
	JobType     string           `json:"jobType"`
	Name        string           `json:"name"`
	Description string           `json:"description"`
	Fields      []ConnectorField `json:"fields"`
}

//go:embed descriptors.json
var descriptorJSON []byte

// Descriptors is loaded from the canonical descriptor file shared with the UI.
var Descriptors = mustLoadDescriptors()

func mustLoadDescriptors() []ConnectorDescriptor {
	var descriptors []ConnectorDescriptor
	if err := json.Unmarshal(descriptorJSON, &descriptors); err != nil {
		panic("invalid embedded connector descriptors: " + err.Error())
	}
	return descriptors
}

// DescriptorByJobType returns the descriptor for a job type, if known.
func DescriptorByJobType(jobType string) (ConnectorDescriptor, bool) {
	for _, d := range Descriptors {
		if d.JobType == jobType {
			return d, true
		}
	}
	return ConnectorDescriptor{}, false
}

// AllConnectorInputKeys returns every known connector input variable key.
func AllConnectorInputKeys() []string {
	seen := map[string]struct{}{}
	var keys []string
	for _, d := range Descriptors {
		for _, f := range d.Fields {
			if _, ok := seen[f.Key]; ok {
				continue
			}
			seen[f.Key] = struct{}{}
			keys = append(keys, f.Key)
		}
	}
	return keys
}

// IsConnectorInputKey reports whether key is a known connector input variable.
func IsConnectorInputKey(key string) bool {
	for _, k := range AllConnectorInputKeys() {
		if k == key {
			return true
		}
	}
	return false
}

// ConnectorInputsFromProperties extracts known connector keys from step properties.
// Empty string values are skipped.
func ConnectorInputsFromProperties(props map[string]any) map[string]any {
	out := map[string]any{}
	if props == nil {
		return out
	}
	for _, key := range AllConnectorInputKeys() {
		v, ok := props[key]
		if !ok || v == nil {
			continue
		}
		if s, ok := v.(string); ok && strings.TrimSpace(s) == "" {
			continue
		}
		out[key] = v
	}
	return out
}
