// Package objectstore is a path-style, SigV4 S3 client. Object references cannot
// override the configured host, and uploads never replace an existing object.
package objectstore

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/xml"
	"errors"
	"fmt"
	"hash"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"pressurecooker/execution/protocol"
)

type Config struct {
	Endpoint, Bucket, AccessKey, SecretKey, Region string
	Timeout                                        time.Duration
}
type Client struct {
	cfg      Config
	endpoint *url.URL
	http     *http.Client
}

func New(c Config) (*Client, error) {
	u, e := url.Parse(c.Endpoint)
	if e != nil || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Scheme != "http" && u.Scheme != "https") || !protocol.ValidID(c.Bucket) || c.AccessKey == "" || c.SecretKey == "" {
		return nil, errors.New("invalid object store configuration")
	}
	if c.Timeout <= 0 {
		c.Timeout = 5 * time.Minute
	}
	if c.Region == "" {
		c.Region = "us-east-1"
	}
	client := newIdleHTTPClient(c.Timeout)
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &Client{c, u, client}, nil
}
func validKey(k string) bool {
	if len(k) > 1024 {
		return false
	}
	for _, p := range strings.Split(k, "/") {
		if !protocol.ValidID(p) {
			return false
		}
	}
	return true
}
func validHash(s string) bool {
	v, e := hex.DecodeString(s)
	return e == nil && len(v) == 32 && s == strings.ToLower(s)
}
func (c *Client) valid(r protocol.ObjectRef) bool {
	return r.Bucket == c.cfg.Bucket && r.Size >= 0 && validHash(r.SHA256) && validKey(r.Key)
}
func sign(key []byte, s string) []byte {
	h := hmac.New(sha256.New, key)
	h.Write([]byte(s))
	return h.Sum(nil)
}
func digest(b []byte) string { v := sha256.Sum256(b); return hex.EncodeToString(v[:]) }
func (c *Client) request(ctx context.Context, method, key string, body io.Reader, size int64, sha, ctype string) (*http.Response, error) {
	return c.requestQuery(ctx, method, key, nil, body, size, sha, ctype)
}

func (c *Client) requestQuery(ctx context.Context, method, key string, query url.Values, body io.Reader, size int64, sha, ctype string) (*http.Response, error) {
	u := *c.endpoint
	u.Path = strings.TrimRight(u.Path, "/") + "/" + c.cfg.Bucket + "/" + key
	u.RawPath = ""
	u.RawQuery = strings.ReplaceAll(query.Encode(), "+", "%20")
	r, e := http.NewRequestWithContext(ctx, method, u.String(), body)
	if e != nil {
		return nil, errors.New("invalid object request")
	}
	now := time.Now().UTC()
	stamp, day := now.Format("20060102T150405Z"), now.Format("20060102")
	headers := map[string]string{"host": u.Host, "x-amz-content-sha256": sha, "x-amz-date": stamp}
	if method == "PUT" {
		headers["if-none-match"] = "*"
		headers["x-amz-meta-sha256"] = sha
		headers["content-type"] = ctype
		r.ContentLength = size
		if size == 0 {
			r.Body = http.NoBody
		}
	}
	keys := make([]string, 0, len(headers))
	for k := range headers {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var canonical strings.Builder
	for _, k := range keys {
		canonical.WriteString(k + ":" + headers[k] + "\n")
		if k != "host" {
			r.Header.Set(k, headers[k])
		}
	}
	names := strings.Join(keys, ";")
	scope := day + "/" + c.cfg.Region + "/s3/aws4_request"
	signed := strings.Join([]string{method, u.EscapedPath(), u.RawQuery, canonical.String(), names, sha}, "\n")
	secret := []byte("AWS4" + c.cfg.SecretKey)
	for _, part := range strings.Split(scope, "/") {
		secret = sign(secret, part)
	}
	sig := hex.EncodeToString(sign(secret, strings.Join([]string{"AWS4-HMAC-SHA256", stamp, scope, digest([]byte(signed))}, "\n")))
	r.Header.Set("Authorization", "AWS4-HMAC-SHA256 Credential="+c.cfg.AccessKey+"/"+scope+", SignedHeaders="+names+", Signature="+sig)
	resp, e := c.http.Do(r)
	if e != nil {
		// Preserve cancellation without exposing URLs or raw transport errors.
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		return nil, errors.New("object store connection failed")
	}
	return resp, nil
}

var errNotFound = errors.New("object not found")

func httpError(code int) error { return fmt.Errorf("object store HTTP %d", code) }
func (c *Client) Head(ctx context.Context, r protocol.ObjectRef) (protocol.ObjectRef, error) {
	if !c.valid(r) {
		return r, errors.New("invalid object reference")
	}
	resp, e := c.request(ctx, "HEAD", r.Key, nil, 0, digest(nil), "")
	if e != nil {
		return r, e
	}
	defer resp.Body.Close()
	if resp.StatusCode == 404 {
		return r, errNotFound
	}
	if resp.StatusCode != 200 {
		return r, httpError(resp.StatusCode)
	}
	if resp.ContentLength != r.Size || resp.Header.Get("X-Amz-Meta-Sha256") != r.SHA256 {
		return r, errors.New("object receipt mismatch")
	}
	r.ContentType = resp.Header.Get("Content-Type")
	return r, nil
}
func (c *Client) Put(ctx context.Context, key string, rd io.ReadSeeker, size int64, sha, ctype string) (protocol.ObjectRef, error) {
	r := protocol.ObjectRef{Bucket: c.cfg.Bucket, Key: key, SHA256: sha, Size: size, ContentType: ctype}
	if !c.valid(r) || ctype == "" || strings.ContainsAny(ctype, "\r\n") {
		return r, errors.New("invalid object upload")
	}
	// Avoid transferring an entire multi-GiB raw log again after a retry.
	if existing, e := c.Head(ctx, r); e == nil {
		return existing, nil
	} else if !errors.Is(e, errNotFound) {
		return r, e
	}
	if _, e := rd.Seek(0, io.SeekStart); e != nil {
		return r, e
	}
	// LimitReader deliberately does not transfer ownership of the caller's file to net/http.
	resp, e := c.request(ctx, "PUT", key, io.LimitReader(rd, size), size, sha, ctype)
	if e != nil {
		return r, e
	}
	resp.Body.Close()
	switch resp.StatusCode {
	case 200, 201, 204, 409, 412:
		return c.Head(ctx, r)
	}
	return r, httpError(resp.StatusCode)
}

type verifiedReader struct {
	source io.ReadCloser
	hash   hash.Hash
	ref    protocol.ObjectRef
	n      int64
	done   bool
}

func (r *verifiedReader) Read(p []byte) (int, error) {
	if r.done {
		return 0, io.EOF
	}
	n, e := r.source.Read(p)
	r.hash.Write(p[:n])
	r.n += int64(n)
	if r.n > r.ref.Size {
		return 0, errors.New("object exceeds declared size")
	}
	if e == io.EOF {
		r.done = true
		if r.n != r.ref.Size || hex.EncodeToString(r.hash.Sum(nil)) != r.ref.SHA256 {
			return n, errors.New("object checksum mismatch")
		}
	}
	return n, e
}
func (r *verifiedReader) Close() error { return r.source.Close() }
func (c *Client) Get(ctx context.Context, r protocol.ObjectRef) (io.ReadCloser, error) {
	if !c.valid(r) {
		return nil, errors.New("invalid object reference")
	}
	resp, e := c.request(ctx, "GET", r.Key, nil, 0, digest(nil), "")
	if e != nil {
		return nil, e
	}
	if resp.StatusCode != 200 {
		resp.Body.Close()
		return nil, httpError(resp.StatusCode)
	}
	if resp.ContentLength != r.Size {
		resp.Body.Close()
		return nil, errors.New("object length mismatch")
	}
	return &verifiedReader{source: resp.Body, hash: sha256.New(), ref: r}, nil
}

// Download publishes a private file only after the entire object verifies.
func (c *Client) Download(ctx context.Context, r protocol.ObjectRef, dst string) error {
	if !filepath.IsAbs(dst) || filepath.Clean(dst) != dst {
		return errors.New("invalid destination")
	}
	dir := filepath.Dir(dst)
	real, e := filepath.EvalSymlinks(dir)
	if e != nil || real != dir {
		return errors.New("untrusted download directory")
	}
	if info, e := os.Lstat(dst); e == nil && !info.Mode().IsRegular() {
		return errors.New("unsafe download destination")
	} else if e != nil && !os.IsNotExist(e) {
		return e
	}
	src, e := c.Get(ctx, r)
	if e != nil {
		return e
	}
	defer src.Close()
	f, e := os.CreateTemp(dir, ".download-")
	if e != nil {
		return e
	}
	defer os.Remove(f.Name())
	_, e = io.Copy(f, src)
	if e == nil {
		e = f.Sync()
	}
	ce := f.Close()
	if e != nil {
		return e
	}
	if ce != nil {
		return ce
	}
	return os.Rename(f.Name(), dst)
}
func (c *Client) Delete(ctx context.Context, key string) error {
	if !validKey(key) {
		return errors.New("invalid key")
	}
	r, e := c.request(ctx, "DELETE", key, nil, 0, digest(nil), "")
	if e != nil {
		return e
	}
	defer r.Body.Close()
	if r.StatusCode/100 != 2 {
		return httpError(r.StatusCode)
	}
	return nil
}

// CloseIdleConnections releases the transport pool after its process owner has
// stopped request and background work. It does not interrupt active transfers.
func (c *Client) CloseIdleConnections() { c.http.CloseIdleConnections() }

// DeletePrefix removes only keys below a validated slash-terminated prefix.
// Listing and deletion are bounded and resumable: a failed call can be retried
// from the start, since S3 deletion is idempotent.
func (c *Client) DeletePrefix(ctx context.Context, prefix string) error {
	if !strings.HasSuffix(prefix, "/") || !validKey(strings.TrimSuffix(prefix, "/")) {
		return errors.New("invalid deletion prefix")
	}
	token := ""
	for page := 0; page < 10000; page++ {
		query := url.Values{"list-type": {"2"}, "prefix": {prefix}, "max-keys": {"1000"}}
		if token != "" {
			query.Set("continuation-token", token)
		}
		resp, err := c.requestQuery(ctx, "GET", "", query, nil, 0, digest(nil), "")
		if err != nil {
			return err
		}
		if resp.StatusCode != 200 {
			resp.Body.Close()
			return httpError(resp.StatusCode)
		}
		var listing struct {
			Truncated bool   `xml:"IsTruncated"`
			Next      string `xml:"NextContinuationToken"`
			Items     []struct {
				Key string `xml:"Key"`
			} `xml:"Contents"`
		}
		err = xml.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(&listing)
		resp.Body.Close()
		if err != nil || len(listing.Items) > 1000 {
			return errors.New("invalid object prefix listing")
		}
		for _, item := range listing.Items {
			if !validKey(item.Key) || !strings.HasPrefix(item.Key, prefix) {
				return errors.New("object outside deletion prefix")
			}
		}
		for _, item := range listing.Items {
			if err = c.Delete(ctx, item.Key); err != nil {
				return err
			}
		}
		if !listing.Truncated {
			return nil
		}
		if listing.Next == "" || listing.Next == token || len(listing.Next) > 8192 {
			return errors.New("invalid object prefix continuation")
		}
		token = listing.Next
	}
	return errors.New("object prefix page limit exceeded")
}
