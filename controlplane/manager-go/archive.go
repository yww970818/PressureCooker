package main

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"
)

var rawFiles = []string{"results.jtl", "jmeter.log", "console.log"}

func rawPrefix(run Obj) (string, error) {
	value := str(run["started_at"])
	if value == "" {
		value = str(run["created_at"])
	}
	t, e := time.Parse(time.RFC3339Nano, value)
	if e != nil {
		return "", fmt.Errorf("invalid run timestamp")
	}
	return t.In(time.FixedZone("Asia/Shanghai", 8*3600)).Format("2006-01-02") + "/" + str(run["id"]) + "/raw/", nil
}
func newReceipt(run Obj) (Obj, error) {
	prefix, e := rawPrefix(run)
	if e != nil {
		return nil, e
	}
	return Obj{"status": "PENDING", "prefix": prefix, "files": []any{}, "missing_files": []any{}, "attempts": 0, "next_retry_at": nil, "error": nil, "archived_at": nil}, nil
}
func (p *Platform) ensureRawArchive(id string) (Obj, error) {
	return p.mutateRun(id, func(run Obj) error {
		if contains(terminal, str(run["status"])) && run["raw_archive"] == nil {
			r, e := newReceipt(run)
			if e != nil {
				return e
			}
			run["raw_archive"] = r
		}
		return nil
	})
}
func (p *Platform) rawFile(id, name string) (string, error) {
	if e := validID(id); e != nil {
		return "", e
	}
	if !contains(rawFiles, name) {
		return "", api(400, "invalid_artifact", "Only original JTL and log files are available")
	}
	return safePath(p.Config.ResultsDir, id+"/raw/"+name, true)
}
func (p *Platform) artifactFile(id, name string) (string, error) {
	r, e := p.get("runs", id)
	if e != nil {
		return "", e
	}
	if !contains(terminal, str(r["status"])) {
		return "", api(409, "run_active", "Original file downloads require a terminal run")
	}
	return p.rawFile(id, name)
}
func (p *Platform) listArtifacts(id string) (Obj, error) {
	run, e := p.get("runs", id)
	if e != nil {
		return nil, e
	}
	if !contains(terminal, str(run["status"])) {
		return nil, api(409, "run_active", "Original file downloads require a terminal run; use /log while running")
	}
	if str(run["execution_mode"]) == "redis" {
		receipt := obj(run["raw_archive"])
		files := arr(receipt["files"])
		return Obj{"project_id": objectProject(run), "items": files, "total": len(files), "archive": receipt}, nil
	}
	receipt := obj(run["raw_archive"])
	previous := map[string]Obj{}
	for _, v := range arr(receipt["files"]) {
		f := obj(v)
		previous[str(f["name"])] = f
	}
	prefix := str(receipt["prefix"])
	if prefix == "" {
		prefix, e = rawPrefix(run)
		if e != nil {
			return nil, e
		}
	}
	items := []any{}
	for _, name := range rawFiles {
		path, e := p.rawFile(id, name)
		if e != nil {
			if a, ok := e.(*APIError); ok && a.Status == 404 {
				continue
			}
			return nil, e
		}
		info, e := os.Stat(path)
		if e != nil {
			return nil, e
		}
		saved := previous[name]
		key := str(saved["object_key"])
		if key == "" {
			key = prefix + name
		}
		state := str(saved["status"])
		if state == "" {
			state = "PENDING"
		}
		items = append(items, Obj{"name": name, "size": info.Size(), "sha256": saved["sha256"], "download_url": "/api/runs/" + id + "/artifacts/" + name, "object_key": key, "status": state})
	}
	return Obj{"project_id": objectProject(run), "items": items, "total": len(items), "archive": run["raw_archive"]}, nil
}
func hashStream(r io.Reader) (string, int64, error) {
	h := sha256.New()
	n, e := io.CopyBuffer(h, r, make([]byte, 1024*1024))
	return hex.EncodeToString(h.Sum(nil)), n, e
}

type ArchiveError struct{ Message string }

func (e *ArchiveError) Error() string { return e.Message }

type S3Archive struct {
	Config Config
	HTTP   *http.Client
}

func hmacSHA(key []byte, value string) []byte {
	h := hmac.New(sha256.New, key)
	h.Write([]byte(value))
	return h.Sum(nil)
}
func escapeObject(key string) string {
	parts := strings.Split(key, "/")
	for i, p := range parts {
		parts[i] = url.PathEscape(p)
	}
	return strings.Join(parts, "/")
}
func (a *S3Archive) request(method, key string, body io.Reader, size int64, hash, contentType string) (int, string, error) {
	c := a.Config
	endpoint, e := url.Parse(c.ArchiveEndpoint)
	if e != nil || !contains([]string{"http", "https"}, endpoint.Scheme) || endpoint.User != nil || endpoint.RawQuery != "" || endpoint.Fragment != "" || endpoint.Host == "" {
		return 0, "", &ArchiveError{"Invalid S3 endpoint"}
	}
	if c.ArchiveAccessKey == "" || c.ArchiveSecretKey == "" {
		return 0, "", &ArchiveError{"Archive credentials unavailable"}
	}
	path := strings.TrimRight(endpoint.EscapedPath(), "/") + "/" + url.PathEscape(c.ArchiveBucket)
	if key != "" {
		path += "/" + escapeObject(key)
	}
	stamp := time.Now().UTC()
	timestamp, date := stamp.Format("20060102T150405Z"), stamp.Format("20060102")
	headers := map[string]string{"host": endpoint.Host, "x-amz-content-sha256": hash, "x-amz-date": timestamp}
	if method == "PUT" && key != "" {
		headers["if-none-match"] = "*"
	}
	keys := []string{}
	for k := range headers {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	signed := strings.Join(keys, ";")
	var canonicalHeaders strings.Builder
	for _, k := range keys {
		canonicalHeaders.WriteString(k + ":" + strings.TrimSpace(headers[k]) + "\n")
	}
	canonical := strings.Join([]string{method, path, "", canonicalHeaders.String(), signed, hash}, "\n")
	scope := date + "/" + c.ArchiveRegion + "/s3/aws4_request"
	toSign := strings.Join([]string{"AWS4-HMAC-SHA256", timestamp, scope, digest([]byte(canonical))}, "\n")
	signingKey := []byte("AWS4" + c.ArchiveSecretKey)
	for _, component := range strings.Split(scope, "/") {
		signingKey = hmacSHA(signingKey, component)
	}
	signature := hex.EncodeToString(hmacSHA(signingKey, toSign))
	r, e := http.NewRequest(method, endpoint.Scheme+"://"+endpoint.Host+path, body)
	if e != nil {
		return 0, "", &ArchiveError{"Invalid S3 request"}
	}
	for k, v := range headers {
		if k != "host" {
			r.Header.Set(k, v)
		}
	}
	r.Header.Set("Authorization", "AWS4-HMAC-SHA256 Credential="+c.ArchiveAccessKey+"/"+scope+", SignedHeaders="+signed+", Signature="+signature)
	r.Header.Set("Content-Type", contentType)
	if method == "PUT" {
		r.ContentLength = size
	}
	res, e := a.HTTP.Do(r)
	if e != nil {
		return 0, "", &ArchiveError{"S3 connection failed"}
	}
	defer res.Body.Close()
	if method == "GET" {
		hash, _, e := hashStream(res.Body)
		if e != nil {
			return 0, "", &ArchiveError{"S3 response read failed"}
		}
		return res.StatusCode, hash, nil
	}
	io.Copy(io.Discard, io.LimitReader(res.Body, 64*1024))
	return res.StatusCode, "", nil
}
func (a *S3Archive) inspect(method, key string) (int, string, error) {
	return a.request(method, key, nil, 0, digest(nil), "application/octet-stream")
}
func (a *S3Archive) verify(key, hash string) error {
	status, remote, e := a.inspect("GET", key)
	if e != nil {
		return e
	}
	if status == 200 && constant(remote, hash) {
		return nil
	}
	return &ArchiveError{"Existing raw object differs; immutable object was not overwritten"}
}
func (a *S3Archive) putStream(key string, source *os.File, size int64, hash, contentType string) error {
	status, _, e := a.inspect("HEAD", key)
	if e != nil {
		return e
	}
	if status == 200 {
		return a.verify(key, hash)
	}
	if status != 404 {
		return &ArchiveError{fmt.Sprintf("S3 archive HTTP %d", status)}
	}
	status, _, e = a.request("PUT", key, source, size, hash, contentType)
	if e != nil {
		return e
	}
	if contains([]string{"200", "201", "204"}, fmt.Sprint(status)) {
		return nil
	}
	if status == 409 || status == 412 {
		return a.verify(key, hash)
	}
	return &ArchiveError{fmt.Sprintf("S3 archive HTTP %d", status)}
}
func (p *Platform) archiveRun(id string) error {
	if e := validID(id); e != nil {
		return e
	}
	locks := filepath.Join(p.Config.StateDir, "raw-archive-locks")
	if e := os.MkdirAll(locks, 0755); e != nil {
		return e
	}
	lock, e := os.OpenFile(filepath.Join(locks, id+".lock"), os.O_CREATE|os.O_RDWR, 0600)
	if e != nil {
		return e
	}
	defer lock.Close()
	if e = syscall.Flock(int(lock.Fd()), syscall.LOCK_EX); e != nil {
		return e
	}
	defer syscall.Flock(int(lock.Fd()), syscall.LOCK_UN)
	run, e := p.get("runs", id)
	if e != nil {
		return e
	}
	if !contains(terminal, str(run["status"])) {
		return nil
	}
	receipt := obj(run["raw_archive"])
	if len(receipt) == 0 {
		receipt, e = newReceipt(run)
		if e != nil {
			return e
		}
	}
	if contains([]string{"ARCHIVED", "EMPTY"}, str(receipt["status"])) {
		return nil
	}
	if number(receipt["attempts"]) == 0 && len(arr(receipt["files"])) == 0 {
		prefix, e := rawPrefix(run)
		if e != nil {
			return e
		}
		receipt["prefix"] = prefix
	}
	prior := map[string]Obj{}
	for _, item := range arr(receipt["files"]) {
		m := obj(item)
		prior[str(m["name"])] = m
	}
	receipt["status"] = "UPLOADING"
	receipt["attempts"] = int(number(receipt["attempts"])) + 1
	receipt["error"] = nil
	receipt["next_retry_at"] = nil
	receipt["archived_at"] = nil
	receipt["files"] = []any{}
	receipt["missing_files"] = []any{}
	save := func() error { _, e := p.updateRun(id, Obj{"raw_archive": receipt}); return e }
	if e = save(); e != nil {
		return e
	}
	errors := []string{}
	client := &S3Archive{p.Config, newArchiveHTTPClient(p.Config.ArchiveTimeout)}
	defer client.HTTP.CloseIdleConnections()
	for _, name := range rawFiles {
		path, e := p.rawFile(id, name)
		if a, ok := e.(*APIError); ok && a.Status == 404 {
			receipt["missing_files"] = append(arr(receipt["missing_files"]), name)
			continue
		}
		item := Obj{"name": name, "size": nil, "sha256": nil, "object_key": str(receipt["prefix"]) + name, "status": "PENDING"}
		if e == nil {
			info, se := os.Stat(path)
			if se != nil {
				e = se
			} else {
				item["size"] = info.Size()
			}
		}
		if e == nil && p.Config.ArchiveEndpoint == "" {
			item["status"] = "DISABLED"
		} else if e == nil {
			var f *os.File
			f, e = os.Open(path)
			if e == nil {
				var hash string
				var size int64
				hash, size, e = hashStream(f)
				if e == nil {
					item["size"] = size
					item["sha256"] = hash
					receipt["files"] = append(arr(receipt["files"]), item)
					if e = save(); e == nil {
						previous := prior[name]
						if !(str(previous["status"]) == "ARCHIVED" && str(previous["sha256"]) == hash && str(previous["object_key"]) == str(item["object_key"])) {
							if _, e = f.Seek(0, io.SeekStart); e == nil {
								contentType := "text/plain"
								if name == "results.jtl" {
									contentType = "text/csv"
								}
								e = client.putStream(str(item["object_key"]), f, size, hash, contentType)
							}
						}
					}
				}
				f.Close()
			}
			if e == nil {
				item["status"] = "ARCHIVED"
			}
		}
		if e != nil {
			message := fmt.Sprintf("%T", e)
			if a, ok := e.(*ArchiveError); ok {
				message = a.Message
			}
			if a, ok := e.(*APIError); ok {
				message = a.Message
			}
			item["status"] = "FAILED"
			item["error"] = message
			errors = append(errors, name+": "+message)
		}
		exists := false
		for _, v := range arr(receipt["files"]) {
			if str(obj(v)["name"]) == name {
				exists = true
				break
			}
		}
		if !exists {
			receipt["files"] = append(arr(receipt["files"]), item)
		}
		if e = save(); e != nil {
			return e
		}
	}
	if len(arr(receipt["files"])) == 0 {
		receipt["status"] = "EMPTY"
		receipt["error"] = "No original artifacts were produced"
	} else if p.Config.ArchiveEndpoint == "" {
		receipt["status"] = "DISABLED"
	} else if len(errors) > 0 || len(arr(receipt["missing_files"])) > 0 {
		missing := []string{}
		for _, m := range arr(receipt["missing_files"]) {
			missing = append(missing, str(m))
		}
		if len(missing) > 0 {
			errors = append(errors, "Missing raw artifacts: "+strings.Join(missing, ", "))
		}
		receipt["status"] = "FAILED"
		receipt["error"] = strings.Join(errors, "; ")
		attempts := int(number(receipt["attempts"]))
		if attempts > 8 {
			attempts = 8
		}
		seconds := 1 << attempts
		if seconds > 300 {
			seconds = 300
		}
		receipt["next_retry_at"] = time.Now().UTC().Add(time.Duration(seconds) * time.Second).Format("2006-01-02T15:04:05.000Z")
	} else {
		receipt["status"] = "ARCHIVED"
		receipt["archived_at"] = now()
	}
	return save()
}
