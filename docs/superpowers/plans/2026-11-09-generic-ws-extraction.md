# Generic WebSocket JSON Extraction Implementation Plan

> **For agentic workers:** Use superpowers:executing-plans inline. Preserve the shared uncommitted workspace and do not create commits.

**Goal:** Support explicitly configured nested JSON decoding and response matching, then install project-owned business rules and a display plugin.

**Architecture:** Extend the existing pure frontend JSON extractor; transport and default empty rules remain unchanged. Rules optionally decode JSON strings, select/filter candidate roots in stages, and expose correlated outputs. Business paths and parser code remain project resources.

**Tech Stack:** Vue, existing JSON path helper and Scalar editor; Go manager/MCP persist opaque collection/plugin resources.

**Spec:** User approved generic nested JSON decoding and response matching, with project-specific automatic extraction and plugin configuration, on 2026-11-09.

## Global Constraints

- No business defaults, built-in business parsers or implicit messages.
- Preserve existing rule behavior unless an option is explicitly configured.
- Only test added extraction/plugin behavior; no real business load.
- Preserve current data, historical versions, credentials, deployment identities and other contributors' changes.
- Use revision-checked MCP updates; do not overwrite concurrent changes.

## Design

- `decode_json: true` explicitly decodes strings while walking JSON paths; preserve large integer IDs and use bounded shared decoding.
- `selectors: [{path, match: [{path, operator, value}]}]` successively select object/array candidates. Each selector may also read up to three `alternate_paths` at the same level, retaining its parent candidate. Match operators are `equals`, `in`, `exists`, `starts_with`; values may explicitly reference current variables with `{{name}}`.
- Existing `path/variable` defines the primary output. `outputs: [{path,variable}]` adds up to seven correlated fields from the same selected candidate. Missing any output invalidates that candidate. Outputs may read the final candidate, its immediately preceding parent, or the packet.
- `on_unmatched: keep` preserves previous outputs when no source candidate matches. Default is `clear`, preserving prior behavior. A matching source missing an output clears the stale result.
- Selecting one of multiple candidates fills all its outputs atomically. Reconnect and rule changes clear runtime values as before.
- Rule editor exposes these generic options and local preview. Existing old/unknown configurations remain opaque and editable-supported rules retain all options.
- Project configuration uses these generic options for sessions, entrance selection, candidate questions and flow buttons. Project plugin parses and displays only, with raw view retained.

## Review Focus

- Nested double-encoded strings and integers above 2^53: preserve exact values.
- Interleaved unrelated packets: opted-in rules retain current valid results.
- Multiple candidates: no automatic pairing across separate fields.
- Missing dynamic match context, invalid paths and excessive decoding: never match accidentally or close transport.
- Existing collections and unknown rules: no implicit migration or rewrite.

### Task 1: Extraction engine and validation

**Files:** `ws-extraction-rules.js`, `ws-response-variables.js`, `websocket-session.js`; new `ws-nested-extraction.test.js`.

- [ ] Add failing tests for decoded selectors, correlated candidates, dynamic match values, unrelated replies and invalid options.
- [ ] Run new tests and confirm failures are caused by unsupported options.
- [ ] Implement bounded generic selection and matching; pass current connection context to extraction.
- [ ] Run new tests plus existing generic session tests.

### Task 2: Rule editor and documentation

**Files:** `WsExtractionRuleEditor.vue`, `docs/interface-plugins.md`; targeted editor tests.

- [ ] Add editor tests that configure selectors/conditions and exercise the actual local preview.
- [ ] Implement explicit advanced controls and correlated output editing.
- [ ] Run targeted UI tests and build frontend.

### Task 3: Project assets, deployment and focused verification

- [ ] Validate project-owned parser against sent, received, candidate, flow, session closure and invalid frames.
- [ ] Freeze and rebuild current source, then update manager only with existing deployment guards.
- [ ] Create/install parser via MCP and revision-update only the project's WebSocket extraction/plugin fields; read back and compare all unrelated fields.
- [ ] Verify deployed editor/local previews, plugin sandbox and an isolated replay connection; no business login or task execution.
- [ ] Record receipts, preserve history and remove temporary resources.
