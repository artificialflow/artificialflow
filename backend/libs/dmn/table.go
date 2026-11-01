package dmn

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

// DecisionTable is a minimal ArtificialFlow decision-table format (not full DMN XML).
// Deploy as JSON via the decisions API and reference with artificialflow:decisionRef.
type DecisionTable struct {
	ID        string `json:"id"`
	HitPolicy string `json:"hitPolicy"` // FIRST (default)
	Rules     []Rule `json:"rules"`
}

type Rule struct {
	When map[string]Condition `json:"when"`
	Then map[string]any       `json:"then"`
}

type Condition struct {
	Op    string `json:"op"` // eq, neq, gt, gte, lt, lte, exists
	Value any    `json:"value"`
}

func Parse(raw []byte) (*DecisionTable, error) {
	if strings.HasPrefix(strings.TrimSpace(string(raw)), "<") {
		return parseXML(raw)
	}
	var table DecisionTable
	if err := json.Unmarshal(raw, &table); err != nil {
		return nil, err
	}
	if strings.TrimSpace(table.ID) == "" {
		return nil, fmt.Errorf("decision table id is required")
	}
	if table.HitPolicy == "" {
		table.HitPolicy = "FIRST"
	}
	if strings.ToUpper(table.HitPolicy) != "FIRST" {
		return nil, fmt.Errorf("unsupported hit policy %q; supported subset: FIRST", table.HitPolicy)
	}
	table.HitPolicy = "FIRST"
	for ruleIndex, rule := range table.Rules {
		for key, condition := range rule.When {
			switch strings.ToLower(strings.TrimSpace(condition.Op)) {
			case "", "eq", "neq", "gt", "gte", "lt", "lte", "exists":
			default:
				return nil, fmt.Errorf("rule %d input %q uses unsupported operator %q", ruleIndex+1, key, condition.Op)
			}
		}
	}
	return &table, nil
}

func Evaluate(table *DecisionTable, inputs map[string]any) (map[string]any, error) {
	if table == nil {
		return nil, fmt.Errorf("decision table is nil")
	}
	if inputs == nil {
		inputs = map[string]any{}
	}
	for _, rule := range table.Rules {
		if matchRule(rule.When, inputs) {
			out := map[string]any{}
			for k, v := range rule.Then {
				out[k] = v
			}
			return out, nil
		}
	}
	return map[string]any{}, nil
}

func matchRule(when map[string]Condition, inputs map[string]any) bool {
	for key, cond := range when {
		val, ok := inputs[key]
		op := strings.ToLower(strings.TrimSpace(cond.Op))
		if op == "" {
			op = "eq"
		}
		switch op {
		case "exists":
			if !ok {
				return false
			}
		case "eq":
			if !ok || !equals(val, cond.Value) {
				return false
			}
		case "neq":
			if ok && equals(val, cond.Value) {
				return false
			}
		case "gt", "gte", "lt", "lte":
			left, lok := asFloat(val)
			right, rok := asFloat(cond.Value)
			if !lok || !rok {
				return false
			}
			switch op {
			case "gt":
				if !(left > right) {
					return false
				}
			case "gte":
				if !(left >= right) {
					return false
				}
			case "lt":
				if !(left < right) {
					return false
				}
			case "lte":
				if !(left <= right) {
					return false
				}
			}
		default:
			return false
		}
	}
	return true
}

func equals(a, b any) bool {
	if a == nil && b == nil {
		return true
	}
	return fmt.Sprint(a) == fmt.Sprint(b)
}

func asFloat(v any) (float64, bool) {
	switch t := v.(type) {
	case float64:
		return t, true
	case float32:
		return float64(t), true
	case int:
		return float64(t), true
	case int64:
		return float64(t), true
	case json.Number:
		f, err := t.Float64()
		return f, err == nil
	case string:
		f, err := strconv.ParseFloat(t, 64)
		return f, err == nil
	default:
		return 0, false
	}
}
