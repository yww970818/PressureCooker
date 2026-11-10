package main

import (
	"errors"
	"net/url"
	"path"
	"regexp"
	"strings"
)

var downloadProjectID = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,100}$`)

func validPublicManagerURL(value string) bool {
	if value == "" {
		return true
	}
	u, e := url.Parse(value)
	return e == nil && (u.Scheme == "http" || u.Scheme == "https") && u.Host != "" && u.User == nil && u.RawQuery == "" && !u.ForceQuery && !strings.Contains(value, "#") && u.Opaque == ""
}

// Return a site-relative URL unless an explicit public manager origin exists.
// The private loopback API address and credentials never become download links.
func (c managerClient) scopedDownloadURL(route, project string) (string, error) {
	bad := func() (string, error) { return "", errors.New("Invalid authenticated download configuration") }
	u, e := url.Parse(route)
	if e != nil || !strings.HasPrefix(u.Path, "/api/") || u.IsAbs() || u.Host != "" || u.User != nil || u.Fragment != "" || u.RawPath != "" || path.Clean(u.Path) != u.Path || !downloadProjectID.MatchString(project) || !validPublicManagerURL(c.publicURL) {
		return bad()
	}
	query := u.Query()
	for key := range query {
		if key != "project_id" {
			return bad()
		}
	}
	query.Set("project_id", project)
	u.RawQuery = query.Encode()
	return strings.TrimRight(c.publicURL, "/") + u.String(), nil
}
