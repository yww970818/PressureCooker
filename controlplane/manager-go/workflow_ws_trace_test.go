package main

import (
	"bufio"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func localWsTraceJava(t *testing.T) (string, string) {
	t.Helper()
	home, java := os.Getenv("JMETER_HOME"), os.Getenv("JAVA_HOME")
	if home == "" || java == "" {
		t.Skip("set JMETER_HOME and JAVA_HOME for local-only native WS checks")
	}
	return home, java
}
func readWsTraceRecords(t *testing.T, raw string) []map[string]any {
	t.Helper()
	var records []map[string]any
	scanner := bufio.NewScanner(strings.NewReader(raw))
	for scanner.Scan() {
		line := scanner.Text()
		i := strings.Index(line, "PC_WS_EVENT ")
		if i < 0 {
			continue
		}
		var record map[string]any
		if err := json.Unmarshal([]byte(line[i+len("PC_WS_EVENT "):]), &record); err != nil {
			t.Fatal("invalid trace JSON")
		}
		records = append(records, record)
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	return records
}

func TestWorkflowWebSocketTraceIsOptInBoundedMetadata(t *testing.T) {
	home, java := localWsTraceJava(t)
	dir := t.TempDir()
	script := strings.Split(workflowWebSocketRuntime, "\nMap config=PCWorkflowWs.parse")[0] + wsTraceChecks
	config := `<Configuration status="OFF"><Appenders><Console name="out" target="SYSTEM_OUT"><PatternLayout pattern="%msg%n"/></Console></Appenders><Loggers><Root level="info"><AppenderRef ref="out"/></Root></Loggers></Configuration>`
	for name, body := range map[string]string{"trace.groovy": script, "log4j2.xml": config} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
	}
	cp := filepath.Join(home, "lib", "*") + string(os.PathListSeparator) + filepath.Join(home, "lib", "ext", "*") + string(os.PathListSeparator) + filepath.Join(home, "bin", "ApacheJMeter.jar")
	out, err := exec.Command(filepath.Join(java, "bin", "java"), "-Dlog4j.configurationFile="+filepath.Join(dir, "log4j2.xml"), "-cp", cp, "groovy.ui.GroovyMain", filepath.Join(dir, "trace.groovy")).CombinedOutput()
	if err != nil {
		t.Fatalf("offline native WS trace: %v\n%s", err, strings.ReplaceAll(string(out), "PRIVATE_TRACE_FIXTURE", "[fixture]"))
	}
	if strings.Contains(string(out), "PRIVATE_TRACE_FIXTURE") {
		t.Fatal("trace exposed user data")
	}
	records := readWsTraceRecords(t, string(out))
	groups := map[string][]map[string]any{}
	for _, record := range records {
		id, ok := record["trace_id"].(string)
		if !ok || len(id) != 32 || record["time_ms"] == nil || record["elapsed_ms"] == nil {
			t.Fatal("trace lacks connection correlation", record)
		}
		for key := range record {
			if !contains([]string{"trace_id", "time_ms", "elapsed_ms", "phase", "ser", "message_id", "sequence", "pending", "boundary"}, key) {
				t.Fatal("trace exposed application metadata", key)
			}
		}
		groups[id] = append(groups[id], record)
	}
	if len(groups) != 2 {
		t.Fatal("disabled trace emitted or enabled trace missing", len(groups))
	}
	normal, capped := false, false
	for _, group := range groups {
		phases := map[string]int{}
		for _, record := range group {
			phases[record["phase"].(string)]++
		}
		if phases["send"] > 0 {
			normal = true
			if len(group) != 7 || phases["enqueue"] != 2 || phases["wait"] != 2 || phases["match"] != 1 || phases["timeout"] != 1 {
				t.Fatal("send/match/timeout evidence missing", phases)
			}
		}
		if phases["trace_limit"] > 0 {
			capped = true
			if len(group) != 257 || phases["trace_limit"] != 1 {
				t.Fatal("trace cap not enforced", len(group), phases)
			}
		}
	}
	if !normal || !capped {
		t.Fatal("normal or capped trace missing")
	}
}

const wsTraceChecks = `
def connection={String key,Properties settings->
 def c=new PCWsConnection(key,false,100,settings)
 c.socket=[sendText:{text,last->CompletableFuture.completedFuture(null)},abort:{->}] as WebSocket
 c
}
def privateValue='PRIVATE_TRACE_FIXTURE'
[new Properties(),new Properties().tap{setProperty('websocket_trace_events','false')}].each { settings->
 def c=connection(privateValue,settings)
 c.enqueue([raw:privateValue,frame:[kind:'notice',text:privateValue]])
 c.receive([mode:'json',rules:['$.frame.kind':'notice']],-1L,100L)
 c.dispose(false)
}
def enabled=new Properties();enabled.setProperty('websocket_trace_events','true')
def c=connection(privateValue,enabled)
c.variables=[credential:privateValue]
c.send(JsonOutput.toJson([kind:'request',id:'{{message_id}}',credential:privateValue]),false)
c.enqueue([raw:privateValue,frame:[kind:'approval',status:'rejected',text:privateValue]])
c.enqueue([raw:privateValue,frame:[kind:'reply',text:privateValue]])
c.receive([mode:'json',rules:['$.frame.kind':'reply']],-1L,100L)
try {c.receive([mode:'json',rules:['$.frame.kind':'approval','$.frame.status':'accepted']],-1L,20L);throw new IllegalStateException('rejected response passed')}catch(TimeoutException expected){}
c.dispose(false)
def capped=connection(privateValue,enabled)
300.times {capped.enqueue([raw:privateValue,frame:[kind:'notice',text:privateValue]])}
capped.dispose(false)
println 'offline WS trace checks completed'
`
