package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/csv"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// Temp files are complete and synced before becoming visible at their final path.
// link is used for new secrets so concurrent preparations cannot replace a value.
func atomicWrite(path string, body []byte, mode os.FileMode, replace bool) error {
	file, err := os.CreateTemp(filepath.Dir(path), ".platform-ops-*")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	defer file.Close()
	if err = file.Chmod(mode); err != nil {
		return err
	}
	if _, err = file.Write(body); err != nil {
		return err
	}
	if err = file.Sync(); err != nil {
		return err
	}
	if err = file.Close(); err != nil {
		return err
	}
	if replace {
		return os.Rename(file.Name(), path)
	}
	return os.Link(file.Name(), path)
}

func prepareSecrets(root string) error {
	directory := filepath.Join(root, "runtime/secrets/platform")
	if err := os.MkdirAll(directory, 0700); err != nil {
		return err
	}
	for _, name := range []string{"admin-token", "session-key", "agent-token", "minio-access-key", "minio-secret-key"} {
		path := filepath.Join(directory, name)
		info, err := os.Lstat(path)
		if errors.Is(err, os.ErrNotExist) {
			length := 36
			if name == "minio-access-key" {
				length = 8
			}
			random := make([]byte, length)
			if _, err := rand.Read(random); err != nil {
				return err
			}
			value := base64.RawURLEncoding.EncodeToString(random)
			if name == "minio-access-key" {
				value = "jmeter-" + hex.EncodeToString(random)
			}
			if err := atomicWrite(path, []byte(value+"\n"), 0600, false); err != nil && !errors.Is(err, os.ErrExist) {
				return err
			}
			info, err = os.Lstat(path)
		}
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("secret must be a regular file: %s", name)
		}
		if err := os.Chmod(path, 0600); err != nil {
			return err
		}
	}
	return nil
}

func decodeJSON(in io.Reader, target any) error {
	decoder := json.NewDecoder(in)
	decoder.UseNumber()
	if err := decoder.Decode(target); err != nil {
		return errors.New("invalid JSON response/input")
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return errors.New("JSON response/input contains trailing data")
	}
	return nil
}

func jsonField(in io.Reader, out io.Writer, keys []string) error {
	var value any
	if err := decodeJSON(in, &value); err != nil {
		return err
	}
	for _, key := range keys {
		object, ok := value.(map[string]any)
		if !ok {
			return errors.New("JSON object key not found")
		}
		value, ok = object[key]
		if !ok {
			return errors.New("JSON object key not found")
		}
	}
	result, ok := value.(string)
	if !ok || result == "" {
		return errors.New("JSON field must be a nonempty string")
	}
	_, err := fmt.Fprintln(out, result)
	return err
}

func readJTL(in io.Reader) ([]map[string]string, error) {
	reader := csv.NewReader(in)
	header, err := reader.Read()
	if err != nil {
		return nil, fmt.Errorf("read JTL header: %w", err)
	}
	for index, name := range header {
		header[index] = strings.ToLower(strings.TrimPrefix(name, "\ufeff"))
	}
	rows := []map[string]string{}
	for {
		values, err := reader.Read()
		if err == io.EOF {
			return rows, nil
		}
		if err != nil {
			return nil, fmt.Errorf("read JTL row: %w", err)
		}
		row := make(map[string]string, len(header))
		for index, name := range header {
			row[name] = values[index]
		}
		rows = append(rows, row)
	}
}

func checkSmoke(directory string, out io.Writer) error {
	file, err := os.Open(filepath.Join(directory, "results.jtl"))
	if err != nil {
		return err
	}
	defer file.Close()
	rows, err := readJTL(file)
	if err != nil {
		return err
	}
	engines := map[string]int{}
	for _, row := range rows {
		if !strings.EqualFold(row["success"], "true") || row["responsecode"] != "200" {
			return errors.New("smoke contains a failed sample or non-200 response")
		}
		engines[row["hostname"]]++
	}
	if len(rows) != 10 || len(engines) != 2 || engines["worker-1"] != 5 || engines["worker-2"] != 5 {
		return errors.New("smoke requires 10 samples: five each from worker-1 and worker-2")
	}
	html := filepath.Join(directory, "html/index.html")
	info, err := os.Stat(html)
	if err != nil || !info.Mode().IsRegular() {
		return errors.New("HTML dashboard missing")
	}
	report := map[string]any{"passed": true, "samples": len(rows), "errors": 0, "samples_by_engine": engines, "html": html}
	body, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return err
	}
	if err := atomicWrite(filepath.Join(directory, "smoke-validation.json"), append(body, '\n'), 0644, true); err != nil {
		return err
	}
	return json.NewEncoder(out).Encode(report)
}

type nodeSource struct {
	Version  string `json:"version"`
	Filename string `json:"filename"`
	URL      string `json:"url"`
	SHA256   string `json:"sha256"`
}

func fileDigest(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	digest := sha256.New()
	if _, err = io.Copy(digest, file); err != nil {
		return "", err
	}
	return hex.EncodeToString(digest.Sum(nil)), nil
}

func fetchNode(ctx context.Context, root string, client *http.Client, out io.Writer) error {
	directory := filepath.Join(root, "vendor")
	body, err := os.ReadFile(filepath.Join(directory, "node-source.json"))
	if err != nil {
		return err
	}
	var meta nodeSource
	if err := decodeJSON(bytes.NewReader(body), &meta); err != nil {
		return err
	}
	filename := "node-v" + meta.Version + "-linux-x64.tar.xz"
	if !regexp.MustCompile(`^\d+\.\d+\.\d+$`).MatchString(meta.Version) || meta.Filename != filename || meta.URL != "https://nodejs.org/dist/v"+meta.Version+"/"+filename || !regexp.MustCompile(`^[a-f0-9]{64}$`).MatchString(meta.SHA256) {
		return errors.New("Node metadata must pin an official Linux x64 release and SHA256")
	}
	path := filepath.Join(directory, filename)
	if digest, err := fileDigest(path); err == nil && digest == meta.SHA256 {
		_, err = fmt.Fprintln(out, "Verified Node "+meta.Version)
		return err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, meta.URL, nil)
	if err != nil {
		return err
	}
	response, err := client.Do(request)
	if err != nil {
		return errors.New("Node download failed")
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("Node download returned HTTP %d", response.StatusCode)
	}
	file, err := os.CreateTemp(directory, ".node-download-*")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	defer file.Close()
	digest := sha256.New()
	if _, err = io.Copy(io.MultiWriter(file, digest), response.Body); err != nil {
		return errors.New("Node download was incomplete")
	}
	if hex.EncodeToString(digest.Sum(nil)) != meta.SHA256 {
		return errors.New("Node checksum mismatch")
	}
	if err = file.Chmod(0644); err != nil {
		return err
	}
	if err = file.Sync(); err != nil {
		return err
	}
	if err = file.Close(); err != nil {
		return err
	}
	if err = os.Rename(file.Name(), path); err != nil {
		return err
	}
	_, err = fmt.Fprintln(out, "Verified Node "+meta.Version)
	return err
}
