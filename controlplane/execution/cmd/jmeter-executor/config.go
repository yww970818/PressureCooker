package main

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"pressurecooker/execution/objectstore"
	"pressurecooker/execution/protocol"
	"pressurecooker/execution/queue"
)

type config struct {
	isolated                                                                      bool
	runtimeSlot                                                                   int
	datasetLimit, taskInputLimit                                                  int64
	id, role, cluster, port, token, state, work, results, java, home, common, rmi string
	heap                                                                          []string
	managerURL, nodeName                                                          string
}
type service struct {
	c               config
	q               *queue.Client
	s               *objectstore.Client
	http            *http.Client
	boot            string
	bootStartedAtMS int64
	machine         *machineCollector
	statusSequence  atomic.Int64
	worker          *worker
	runner          *runner
}

func env(k, d string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return d
}
func secret(k, d string) (string, error) {
	p := env(k, d)
	info, e := os.Lstat(p)
	if e != nil || !privateSecret(info) || info.Size() > 16384 {
		return "", errors.New("secret file unavailable: " + k)
	}
	b, e := os.ReadFile(p)
	return strings.TrimSpace(string(b)), e
}
func uuid() string {
	var b [16]byte
	if _, e := rand.Read(b[:]); e != nil {
		panic(e)
	}
	return hex.EncodeToString(b[:])
}
func timestamp() string { return time.Now().UTC().Format(time.RFC3339Nano) }
func directory(p string) error {
	if !filepath.IsAbs(p) || filepath.Clean(p) != p {
		return errors.New("invalid directory")
	}
	if p == "/" {
		return nil
	}
	if e := directory(filepath.Dir(p)); e != nil {
		return e
	}
	info, e := os.Lstat(p)
	if os.IsNotExist(e) {
		e = os.Mkdir(p, 0700)
		if e == nil {
			return nil
		}
		if !os.IsExist(e) {
			return e
		}
		info, e = os.Lstat(p)
	}
	if e != nil || !info.IsDir() {
		return errors.New("unsafe directory")
	}
	return nil
}
func writeFile(p string, b []byte) error {
	if e := directory(filepath.Dir(p)); e != nil {
		return e
	}
	f, e := os.CreateTemp(filepath.Dir(p), ".tmp-")
	if e != nil {
		return e
	}
	defer os.Remove(f.Name())
	if _, e = f.Write(b); e == nil {
		e = f.Sync()
	}
	ce := f.Close()
	if e != nil {
		return e
	}
	if ce != nil {
		return ce
	}
	if e = os.Rename(f.Name(), p); e != nil {
		return e
	}
	d, e := os.Open(filepath.Dir(p))
	if e != nil {
		return e
	}
	defer d.Close()
	return d.Sync()
}
func save(p string, v any) error {
	b, e := json.Marshal(v)
	if e != nil {
		return e
	}
	return writeFile(p, b)
}
func load(p string, v any) error {
	f, e := openRegular(p)
	if e != nil {
		return e
	}
	defer f.Close()
	b, e := io.ReadAll(io.LimitReader(f, 8<<20))
	if e != nil {
		return e
	}
	return json.Unmarshal(b, v)
}
func openRegular(p string) (*os.File, error) {
	dir := filepath.Dir(p)
	real, e := filepath.EvalSymlinks(dir)
	if e != nil {
		return nil, e
	}
	if !filepath.IsAbs(p) || filepath.Clean(p) != p || real != dir {
		return nil, errors.New("unsafe file directory")
	}
	i, e := os.Lstat(p)
	if e != nil {
		return nil, e
	}
	if !i.Mode().IsRegular() {
		return nil, errors.New("unsafe file")
	}
	f, e := os.Open(p)
	if e != nil {
		return nil, e
	}
	fi, e := f.Stat()
	if e != nil || !os.SameFile(i, fi) {
		f.Close()
		return nil, errors.New("file changed")
	}
	return f, nil
}
func configure() (*service, error) {
	id := env("JMETER_AGENT_ID", "")
	role := env("JMETER_AGENT_ROLE", "")
	c := config{id: id, role: role, port: env("JMETER_AGENT_PORT", "8082")}
	port, e := strconv.Atoi(c.port)
	if e != nil || port < 1 || port > 65535 {
		return nil, errors.New("invalid port")
	}
	srv := &service{c: c, boot: uuid(), bootStartedAtMS: time.Now().UnixMilli(), http: &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}
	if role == "fixture" {
		return srv, nil
	}
	if !protocol.ValidID(id) || (role != "controller" && role != "worker") {
		return nil, errors.New("invalid agent identity")
	}
	if e = c.nodeSettings(); e != nil {
		return nil, e
	}
	if e = protectParent(); e != nil {
		return nil, e
	}
	c.isolated = true
	c.token, e = secret("JMETER_AGENT_TOKEN_FILE", "/run/platform/agent-token")
	if e != nil || len(c.token) < 16 {
		return nil, errors.New("invalid agent token")
	}
	sum := sha256.Sum256([]byte(id))
	c.cluster = env("JMETER_CLUSTER_ID", "cluster-"+hex.EncodeToString(sum[:])[:24])
	if !protocol.ValidID(c.cluster) {
		return nil, errors.New("invalid cluster")
	}
	c.datasetLimit, e = strconv.ParseInt(env("JMETER_DATASET_MAX_BYTES", "268435456"), 10, 64)
	if e != nil || c.datasetLimit < 1 || c.datasetLimit > 1<<30 {
		return nil, errors.New("invalid dataset limit")
	}
	c.taskInputLimit, e = strconv.ParseInt(env("JMETER_TASK_INPUT_MAX_BYTES", "1073741824"), 10, 64)
	if e != nil || c.taskInputLimit < 1 || c.taskInputLimit > 8<<30 {
		return nil, errors.New("invalid task input limit")
	}
	c.work = env("JMETER_WORK_DIR", "/work")
	c.state = filepath.Join(env("JMETER_AGENT_STATE_DIR", "/state"), "execution", id)
	c.results = env("JMETER_RESULTS_DIR", "/results")
	c.home = env("JMETER_HOME", "/opt/apache-jmeter-5.6.3")
	c.common = env("JMETER_COMMON_PROPERTIES", "/opt/cluster/conf/common.properties")
	c.rmi = env("JMETER_RMI_PROPERTIES", "/run/jmeter-rmi/rmi.properties")
	c.java = "java"
	if h := os.Getenv("JAVA_HOME"); h != "" {
		c.java = filepath.Join(h, "bin/java")
	}
	c.heap = strings.Fields(env("JMETER_ENGINE_HEAP", "-Xms128m -Xmx1024m"))
	heapRE := regexp.MustCompile(`^-Xm[sx][1-9][0-9]*[mMgG]$`)
	for _, s := range c.heap {
		if !heapRE.MatchString(s) {
			return nil, errors.New("invalid JVM heap")
		}
	}
	for _, d := range []string{c.state, filepath.Join(c.work, "tasks")} {
		if e = directory(d); e != nil {
			return nil, e
		}
	}
	if e = c.protectPaths(); e != nil {
		return nil, e
	}
	pass, e := secret("JMETER_REDIS_PASSWORD_FILE", "/run/platform/redis-password")
	if e != nil {
		return nil, e
	}
	db, e := strconv.Atoi(env("JMETER_REDIS_DB", "0"))
	if e != nil {
		return nil, e
	}
	srv.q, e = queue.New(queue.Config{Addr: env("JMETER_REDIS_ADDR", ""), Password: pass, DB: db, Prefix: env("JMETER_REDIS_PREFIX", "jmeter:v2")})
	if e != nil {
		return nil, e
	}
	access, e := secret("JMETER_ARCHIVE_ACCESS_KEY_FILE", "/run/platform/minio-access-key")
	if e != nil {
		return nil, e
	}
	sk, e := secret("JMETER_ARCHIVE_SECRET_KEY_FILE", "/run/platform/minio-secret-key")
	if e != nil {
		return nil, e
	}
	srv.s, e = objectstore.New(objectstore.Config{Endpoint: env("JMETER_ARCHIVE_ENDPOINT", "http://minio:9000"), Bucket: env("JMETER_ARCHIVE_BUCKET", "jmeter-reports"), Region: env("JMETER_ARCHIVE_REGION", "us-east-1"), AccessKey: access, SecretKey: sk})
	srv.c = c
	srv.machine = newMachineCollector(c.work)
	return srv, e
}
func (s *service) putFile(ctx context.Context, key, p, ctype string) (protocol.ObjectRef, error) {
	var ref protocol.ObjectRef
	f, e := openRegular(p)
	if e != nil {
		return ref, e
	}
	defer f.Close()
	h := sha256.New()
	n, e := io.Copy(h, f)
	if e != nil {
		return ref, e
	}
	r, e := s.s.Put(ctx, key, f, n, hex.EncodeToString(h.Sum(nil)), ctype)
	r.Name = filepath.Base(p)
	r.URL = fmt.Sprintf("s3://%s/%s", r.Bucket, r.Key)
	return r, e
}
