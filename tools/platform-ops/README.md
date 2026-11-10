# Platform operational helpers

This standard-library-only Go module replaces the platform's former Python helpers.
Run commands through `bin/platform-ops.sh`, which selects
`vendor/pressurecooker-ops-OS-ARCH` or uses `GO` / an available Go compiler.
`bin/build-master.sh` builds Linux amd64 and native-host binaries; deployment
hosts use the binary and do not need a compiler.

- `bin/fetch-node.sh`: verify the official version and SHA256 in
  `vendor/node-source.json`, reuse the verified cache, or atomically download it.
- `bin/check-smoke.sh DIRECTORY`: validate a standalone two-worker JTL/HTML
  result and atomically save `smoke-validation.json`.
- `bin/platform-smoke.sh --url URL --token-file FILE --plan FILE --project ID --cluster ID
  --environment ID`: explicitly submit the internal self-test on the selected registered cluster and
  verify five successful samples per selected worker and its raw artifacts. This command creates an actual internal test task;
  it is never part of the offline test suite.
- `bin/platform-ops.sh prepare-secrets`: create missing private platform keys;
  existing regular files retain their contents and are set to mode 0600.
- `bin/platform-ops.sh json-field KEY...`: read a string field from standard
  input using exact object keys, including hyphenated service names.

The obsolete HTTP scheduler/fault verifier was removed. It was not the current
Redis runner acceptance entry point. The current self-test retains its existing
raw-only assertions; it is not a report-generation acceptance suite.

Run `GO=/path/to/go bin/test-controlplane.sh` for the platform baseline, or
`go test ./...` in this module for only file and local HTTP fixtures. No fixture
connects to a deployment or starts JMeter.
