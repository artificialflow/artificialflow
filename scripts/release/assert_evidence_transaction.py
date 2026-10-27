#!/usr/bin/env python3
"""Deploy one workflow, complete its job, and require the query projection."""

import json
import os
import time
import urllib.error
import urllib.request

command = os.environ.get("COMMAND_URL", "http://127.0.0.1:18080").rstrip("/")
query = os.environ.get("QUERY_URL", "http://127.0.0.1:18081").rstrip("/")
suffix = time.strftime("%H%M%S")
job_type = f"evidence-task-{suffix}"
worker = f"evidence-worker-{suffix}"
bpmn = f"""<?xml version="1.0" encoding="UTF-8"?>
<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL"
  xmlns:artificialflow="http://artificialflow.io/schema/1.0/bpmn"
  id="Definitions_evidence" targetNamespace="http://bpmn.io/schema/bpmn">
  <bpmn:process id="evidence-{suffix}" name="Evidence" isExecutable="true">
    <bpmn:startEvent id="start1"><bpmn:outgoing>flow1</bpmn:outgoing></bpmn:startEvent>
    <bpmn:serviceTask id="task1" name="Do Work" artificialflow:taskType="{job_type}">
      <bpmn:incoming>flow1</bpmn:incoming><bpmn:outgoing>flow2</bpmn:outgoing>
    </bpmn:serviceTask>
    <bpmn:endEvent id="end1"><bpmn:incoming>flow2</bpmn:incoming></bpmn:endEvent>
    <bpmn:sequenceFlow id="flow1" sourceRef="start1" targetRef="task1"/>
    <bpmn:sequenceFlow id="flow2" sourceRef="task1" targetRef="end1"/>
  </bpmn:process>
</bpmn:definitions>"""


def call(method, url, body=None, content_type=None, ok=(200,)):
    data = body.encode() if isinstance(body, str) else body
    headers = {}
    if content_type:
        headers["Content-Type"] = content_type
    request = urllib.request.Request(url, data=data, headers=headers, method=method)
    try:
        with urllib.request.urlopen(request, timeout=20) as response:
            raw = response.read().decode()
            if response.status not in ok:
                raise SystemExit(f"{method} {url} -> {response.status} {raw[:300]}")
            return raw
    except urllib.error.HTTPError as error:
        raw = error.read().decode()
        raise SystemExit(f"{method} {url} -> {error.code} {raw[:300]}") from error


raw = call("POST", f"{command}/workflows", bpmn, "text/xml; charset=utf-8")
workflow_id = json.loads(raw)["id"]
raw = call(
    "POST",
    f"{command}/instances",
    json.dumps({"workflow_id": workflow_id, "context": {"evidence": suffix}}),
    "application/json",
)
instance_id = str(json.loads(raw)["id"])
deadline = time.time() + 60
job_key = None
while time.time() < deadline:
    raw = call(
        "POST",
        f"{command}/jobs/activate",
        json.dumps(
            {
                "type": job_type,
                "worker": worker,
                "maxJobs": 1,
                "lockDurationMs": 30000,
            }
        ),
        "application/json",
    )
    parsed = json.loads(raw)
    jobs = parsed if isinstance(parsed, list) else parsed.get("jobs", [])
    if jobs:
        job_key = jobs[0].get("key") or jobs[0].get("id")
        break
    time.sleep(1)
if not job_key:
    raise SystemExit(f"job for {instance_id} was not activated")
call(
    "POST",
    f"{command}/jobs/{job_key}/complete",
    json.dumps({"worker": worker, "variables": {"result": "ok"}}),
    "application/json",
    ok=(200, 204),
)
deadline = time.time() + 30
seen = None
while time.time() < deadline:
    try:
        raw = call("GET", f"{query}/instances/{instance_id}")
    except SystemExit:
        time.sleep(1)
        continue
    seen = json.loads(raw)
    if seen.get("status") == "COMPLETED":
        break
    time.sleep(1)
if not seen or seen.get("status") != "COMPLETED":
    raise SystemExit(f"instance {instance_id} not completed in query: {seen}")
print(f"assert ok instance={instance_id} workflow={workflow_id} status={seen['status']}")
