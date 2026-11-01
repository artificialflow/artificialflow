package dmn

import (
	"bytes"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"strconv"
	"strings"
)

// XMLSubset documents the intentionally small, deterministic standards subset.
const XMLSubset = "DMN 1.3 decision/decisionTable, FIRST hit policy, inputExpression, output, rule, inputEntry/outputEntry, scalar unary tests"

type xmlDefinitions struct {
	Decisions []xmlDecision `xml:"decision"`
}

type xmlDecision struct {
	ID    string           `xml:"id,attr"`
	Table xmlDecisionTable `xml:"decisionTable"`
}

type xmlDecisionTable struct {
	HitPolicy string      `xml:"hitPolicy,attr"`
	Inputs    []xmlInput  `xml:"input"`
	Outputs   []xmlOutput `xml:"output"`
	Rules     []xmlRule   `xml:"rule"`
}

type xmlInput struct {
	Expression xmlText `xml:"inputExpression"`
}

type xmlOutput struct {
	Name string `xml:"name,attr"`
}

type xmlRule struct {
	Inputs  []xmlText `xml:"inputEntry"`
	Outputs []xmlText `xml:"outputEntry"`
}

type xmlText struct {
	Text string `xml:"text"`
}

func parseXML(raw []byte) (*DecisionTable, error) {
	if err := rejectUnsupportedXMLElements(raw); err != nil {
		return nil, err
	}
	var definitions xmlDefinitions
	if err := xml.Unmarshal(raw, &definitions); err != nil {
		return nil, fmt.Errorf("invalid decision-table XML: %w", err)
	}
	if len(definitions.Decisions) != 1 {
		return nil, fmt.Errorf("DMN XML subset requires exactly one decision")
	}
	decision := definitions.Decisions[0]
	if strings.TrimSpace(decision.ID) == "" {
		return nil, fmt.Errorf("DMN decision id is required")
	}
	hitPolicy := strings.ToUpper(strings.TrimSpace(decision.Table.HitPolicy))
	if hitPolicy == "" {
		hitPolicy = "FIRST"
	}
	if hitPolicy != "FIRST" {
		return nil, fmt.Errorf("unsupported DMN hit policy %q; supported subset: FIRST", hitPolicy)
	}
	if len(decision.Table.Inputs) == 0 || len(decision.Table.Outputs) == 0 {
		return nil, fmt.Errorf("DMN decisionTable requires at least one input and output")
	}
	inputNames := make([]string, len(decision.Table.Inputs))
	for index, input := range decision.Table.Inputs {
		inputNames[index] = strings.TrimSpace(input.Expression.Text)
		if inputNames[index] == "" {
			return nil, fmt.Errorf("DMN input %d requires inputExpression/text", index+1)
		}
	}
	outputNames := make([]string, len(decision.Table.Outputs))
	for index, output := range decision.Table.Outputs {
		outputNames[index] = strings.TrimSpace(output.Name)
		if outputNames[index] == "" {
			return nil, fmt.Errorf("DMN output %d requires a name", index+1)
		}
	}

	table := &DecisionTable{ID: decision.ID, HitPolicy: hitPolicy, Rules: make([]Rule, 0, len(decision.Table.Rules))}
	for ruleIndex, source := range decision.Table.Rules {
		if len(source.Inputs) != len(inputNames) || len(source.Outputs) != len(outputNames) {
			return nil, fmt.Errorf("DMN rule %d entry count does not match table inputs/outputs", ruleIndex+1)
		}
		rule := Rule{When: map[string]Condition{}, Then: map[string]any{}}
		for index, entry := range source.Inputs {
			condition, wildcard, err := parseUnaryTest(entry.Text)
			if err != nil {
				return nil, fmt.Errorf("DMN rule %d input %q: %w", ruleIndex+1, inputNames[index], err)
			}
			if !wildcard {
				rule.When[inputNames[index]] = condition
			}
		}
		for index, entry := range source.Outputs {
			value, err := parseScalar(entry.Text)
			if err != nil {
				return nil, fmt.Errorf("DMN rule %d output %q: %w", ruleIndex+1, outputNames[index], err)
			}
			rule.Then[outputNames[index]] = value
		}
		table.Rules = append(table.Rules, rule)
	}
	return table, nil
}

func parseUnaryTest(raw string) (Condition, bool, error) {
	value := strings.TrimSpace(raw)
	if value == "-" {
		return Condition{}, true, nil
	}
	for _, candidate := range []struct{ prefix, op string }{
		{">=", "gte"}, {"<=", "lte"}, {"!=", "neq"}, {">", "gt"}, {"<", "lt"}, {"=", "eq"},
	} {
		if strings.HasPrefix(value, candidate.prefix) {
			scalar, err := parseScalar(strings.TrimSpace(strings.TrimPrefix(value, candidate.prefix)))
			return Condition{Op: candidate.op, Value: scalar}, false, err
		}
	}
	if strings.ContainsAny(value, "(),[]") {
		return Condition{}, false, fmt.Errorf("unsupported unary test %q", value)
	}
	scalar, err := parseScalar(value)
	return Condition{Op: "eq", Value: scalar}, false, err
}

func parseScalar(raw string) (any, error) {
	value := strings.TrimSpace(raw)
	if value == "" {
		return nil, fmt.Errorf("empty expression is unsupported")
	}
	var decoded any
	if json.Unmarshal([]byte(value), &decoded) == nil {
		switch decoded.(type) {
		case nil, bool, float64, string:
			return decoded, nil
		}
	}
	if number, err := strconv.ParseFloat(value, 64); err == nil {
		return number, nil
	}
	return nil, fmt.Errorf("expression %q must be a JSON scalar", value)
}

func rejectUnsupportedXMLElements(raw []byte) error {
	allowed := map[string]bool{
		"definitions": true, "decision": true, "decisionTable": true,
		"input": true, "inputExpression": true, "output": true,
		"rule": true, "inputEntry": true, "outputEntry": true,
		"text": true, "description": true,
	}
	decoder := xml.NewDecoder(bytes.NewReader(raw))
	for {
		token, err := decoder.Token()
		if err != nil {
			if err == io.EOF {
				return nil
			}
			return fmt.Errorf("invalid decision-table XML: %w", err)
		}
		if start, ok := token.(xml.StartElement); ok && !allowed[start.Name.Local] {
			return fmt.Errorf("unsupported DMN element <%s>; supported subset: %s", start.Name.Local, XMLSubset)
		}
	}
}
