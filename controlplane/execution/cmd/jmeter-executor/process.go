package main

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"pressurecooker/execution/protocol"
)

type process struct {
	cmd  *exec.Cmd
	done chan struct{}
	err  error
}

func launch(c config, argv []string, dir, console string) (*process, error) {
	if e := directory(dir); e != nil {
		return nil, e
	}
	if i, e := os.Lstat(console); e == nil && !i.Mode().IsRegular() {
		return nil, errors.New("unsafe console log")
	}
	f, e := os.OpenFile(console, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if e != nil {
		return nil, e
	}
	defer f.Close()
	cmd := exec.Command(c.java, argv...)
	cmd.SysProcAttr = childAttrs()
	if c.isolated {
		if e = reapEngine(); e != nil {
			return nil, e
		}
		childIdentity(cmd.SysProcAttr)
		cmd.Env = []string{"PATH=/usr/bin:/bin", "LANG=C.UTF-8", "HOME=/tmp", "TMPDIR=/tmp"}
	}
	cmd.Dir = dir
	cmd.Stdout = f
	cmd.Stderr = f
	if e = cmd.Start(); e != nil {
		return nil, e
	}
	p := &process{cmd: cmd, done: make(chan struct{})}
	go func() {
		p.err = cmd.Wait()
		// A sampler may fork children. The private process group cannot outlive
		// its JMeter leader, including when that leader exits on SIGTERM.
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		if c.isolated {
			if e := reapEngine(); e != nil {
				p.err = e
			}
		}
		close(p.done)
	}()
	return p, nil
}
func (p *process) alive() bool {
	if p == nil {
		return false
	}
	select {
	case <-p.done:
		return false
	default:
		return true
	}
}
func (p *process) kill() {
	if !p.alive() {
		return
	}
	syscall.Kill(-p.cmd.Process.Pid, syscall.SIGTERM)
	select {
	case <-p.done:
		return
	case <-time.After(2 * time.Second):
		syscall.Kill(-p.cmd.Process.Pid, syscall.SIGKILL)
		<-p.done
	}
}
func (c config) baseArgs() []string {
	a := append([]string{}, c.heap...)
	return append(a, "-Djava.awt.headless=true", "-Duser.home=/tmp", "-Dpressurecooker.capture.tasks_root="+filepath.Join(c.work, "tasks"), "-Djava.rmi.server.hostname="+c.id, "-jar", filepath.Join(c.home, "bin/ApacheJMeter.jar"), "-d", c.home, "-q", c.common, "-q", c.rmi, "-Jclient.rmi.localport=0")
}
func (s *service) runProcess(ctx context.Context, t protocol.Task, raw string) (code int, runErr error) {
	base := s.taskDir(t.TaskID)
	f := filepath.Join(base, "task.properties")
	hosts := make([]string, len(t.Workers))
	for i, w := range t.Workers {
		hosts[i] = w.RMIHost
	}

	if s.c.isolated {
		for _, p := range []string{filepath.Join(base, "plan.jmx"), f} {
			if e := s.grantInput(t.TaskID, p); e != nil {
				return -1, e
			}
		}
		for _, d := range t.Datasets {
			if e := s.grantInput(t.TaskID, s.csvPath(t.TaskID, d.NodeID)); e != nil {
				return -1, e
			}
		}
	}
	out, e := s.outputDirectory(t.TaskID, raw)
	if e != nil {
		return -1, e
	}
	defer func() {
		if e := s.collectOutputs(t.TaskID, raw); e != nil {
			runErr = e
		}
	}()
	args := append(s.c.baseArgs(), "-q", f, "-G"+f, "-n", "-t", filepath.Join(base, "plan.jmx"), "-R", strings.Join(hosts, ","), "-l", filepath.Join(out, "results.jtl"), "-j", filepath.Join(out, "jmeter.log"), "-Jjmeterengine.nongui.port=4445", "-Jjmeterengine.nongui.maxport=4445", "-Jjmeterengine.remote.system.exit=false")
	if e := ctx.Err(); e != nil {
		return -1, e
	}
	p, e := launch(s.c, args, base, filepath.Join(raw, "console.log"))
	if e != nil {
		return -1, e
	}
	select {
	case <-ctx.Done():
		p.kill()
		return -1, ctx.Err()
	case <-p.done:
		return p.cmd.ProcessState.ExitCode(), p.err
	}
}
