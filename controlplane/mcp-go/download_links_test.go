package main

import "testing"

func TestDownloadsUsePortableAuthenticatedPathsAndOptionalPublicOrigin(t *testing.T) {
	for _, tc := range []struct{ public, path, project, want string }{
		{"", "/api/datasets/data-one/content", "project-one", "/api/datasets/data-one/content?project_id=project-one"},
		{"https://platform.example/console/", "/api/pressure-reports/pressure-one/export.html", "project-two", "https://platform.example/console/api/pressure-reports/pressure-one/export.html?project_id=project-two"},
	} {
		got, e := (managerClient{baseURL: "http://127.0.0.1:8080", publicURL: tc.public}).scopedDownloadURL(tc.path, tc.project)
		if e != nil || got != tc.want {
			t.Fatalf("URL %q want %q error %v", got, tc.want, e)
		}
	}
	for _, base := range []string{"https://user:password@platform.example", "https://platform.example?token=secret", "https://platform.example#fragment", "https://platform.example/#", "//evil.example", "file:///tmp"} {
		if _, e := (managerClient{publicURL: base}).scopedDownloadURL("/api/datasets/data-one/content", "project-one"); e == nil {
			t.Fatal("unsafe public origin accepted")
		}
	}
	for _, path := range []string{"https://evil.example/file", "//evil.example/file", "/api/../secrets", "/api/data?token=secret"} {
		if _, e := (managerClient{}).scopedDownloadURL(path, "project-one"); e == nil {
			t.Fatal("unsafe download path accepted", path)
		}
	}
}
