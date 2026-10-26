# Decision tables (DMN-lite)

ArtificialFlow evaluates JSON decision tables and a strict decision-table XML
subset referenced by `artificialflow:decisionRef` on business-rule tasks.

## Supported decision-table XML subset

The XML importer accepts one DMN 1.3 `decision` containing one
`decisionTable`. Supported elements are `input`/`inputExpression`,
named `output`, `rule`, `inputEntry`, `outputEntry`, and `text`. Only the
`FIRST` hit policy is supported. Input entries accept `-`, scalar equality,
and the unary comparisons `=`, `!=`, `>`, `>=`, `<`, and `<=`. Output entries
must be JSON scalars (quoted strings, numbers, booleans, or `null`).

Other decision kinds, hit policies, FEEL functions, ranges, lists, contexts,
and multi-decision documents are rejected during deployment with an explicit
unsupported-construct error. They are never silently approximated.

## UI upload

Open **Decisions** in the ArtificialFlow console (`artificialflowadmin` or `artificialflowbusiness`). Upload a
JSON or DMN XML file, or paste the decision table, then **Deploy**. Use
**Evaluate** to dry-run inputs without starting a process.

## Deploy (API)

```bash
curl -X POST "$API/decisions" \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{
    "decision_id": "invoice_decision",
    "name": "Invoice routing",
    "resource": "{\"id\":\"invoice_decision\",\"hitPolicy\":\"FIRST\",\"rules\":[{\"when\":{\"amount\":{\"op\":\"gt\",\"value\":1000}},\"then\":{\"result\":\"manager\"}},{\"when\":{},\"then\":{\"result\":\"auto\"}}]}"
  }'
```

```bash
curl -H "Authorization: Bearer $TOKEN" "$API/decisions"
```

## Evaluate

```bash
curl -X POST "$API/decisions/invoice_decision/evaluate" \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"inputs":{"amount":1500}}'
```

Outputs are written to the instance context (`result_variable`, default `decisionResult`) and flattened into top-level keys.
