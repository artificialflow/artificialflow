package elasticsearch

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func TestEncodeBulkBodyNDJSON(t *testing.T) {
	body, err := encodeBulkBody([]WriteOp{
		{Kind: WriteIndex, Index: "idx-a", ID: "1", Doc: map[string]any{"state": "ACTIVE"}},
		{Kind: WriteDelete, Index: "idx-a", ID: "2"},
		{
			Kind: WriteUpsertScript, Index: "idx-b", ID: "3",
			Script: "ctx._source.state = params.state;",
			Params: map[string]any{"state": "COMPLETED"},
			Upsert: map[string]any{"key": 3, "state": "COMPLETED"},
		},
		{
			Kind: WriteUpdateScript, Index: "idx-b", ID: "4",
			Script: "ctx._source.context.remove(params.name);",
			Params: map[string]any{"name": "x"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	lines := bytes.Split(bytes.TrimSuffix(body, []byte("\n")), []byte("\n"))
	if len(lines) != 7 {
		t.Fatalf("expected 7 NDJSON lines, got %d:\n%s", len(lines), body)
	}

	var indexMeta map[string]any
	if err := json.Unmarshal(lines[0], &indexMeta); err != nil {
		t.Fatal(err)
	}
	indexAction := indexMeta["index"].(map[string]any)
	if indexAction["_index"] != "idx-a" || indexAction["_id"] != "1" {
		t.Fatalf("unexpected index meta: %#v", indexAction)
	}

	var doc map[string]any
	if err := json.Unmarshal(lines[1], &doc); err != nil {
		t.Fatal(err)
	}
	if doc["state"] != "ACTIVE" {
		t.Fatalf("unexpected doc: %#v", doc)
	}

	var deleteMeta map[string]any
	if err := json.Unmarshal(lines[2], &deleteMeta); err != nil {
		t.Fatal(err)
	}
	if deleteMeta["delete"] == nil {
		t.Fatalf("expected delete meta, got %#v", deleteMeta)
	}

	var updateMeta map[string]any
	if err := json.Unmarshal(lines[3], &updateMeta); err != nil {
		t.Fatal(err)
	}
	updateAction := updateMeta["update"].(map[string]any)
	if updateAction["retry_on_conflict"].(float64) != 5 {
		t.Fatalf("expected retry_on_conflict=5, got %#v", updateAction["retry_on_conflict"])
	}

	var updateBody map[string]any
	if err := json.Unmarshal(lines[4], &updateBody); err != nil {
		t.Fatal(err)
	}
	if updateBody["upsert"] == nil {
		t.Fatal("expected upsert document on WriteUpsertScript")
	}

	var updateOnly map[string]any
	if err := json.Unmarshal(lines[6], &updateOnly); err != nil {
		t.Fatal(err)
	}
	if _, ok := updateOnly["upsert"]; ok {
		t.Fatal("WriteUpdateScript must not include upsert")
	}
}

func TestParseBulkResponsePartialFailureAndDelete404(t *testing.T) {
	raw := []byte(`{
		"errors": true,
		"items": [
			{"index": {"_index":"idx","_id":"1","status":201}},
			{"update": {"_index":"idx","_id":"2","status":409,"error":{"type":"version_conflict_engine_exception","reason":"conflict"}}},
			{"delete": {"_index":"idx","_id":"3","status":404,"error":{"type":"not_found","reason":"missing"}}}
		]
	}`)
	items, err := parseBulkResponse(raw, 3)
	if err != nil {
		t.Fatal(err)
	}
	if items[0].Error != nil || items[0].Status != 201 {
		t.Fatalf("item0: %+v", items[0])
	}
	if items[1].Error == nil || items[1].Status != 409 {
		t.Fatalf("item1 should fail: %+v", items[1])
	}
	if !strings.Contains(items[1].Error.Error(), "version_conflict_engine_exception") {
		t.Fatalf("unexpected item1 error: %v", items[1].Error)
	}
	if items[2].Error != nil || items[2].Status != 404 {
		t.Fatalf("delete 404 must be success: %+v", items[2])
	}
}

func TestEncodeBulkBodyRejectsInvalidOps(t *testing.T) {
	if _, err := encodeBulkBody([]WriteOp{{Kind: WriteIndex, Index: "", ID: "1"}}); err == nil {
		t.Fatal("expected missing index error")
	}
	if _, err := encodeBulkBody([]WriteOp{{Kind: WriteUpsertScript, Index: "i", ID: "1", Script: "x"}}); err == nil {
		t.Fatal("expected missing upsert error")
	}
}
