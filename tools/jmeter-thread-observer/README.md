# Independent JMeter thread observations

This official JMeter lifecycle extension reads `JMeterContextService.getNumberOfThreads()` on a daemon every 1000 ms. It creates no JMeter worker threads, samplers, sample results, or JTL rows. Normal lifecycle start/end can add actual readings. Forced JVM termination adds no final reading or `END` record.

Build with JDK 8 or newer and the official JMeter 5.6.3 distribution:

```sh
JAVA_HOME=/path/to/jdk bash tools/jmeter-thread-observer/build.sh /path/to/apache-jmeter-5.6.3
```

Install `.build/pressurecooker-thread-observer.jar` into `JMETER_HOME/lib/ext/` **on the controller and every worker**. The controller must deserialize the same custom test element before sending the plan to workers. Restart runtime JVMs after changing the jar. The executor injects the test element; users do not add a monitoring sampler.

The executor prepares a local, read-only `binding.properties` below the supervisor-provided `pressurecooker.capture.tasks_root`. The worker JVM receives its node ID and generation as supervisor system properties. The listener requires task, attempt, owner hash, JVM node/generation, basename, device, inode, single-link count, and exact initial CSV header to match before opening the already-created file. A controller without a worker identity collects nothing. Engine directory write permission is not required.

The source is bounded to 4 MiB and 90000 points per node. EOF without `END` remains incomplete. The supervisor copies and validates the source after process exit and before generation reset, then uploads it under an immutable task/attempt/node/generation/SHA key. Owner hashes and lease tokens are absent from uploaded CSV data.

Run only the synthetic official-runtime fixtures (one sleeping sampler; no network requests):

```sh
cd controlplane/execution
JAVA_HOME=/path/to/jdk JMETER_THREAD_TEST_JAVA=/path/to/jdk/bin/java \
  JMETER_THREAD_OBSERVATIONS_IT=1 go test ./cmd/jmeter-executor -run '^TestThreadObservationsOfficialRuntime' -count=1 -v
```
