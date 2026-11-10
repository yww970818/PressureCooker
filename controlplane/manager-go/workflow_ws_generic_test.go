package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Frames that look like an application protocol must still be delivered whole;
// user variables remain ordinary input and transport maintenance never sends replies.
func TestWorkflowWebSocketTreatsApplicationFramesAsOpaqueJSON(t *testing.T) {
	home, java := localWsTraceJava(t)
	dir := t.TempDir()
	script := strings.Split(workflowWebSocketRuntime, "\nMap config=PCWorkflowWs.parse")[0] + `
 def writes=Collections.synchronizedList([])
 def c=new PCWsConnection('opaque',true,100,new Properties())
 c.socket=[sendText:{text,last->writes.add(text);CompletableFuture.completedFuture(null)},abort:{->}] as WebSocket
 c.variables=[session_id:'custom-session',session_token:'custom-token',staff_id:'custom-staff',client_ip:'custom-client',profile_update_time:'custom-version']
 assert c.values(false).session_id=='custom-session'
 assert c.values(false).profile_update_time=='custom-version'
 def legacyFrames=[
  [sid:4,cid:5,code:200],
  [sid:2,cid:3,code:200,r:[['103':'remote-ip']]],
  [sid:4,cid:1,r:[9,[headerPacket:[sid:101,cid:2],body:[['10':JsonOutput.toJson([cmd:2,code:200,sessionid:7])]]]]]
 ]
 legacyFrames.each { body ->
  String raw='3:::'+JsonOutput.toJson(body)
  c.accept(raw)
  def result=c.receive([mode:'any'],-1L,100L)
  assert result.raw==raw && result.frame instanceof Map
  assert !result.containsKey('event') && !result.containsKey('message_session_id')
 }
 assert c.values(false).session_id=='custom-session' && c.values(false).client_ip=='custom-client'
 assert writes.isEmpty()
 c.accept('2::'); assert writes==['2::']
 writes.clear()
 c.startHeartbeat([heartbeatEnabled:true,heartbeatIntervalMs:'10',heartbeatMessage:'{}'])
 Thread.sleep(250)
 c.check()
 assert writes.size()>2 && writes.every { it=='{}' }
 c.dispose(false)
 `
	path := filepath.Join(dir, "generic.groovy")
	if err := os.WriteFile(path, []byte(script), 0600); err != nil {
		t.Fatal(err)
	}
	cp := filepath.Join(home, "lib", "*") + string(os.PathListSeparator) + filepath.Join(home, "lib", "ext", "*") + string(os.PathListSeparator) + filepath.Join(home, "bin", "ApacheJMeter.jar")
	out, err := exec.Command(filepath.Join(java, "bin", "java"), "-cp", cp, "groovy.ui.GroovyMain", path).CombinedOutput()
	if err != nil {
		t.Fatalf("generic WS runtime: %v\n%s", err, out)
	}
}
