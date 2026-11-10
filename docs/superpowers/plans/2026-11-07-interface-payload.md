# Interface Payload Implementation Plan

> **For agentic workers:** Use executing-plans for owned tasks, coordinate independent domains with dispatching-parallel-agents. Preserve all existing changes.

**Goal:** Clicking an interface shows actual bounded request/response/assertion evidence, then deploy and verify.

**Architecture:** Add bounded worker sample receipts using the official JMeter Groovy listener; parse available CSV evidence; publish immutable report-scoped samples and fetch only on interface drilldown.

**Tech Stack:** Go execution/manager/reporting; Vue; official JMeter/Groovy.

**Spec:** docs/superpowers/specs/2026-11-07-interface-payload-design.md

## Global Constraints

- Preserve unrelated/untracked work and historical report versions; no business load test.
- Runtime secrets/receipts stay under runtime and are never printed or committed.
- Keep existing stats and failed-only sorting semantics. No fabricated history, unknown assertions or response data.
- API only reads fixed report samples; project/node/step isolation. Plain-text rendering and bounded redacted payloads.

## Review Focus

- JMeter listener sees results after assertions and before distributed stripping; test official runtime.
- Missing versus captured-empty bodies/zero assertions remain distinct.
- Secret masking happens before clipping, including multiline JSON/headers and known credentials.
- A stale worker generation or late UI response cannot attach evidence to a different task/interface.
- Encoded total budgets hold for UTF-8, escaping, many nodes and large payloads.

### Task 1: Executor sampling (executor agent)

Files: execution/protocol/protocol.go and cmd/jmeter-executor/*.go, new embedded Groovy collector if needed. Own this domain only.

- [ ] Add protocol.RequestAssertion and RequestSample (nullable strings); add optional NodeEvidence request fields per spec. Write failing ownership/budget/injection tests first.
- [ ] Inject one cached official JSR223 listener into the downloaded plan copy. Per worker label+success keep first sample, at most64/128KiB. Capture actual method/URL/samplerData/response/assertions with honest nulls and truncation. Preserve JMX bytes except new listener and current CSV patches.
- [ ] Initialize a task/attempt-specific writable capture file at prepare; collect only exact owner/generation after JVM stop. Preserve receipts/retries, enforce total evidence bounds and old-agent compatibility.
- [ ] Run executor tests and official JMeter synthetic validation, no business target.

### Task 2: Reporting evidence parsing (parser agent)

Files: manager-go/reporting/*.go only. Own no manager root/protocol/frontend files.

- [ ] Add reporting.RequestSample and RequestAssertion matching spec; Analysis.RequestSamples. ParserVersion1.4.0. Write failing evidence tests.
- [ ] Parse optional URL/url, method, samplerData/requestParams/request_params, responseData/response_body, assertionFailureMessage/failureMessage and optional assertionResults JSON from CSV. Choose bounded first success/failure per node/step. A failure message proves only the listed failed assertion; no assertion records is unknown.
- [ ] Export StableStepKey(kind,label) and SanitizeRequestSample(sample,redactor) helper that derives key before redaction if absent and safely masks secrets before caps. Keep existing generic report redactor behavior unchanged.
- [ ] Export MergeRequestSamples(analysis *Analysis, samples []RequestSample, redact func(string)string) to match only existing step/node identities, prefer richer executor evidence over CSV metadata, enforce64/node and128KiB/node and2MiB total. Preserve node identities after redaction. Return captured evidence in Analysis.RequestSamples; incomplete flags in sample.Truncated and Analysis.RequestSamplesTruncated.
- [ ] Test null/empty/unknown/pass/fail assertions, secret masks and limits; run reporting suite.

### Task 3: Frontend payload view (frontend agent)

Files: frontend/src/reports/NodeExecutionDetails.vue plus tests; may create focused child RequestSampleDetails.vue+test.

- [ ] Write tests first. Add lazy `/samples` request only when evidenceStep exists, no failed_only/sort inherited. State and request cancellation follow existing scope.
- [ ] Render request URL/method, parameters, response code/body, assertions in shared click panel. Multiple actual records selectable by node/time/status. Distinguish null/empty, unknown assertions/no assertions/pass/fail and truncation/legacy.
- [ ] Maintain list filters/sort/page, focus/return behavior and mobile confined scrolling; pure text/JSON formatting, no HTML execution.
- [ ] Run focused and full frontend tests/build; freeze changes.

### Task 4: Manager projection, integration and deployment (root)

Files: manager root pressure_report_*.go, docs and runtime evidence.

- [ ] Add failing integration/API tests then validate/sanitize callback samples; merge with parsed metadata. Store `request_samples`, `request_samples_version`, `request_samples_truncated` on immutable report. Exclude payloads from summary/public/list/compare and expose authenticated scoped samples page only.
- [ ] Verify project/node/step/pagination/legacy safety with real isolated MySQL fixtures. Update PRESSURE-REPORTS.md; inspect all domain changes and resolve issues.
- [ ] Build current sources and frontend, preflight idle queue, guarded rollout of manager/executors only as needed; preserve other services and rollback.
- [ ] Validate live history empty/missing states plus synthetic successful/failed/asserted request evidence in actual page, desktop/mobile, zero console/API errors, source/container health receipts. Stop temporary local services.
