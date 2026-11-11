//go:build linux

package main

import (
	"errors"
	"fmt"
	"golang.org/x/sys/unix"
	"os"
	"strconv"
	"strings"
	"syscall"
	"time"
)

func protectParent() error {
	if os.Geteuid() != 0 || os.Getpid() != 1 {
		return errors.New("executor requires root in its private container PID namespace")
	}
	nnp, e := unix.PrctlRetInt(unix.PR_GET_NO_NEW_PRIVS, 0, 0, 0, 0)
	if e != nil || nnp != 1 {
		return errors.New("no-new-privileges is required")
	}
	return unix.Prctl(unix.PR_SET_DUMPABLE, 0, 0, 0, 0)
}
func childIdentity(a *syscall.SysProcAttr, ids ...int) {
	uid := engineGroup(ids)
	groups := []uint32{}
	if uid != engineUID {
		// Shared JMeter/RMI configuration remains readable through the legacy
		// group, while each task directory is granted only to its private group.
		groups = append(groups, engineGID)
	}
	a.Credential = &syscall.Credential{Uid: uint32(uid), Gid: uint32(uid), Groups: groups}
}

// A JSR223/OS sampler can detach from the JVM's process group. Sweep only the
// selected engine account in this supervisor's private PID namespace after each JVM
// exit and before admitting a replacement. Pidfds protect against PID reuse.
func reapEngine(ids ...int) error {
	uidToReap := engineGroup(ids)
	if os.Getpid() != 1 {
		return errors.New("refusing process cleanup outside the executor PID namespace")
	}
	until := time.Now().Add(4 * time.Second)
	for {
		entries, e := os.ReadDir("/proc")
		if e != nil {
			return e
		}
		remaining := false
		for _, entry := range entries {
			pid, e := strconv.Atoi(entry.Name())
			if e != nil || pid <= 1 {
				continue
			}
			fd, e := unix.PidfdOpen(pid, 0)
			if e == unix.ESRCH {
				continue
			}
			if e != nil {
				return fmt.Errorf("engine pidfd: %w", e)
			}
			raw, e := os.ReadFile("/proc/" + entry.Name() + "/status")
			if e != nil {
				unix.Close(fd)
				if os.IsNotExist(e) {
					continue
				}
				return e
			}
			state, uid, ppid := "", "", ""
			for _, line := range strings.Split(string(raw), "\n") {
				f := strings.Fields(line)
				if len(f) < 2 {
					continue
				}
				switch f[0] {
				case "Uid:":
					uid = f[1]
				case "State:":
					state = f[1]
				case "PPid:":
					ppid = f[1]
				}
			}
			if uid == strconv.Itoa(uidToReap) {
				if state == "Z" {
					if ppid == "1" {
						var st syscall.WaitStatus
						_, _ = syscall.Wait4(pid, &st, syscall.WNOHANG, nil)
					}
				} else {
					remaining = true
					e = unix.PidfdSendSignal(fd, unix.SIGKILL, nil, 0)
				}
			}
			unix.Close(fd)
			if e != nil && e != unix.ESRCH {
				return e
			}
		}
		if !remaining {
			return nil
		}
		if time.Now().After(until) {
			return errors.New("engine descendants remain alive")
		}
		time.Sleep(15 * time.Millisecond)
	}
}
