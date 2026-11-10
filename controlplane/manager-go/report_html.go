package main

import (
	"net/http"
	"net/url"
	"strings"
)

// Keep ownership in the browser base path: relative HTML, CSS and script URLs
// do not inherit the entry document's query parameters. Serve original bytes.
func projectReportHTMLPath(path string) (projectID, reportID, relative string, ok bool) {
	parts := strings.Split(strings.Trim(path, "/"), "/")
	if len(parts) < 6 || parts[0] != "api" || parts[1] != "projects" || parts[3] != "reports" || parts[5] != "html" {
		return "", "", "", false
	}
	return parts[2], parts[4], strings.Join(parts[6:], "/"), true
}

func projectReportHTMLURL(r *http.Request, projectID, reportID, relative string) string {
	query := r.URL.Query()
	query.Del("project_id")
	u := url.URL{Path: "/api/projects/" + projectID + "/reports/" + reportID + "/html/" + relative, RawQuery: query.Encode()}
	return u.String()
}
