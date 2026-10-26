package bpmn

import (
	"strings"
	"testing"
)

func FuzzParse(f *testing.F) {
	seeds := []string{
		`<?xml version="1.0"?><definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL"><process id="p"><startEvent id="s"/></process></definitions>`,
		`<definitions>`,
		``,
		`<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL"><process id="p"><sequenceFlow sourceRef="missing" targetRef="missing"/></process></definitions>`,
	}
	for _, seed := range seeds {
		f.Add(seed)
	}

	f.Fuzz(func(t *testing.T, input string) {
		if len(input) > 256*1024 {
			t.Skip()
		}
		_, _ = Parse(strings.NewReader(input))
	})
}
