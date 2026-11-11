//go:build linux

package main

import (
	"encoding/csv"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// Run ONLY as PID 1 in a disposable --network none Linux container with an
// empty /fixture tmpfs and an existing official JMeter distribution. Every
// protected file this test reads is freshly created here and contains "dummy".
func TestLinuxSubmittedJMXCannotReadSupervisor(t *testing.T) {
	if os.Getenv("JMETER_DUMMY_ISOLATION_TEST") != "1" {
		t.Skip("requires disposable Linux dummy fixture")
	}
	if e := protectParent(); e != nil {
		t.Fatal(e)
	}
	root := "/fixture"
	entries, e := os.ReadDir(root)
	if e != nil || len(entries) != 0 {
		t.Fatal("requires empty fixture tmpfs")
	}
	c := config{isolated: true, work: root, results: root + "/tasks", state: root + "/state", id: "controller-a", java: filepath.Join(os.Getenv("JAVA_HOME"), "bin/java"), home: os.Getenv("JMETER_HOME"), common: root + "/common.properties", rmi: root + "/rmi/rmi.properties", heap: []string{"-Xms32m", "-Xmx256m"}}
	s := &service{c: c}
	base := s.taskDir("current")
	raw := filepath.Join(base, "raw")
	for _, p := range []string{c.state, c.results, raw, root + "/platform", root + "/rmi"} {
		if e = directory(p); e != nil {
			t.Fatal(e)
		}
	}
	if e = c.protectPaths(); e != nil {
		t.Fatal(e)
	}
	// Outer secret directory is searchable so the test exercises the actual
	// host-owned bind-file mode as well as journal directory isolation.
	if e = os.Chmod(root+"/platform", 0755); e != nil {
		t.Fatal(e)
	}
	for _, name := range []string{"agent-token", "redis-password", "minio-access-key", "minio-secret-key"} {
		p := root + "/platform/" + name
		if e = writeFile(p, []byte("dummy")); e != nil {
			t.Fatal(e)
		}
		if e = os.Chown(p, 1000, 1000); e != nil {
			t.Fatal(e)
		}
	}
	bad := root + "/platform/redis-password"
	info, _ := os.Stat(bad)
	if !privateSecret(info) {
		t.Fatal("host-owned private secret rejected")
	}
	if e = os.Chown(bad, 0, 0); e != nil {
		t.Fatal(e)
	}
	if e = os.Chmod(bad, 0644); e != nil {
		t.Fatal(e)
	}
	info, _ = os.Stat(bad)
	if privateSecret(info) {
		t.Fatal("world-readable credential accepted")
	}
	if e = os.Chmod(bad, 0600); e != nil {
		t.Fatal(e)
	}
	if e = os.Chown(bad, 1000, 1000); e != nil {
		t.Fatal(e)
	}
	if e = writeFile(c.state+"/journal", []byte("dummy")); e != nil {
		t.Fatal(e)
	}
	for _, p := range []string{c.rmi, root + "/rmi/rmi_keystore.jks", c.common} {
		if e = writeFile(p, []byte("#dummy\n")); e != nil {
			t.Fatal(e)
		}
		if e = grant(p, false, 0440); e != nil {
			t.Fatal(e)
		}
	}
	if e = grant(root+"/rmi", true, 0750); e != nil {
		t.Fatal(e)
	}
	plan := filepath.Join(base, "plan.jmx")
	prop := filepath.Join(base, "task.properties")
	csvp := s.csvPath("current", "node")
	for p, v := range map[string]string{plan: probeJMX, prop: "dummy.allowed=visible\n", csvp: "id\n1\n"} {
		if e = writeFile(p, []byte(v)); e != nil {
			t.Fatal(e)
		}
		if e = s.grantInput("current", p); e != nil {
			t.Fatal(e)
		}
	}
	previous := filepath.Join(s.taskDir("previous"), "task.properties")
	if e = writeFile(previous, []byte("dummy")); e != nil {
		t.Fatal(e)
	}
	if e = s.grantInput("previous", previous); e != nil {
		t.Fatal(e)
	}
	if e = s.seal("previous"); e != nil {
		t.Fatal(e)
	}
	out, e := s.outputDirectory("current", raw)
	if e != nil {
		t.Fatal(e)
	}
	if !filepath.IsAbs(c.java) {
		c.java, e = exec.LookPath("java")
		if e != nil {
			t.Fatal(e)
		}
	}
	args := append(c.baseArgs(), "-q", prop, "-n", "-t", plan, "-l", out+"/results.jtl", "-j", out+"/jmeter.log", "-Jjmeter.save.saveservice.response_message=true")
	defer func() {
		if t.Failed() {
			for _, name := range rawNames {
				data, _ := os.ReadFile(filepath.Join(raw, name))
				t.Log(name, string(data))
			}
		}
	}()
	p, e := launch(c, args, base, raw+"/console.log")
	if e != nil {
		t.Fatal(e)
	}
	select {
	case <-p.done:
	case <-time.After(45 * time.Second):
		p.kill()
		t.Fatal("dummy JMeter timed out")
	}
	// Logs belong only to this isolated synthetic JMX, never to live services.
	if t.Failed() || p.err != nil {
		for _, name := range []string{"console.log", "jmeter.log"} {
			b, _ := os.ReadFile(raw + "/" + name)
			t.Log(string(b))
		}
		t.Fatalf("JMeter exit %v", p.err)
	}
	if e = s.collectOutputs("current", raw); e != nil {
		t.Fatal(e)
	}
	f, e := os.Open(raw + "/results.jtl")
	if e != nil {
		t.Fatal(e)
	}
	defer f.Close()
	r := csv.NewReader(f)
	header, e := r.Read()
	if e != nil {
		t.Fatal(e)
	}
	row, e := r.Read()
	if e != nil {
		t.Fatalf("missing dummy sample: %v", e)
	}
	sample := map[string]string{}
	for i, k := range header {
		sample[k] = row[i]
	}
	if sample["success"] != "true" || !strings.HasPrefix(sample["responseMessage"], "isolation-ok-") {
		t.Fatalf("dummy JMX did not pass isolation checks: %q", sample["responseMessage"])
	}
	pid, e := strconv.Atoi(strings.TrimPrefix(sample["responseMessage"], "isolation-ok-"))
	if e != nil || pid < 2 {
		t.Fatal("invalid detached probe pid")
	}
	if _, e = os.Stat("/proc/" + strconv.Itoa(pid)); !os.IsNotExist(e) {
		t.Fatal("detached sampler process survived JVM termination")
	}
	if _, e = r.Read(); e != io.EOF {
		t.Fatal("unexpected extra sample")
	}
	if e = os.Symlink("/fixture/platform/agent-token", filepath.Join(out, "results.jtl")); e != nil {
		t.Fatal(e)
	}
	if e = s.collectOutputs("current", raw); e != errInvalidOutput {
		t.Fatal("collector accepted a symbolic output")
	}
	if e = os.Link("/fixture/platform/agent-token", filepath.Join(out, "jmeter.log")); e != nil {
		t.Fatal(e)
	}
	if e = s.collectOutputs("current", raw); e != errInvalidOutput {
		t.Fatal("collector accepted a hardlinked output")
	}
	t.Log("Official JMeter local JSR223 sample passed: UID/GID 10001, no effective capabilities or supervisor environment; four dummy secrets, private journal, prior task, parent environ/mem/fd and log-path replacement denied; current plan/CSV/RMI/outputs accessible; detached process reaped.")
}

const probeJMX = `<?xml version="1.0" encoding="UTF-8"?>
<jmeterTestPlan version="1.2" properties="5.0" jmeter="5.6.3"><hashTree><TestPlan guiclass="TestPlanGui" testclass="TestPlan" testname="Dummy local isolation"><elementProp name="TestPlan.user_defined_variables" elementType="Arguments"><collectionProp name="Arguments.arguments"/></elementProp></TestPlan><hashTree><ThreadGroup guiclass="ThreadGroupGui" testclass="ThreadGroup" testname="One"><intProp name="ThreadGroup.num_threads">1</intProp><intProp name="ThreadGroup.ramp_time">1</intProp><elementProp name="ThreadGroup.main_controller" elementType="LoopController"><intProp name="LoopController.loops">1</intProp><boolProp name="LoopController.continue_forever">false</boolProp></elementProp></ThreadGroup><hashTree><JSR223Sampler guiclass="TestBeanGUI" testclass="JSR223Sampler" testname="local isolation"><stringProp name="scriptLanguage">groovy</stringProp><stringProp name="script"><![CDATA[
import java.nio.file.*
def denied={fn -> try{fn();return false}catch(AccessDeniedException e){return true}}
def status=Files.readString(Path.of("/proc/self/status"))
assert status.readLines().find{it.startsWith("Uid:")}.tokenize().drop(1).every{it=="10001"}
assert status.readLines().find{it.startsWith("Gid:")}.tokenize().drop(1).every{it=="10001"}
assert status.readLines().find{it.startsWith("Groups:")}.tokenize().size()==1
assert status.readLines().find{it.startsWith("CapEff:")}.endsWith("0000000000000000")
assert status.readLines().find{it.startsWith("NoNewPrivs:")}.tokenize().last()=="1"
assert System.getenv("JMETER_DUMMY_ISOLATION_TEST")==null
for(name in ["agent-token","redis-password","minio-access-key","minio-secret-key"]){assert denied{Files.newInputStream(Path.of("/fixture/platform/"+name)).close()}}
for(path in ["/fixture/state/journal","/fixture/tasks/previous/task.properties","/proc/1/environ","/proc/1/mem"]){assert denied{Files.newInputStream(Path.of(path)).close()}}
assert denied{Files.newDirectoryStream(Path.of("/proc/1/fd")).close()}
assert denied{Files.createSymbolicLink(Path.of("/fixture/tasks/current/raw/new.log"),Path.of("/fixture/platform/agent-token"))}
assert denied{Files.createFile(Path.of("/fixture/tasks/current/raw/jmeter.log"))}
assert denied{Files.writeString(Path.of("/fixture/tasks/current/task.properties"),"bad")}
assert props.getProperty("dummy.allowed")=="visible"
assert Files.readString(Path.of("/fixture/tasks/current/datasets/node/data.csv")).contains("1")
assert Files.readString(Path.of("/fixture/rmi/rmi_keystore.jks")).contains("dummy")
def detached=new ProcessBuilder("/usr/bin/setsid","/bin/sleep","90").start()
SampleResult.setResponseMessage("isolation-ok-"+detached.pid())
]]></stringProp></JSR223Sampler><hashTree/></hashTree></hashTree></hashTree></jmeterTestPlan>`

// Run as PID 1 in a disposable --network none Linux container. No JMeter or
// business assets are needed: shell children exercise the same launch/reaper
// and filesystem grants used by Core JVMs.
func TestLinuxConcurrentCoreRuntimeIsolation(t *testing.T) {
	if os.Getenv("JMETER_DUMMY_CORE_ISOLATION_TEST") != "1" {
		t.Skip("requires disposable Linux Core isolation fixture")
	}
	if err := protectParent(); err != nil {
		t.Fatal(err)
	}
	root := "/fixture/parallel"
	base := config{isolated: true, work: root, results: root + "/results", state: root + "/state", java: "/bin/sh"}
	for _, dir := range []string{root, base.results, base.state, filepath.Join(root, "tasks")} {
		if err := directory(dir); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Chmod(root, 0755); err != nil {
		t.Fatal(err)
	}
	if err := base.protectPaths(); err != nil {
		t.Fatal(err)
	}
	shared := filepath.Join(root, "common.properties")
	if err := writeFile(shared, []byte("dummy shared config\n")); err != nil {
		t.Fatal(err)
	}
	if err := grant(shared, false, 0440); err != nil {
		t.Fatal(err)
	}
	services := []*service{{c: base}, {c: base}}
	for i, s := range services {
		s.c.runtimeSlot = i + 1
		id := []string{"first", "second"}[i]
		if err := writeFile(filepath.Join(s.taskDir(id), "task.properties"), []byte("dummy own config\n")); err != nil {
			t.Fatal(err)
		}
		if err := s.grantInput(id, filepath.Join(s.taskDir(id), "task.properties")); err != nil {
			t.Fatal(err)
		}
	}
	var children []*process
	for i, s := range services {
		id := []string{"first", "second"}[i]
		peer := []string{"second", "first"}[i]
		raw := filepath.Join(base.results, id)
		if err := directory(raw); err != nil {
			t.Fatal(err)
		}
		out, err := s.outputDirectory(id, raw)
		if err != nil {
			t.Fatal(err)
		}
		// Each process can read shared config and its own input. Its peer's
		// input/output are inaccessible even though both JVMs are running.
		script := `set -eu
cat "$1" >/dev/null
cat "$2" >/dev/null
if cat "$3" >/dev/null 2>&1; then exit 21; fi
printf 'ready\n' > "$4"
setsid sleep 90 &
printf '%s\n' "$!" > "$5"
while :; do sleep 1; done`
		args := []string{"-c", script, "probe", shared, filepath.Join(s.taskDir(id), "task.properties"), filepath.Join(s.taskDir(peer), "task.properties"), filepath.Join(out, "results.jtl"), filepath.Join(out, "jmeter.log")}
		p, err := launch(s.c, args, s.taskDir(id), filepath.Join(raw, "console.log"))
		if err != nil {
			t.Fatal(err)
		}
		defer p.kill()
		children = append(children, p)
	}
	pids := []int{}
	for i, s := range services {
		id := []string{"first", "second"}[i]
		pidFile := filepath.Join(s.taskDir(id), "jvm-output", "jmeter.log")
		deadline := time.Now().Add(5 * time.Second)
		for {
			raw, err := os.ReadFile(pidFile)
			pid, parseErr := strconv.Atoi(strings.TrimSpace(string(raw)))
			if err == nil && parseErr == nil && pid > 1 {
				pids = append(pids, pid)
				break
			}
			if !children[i].alive() || time.Now().After(deadline) {
				log, _ := os.ReadFile(filepath.Join(base.results, id, "console.log"))
				t.Fatalf("isolated process failed to read only its granted inputs: %s", log)
			}
			time.Sleep(10 * time.Millisecond)
		}
	}
	children[0].kill()
	if _, err := os.Stat("/proc/" + strconv.Itoa(pids[0])); !os.IsNotExist(err) {
		t.Fatal("first task left a detached descendant")
	}
	if !children[1].alive() {
		t.Fatal("first task reaper killed the second task JVM")
	}
	if _, err := os.Stat("/proc/" + strconv.Itoa(pids[1])); err != nil {
		t.Fatal("first task reaper killed the second task descendant")
	}
	if err := services[0].collectOutputs("first", filepath.Join(base.results, "first")); err != nil {
		t.Fatal(err)
	}
	if err := services[0].seal("first"); err != nil {
		t.Fatal(err)
	}
	// Recovery may choose a different free slot, but collection must validate
	// files against the original journal identity rather than the new slot.
	children[1].kill()
	if err := services[0].collectOutputsAs("second", filepath.Join(base.results, "second"), 10003); err != nil {
		t.Fatal("recovery rejected the original runtime's output", err)
	}
	if _, err := os.Stat("/proc/" + strconv.Itoa(pids[1])); !os.IsNotExist(err) {
		t.Fatal("second task left a detached descendant")
	}
}
