package dmn

import (
	"strings"
	"testing"
)

func TestParseDMNXMLSubsetAndEvaluate(t *testing.T) {
	table, err := Parse([]byte(`<?xml version="1.0"?>
<definitions xmlns="https://www.omg.org/spec/DMN/20191111/MODEL/">
  <decision id="invoice-routing">
    <decisionTable hitPolicy="FIRST">
      <input><inputExpression><text>amount</text></inputExpression></input>
      <output name="route"/>
      <rule><inputEntry><text>&gt; 1000</text></inputEntry><outputEntry><text>"manager"</text></outputEntry></rule>
      <rule><inputEntry><text>-</text></inputEntry><outputEntry><text>"automatic"</text></outputEntry></rule>
    </decisionTable>
  </decision>
</definitions>`))
	if err != nil {
		t.Fatal(err)
	}
	output, err := Evaluate(table, map[string]any{"amount": 1200})
	if err != nil {
		t.Fatal(err)
	}
	if output["route"] != "manager" {
		t.Fatalf("output = %#v", output)
	}
}

func TestParseDMNXMLRejectsUnsupportedConstruct(t *testing.T) {
	_, err := Parse([]byte(`<definitions><decision id="d"><literalExpression><text>1</text></literalExpression></decision></definitions>`))
	if err == nil || !strings.Contains(err.Error(), "unsupported DMN element") {
		t.Fatalf("expected strict unsupported rejection, got %v", err)
	}
}

func TestParseRejectsUnsupportedJSONOperator(t *testing.T) {
	_, err := Parse([]byte(`{"id":"d","rules":[{"when":{"x":{"op":"contains","value":"a"}},"then":{}}]}`))
	if err == nil || !strings.Contains(err.Error(), "unsupported operator") {
		t.Fatalf("expected unsupported operator rejection, got %v", err)
	}
}
