package main

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"syscall"
)

const engineUID = 10001
const engineGID = 10001

// The engine can traverse explicitly granted directories but cannot replace
// their entries. Root opens only its own files for publishing and delivery.
func grant(p string, dir bool, mode os.FileMode) error {
	real, e := filepath.EvalSymlinks(p)
	if e != nil {
		return e
	}
	if real != p {
		return errors.New("untrusted engine path")
	}
	info, e := os.Lstat(p)
	if e != nil {
		return e
	}
	if info.IsDir() != dir || !dir && !info.Mode().IsRegular() {
		return errors.New("invalid engine path type")
	}
	if e = os.Chown(p, 0, engineGID); e != nil {
		return e
	}
	return os.Chmod(p, mode)
}
func engineDirectories(dir, root string) error {
	rel, e := filepath.Rel(root, dir)
	if e != nil || !filepath.IsLocal(rel) {
		return errors.New("engine path outside task root")
	}
	if dir != root {
		if e = engineDirectories(filepath.Dir(dir), root); e != nil {
			return e
		}
	}
	return grant(dir, true, 0710)
}
func (c config) protectPaths() error {
	// All ancestors remain controlled by the supervisor or trusted host operator.
	for _, p := range []string{c.work, c.results, c.state} {
		for d := p; d != "/"; d = filepath.Dir(d) {
			st, e := os.Lstat(d)
			if e != nil {
				if os.IsNotExist(e) {
					continue
				}
				return e
			}
			owner, ok := st.Sys().(*syscall.Stat_t)
			if !ok || !st.IsDir() || owner.Uid == engineUID || st.Mode().Perm()&0002 != 0 || owner.Gid == engineGID && st.Mode().Perm()&0020 != 0 {
				return errors.New("engine-writable executor directory")
			}
		}
	}
	tasks := filepath.Join(c.work, "tasks")
	if e := grant(tasks, true, 0710); e != nil {
		return e
	}
	if e := grant(c.state, true, 0700); e != nil {
		return e
	}
	// No inherited permissions from an old pending callback become active on reboot.
	entries, e := os.ReadDir(tasks)
	if e != nil {
		return e
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			return errors.New("unexpected task root entry")
		}
		if e = grant(filepath.Join(tasks, entry.Name()), true, 0700); e != nil {
			return e
		}
	}
	return nil
}
func privateSecret(st os.FileInfo) bool {
	v, ok := st.Sys().(*syscall.Stat_t)
	return ok && v.Uid != engineUID && st.Mode().Perm()&0077 == 0 && st.Mode().IsRegular()
}
func (s *service) grantInput(id, p string) error {
	if !s.c.isolated {
		return nil
	}
	if e := engineDirectories(filepath.Dir(p), s.taskDir(id)); e != nil {
		return e
	}
	return grant(p, false, 0640)
}
func (s *service) seal(id string) error {
	if !s.c.isolated {
		return nil
	}
	p := s.taskDir(id)
	if _, e := os.Lstat(p); os.IsNotExist(e) {
		return nil
	}
	return grant(p, true, 0700)
}

var errInvalidOutput = errors.New("invalid JMeter output")

func (s *service) outputDirectory(id, raw string) (string, error) {
	if !s.c.isolated {
		return raw, nil
	}
	dir := filepath.Join(s.taskDir(id), "jvm-output")
	if e := os.Mkdir(dir, 0700); e != nil {
		return "", e
	}
	// JMeter writes its own CSV header only when it creates a new results file.
	// The parent of this writable directory is immutable to the engine.
	return dir, grant(dir, true, 0730)
}
func transferOutput(src, dst string) error {
	if e := os.Rename(src, dst); e == nil {
		return nil
	} else if !errors.Is(e, syscall.EXDEV) {
		return e
	}
	srcFile, e := openRegular(src)
	if e != nil {
		return e
	}
	defer srcFile.Close()
	temp, e := os.CreateTemp(filepath.Dir(dst), ".output-")
	if e != nil {
		return e
	}
	defer os.Remove(temp.Name())
	_, e = io.Copy(temp, srcFile)
	if e == nil {
		e = temp.Sync()
	}
	ce := temp.Close()
	if e != nil {
		return e
	}
	if ce != nil {
		return ce
	}
	if e = os.Rename(temp.Name(), dst); e != nil {
		return e
	}
	return os.Remove(src)
}
func (s *service) collectOutputs(id, raw string) error {
	if !s.c.isolated {
		return nil
	}
	dir := filepath.Join(s.taskDir(id), "jvm-output")
	if _, e := os.Lstat(dir); os.IsNotExist(e) {
		return nil
	}
	// Revocation under a root-owned, non-writable parent closes path-swap races.
	// A detached sampler cannot populate a future attempt; the UID sweep finishes
	// before a replacement JVM is admitted. Ignore every non-contract filename.
	if e := grant(dir, true, 0700); e != nil {
		return e
	}
	var invalid bool
	for _, name := range []string{"results.jtl", "jmeter.log"} {
		src := filepath.Join(dir, name)
		st, e := os.Lstat(src)
		if os.IsNotExist(e) {
			continue
		}
		if e != nil {
			return e
		}
		owner, ok := st.Sys().(*syscall.Stat_t)
		if !ok || !st.Mode().IsRegular() || owner.Uid != engineUID || owner.Nlink != 1 {
			invalid = true
			if e = os.RemoveAll(src); e != nil {
				return e
			}
			continue
		}
		dst := filepath.Join(raw, name)
		if e = transferOutput(src, dst); e != nil {
			return e
		}
		if e = os.Chown(dst, 0, 0); e != nil {
			return e
		}
		if e = os.Chmod(dst, 0600); e != nil {
			return e
		}
	}
	if invalid {
		return errInvalidOutput
	}
	return nil
}
