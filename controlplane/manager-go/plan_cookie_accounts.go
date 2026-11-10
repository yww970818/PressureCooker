package main

import (
	"encoding/json"
	"regexp"
	"strings"
	"unicode/utf8"
)

// This declaration is descriptive metadata in the immutable JMX, never code.
const cookieAccountsMarker = "jmeter.cookie_accounts"

var planAuthenticationTypeRE = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,39}$`)
var planIdentityNameRE = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_-]{0,39}$`)

func validIdentityField(field string) bool {
	return planAuthenticationTypeRE.MatchString(field) && field != "__proto__" && field != "constructor" && field != "prototype"
}
func identityFieldNames(value any) ([]any, error) {
	list, ok := value.([]any)
	if !ok || len(list) > 20 {
		return nil, api(400, "invalid_cookie_account_declaration", "身份字段须为最多20项的数组")
	}
	fields, seen := []any{}, map[string]bool{}
	for _, raw := range list {
		field, ok := raw.(string)
		if !ok || !validIdentityField(field) || seen[field] {
			return nil, api(400, "invalid_cookie_account_declaration", "身份字段须为唯一合法字段名")
		}
		fields = append(fields, field)
		seen[field] = true
	}
	return fields, nil
}

func normalizePlanCookieAccounts(value any) ([]any, error) {
	rows, ok := value.([]any)
	if !ok || len(rows) > 20 {
		return nil, api(400, "invalid_cookie_account_declaration", "脚本身份声明须为最多20条的数组")
	}
	result, seen := []any{}, map[string]bool{}
	for _, raw := range rows {
		row, ok := raw.(map[string]any)
		name, label, purpose := str(row["name"]), str(row["label"]), str(row["purpose"])
		if !ok || !planIdentityNameRE.MatchString(name) || seen[name] || strings.TrimSpace(label) == "" || utf8.RuneCountInString(label) > 80 || strings.TrimSpace(purpose) == "" || utf8.RuneCountInString(purpose) > 500 {
			return nil, api(400, "invalid_cookie_account_declaration", "脚本身份须有唯一合法名称、显示名称和用途说明")
		}
		seen[name] = true
		authType := "cookie"
		if v, exists := row["auth_type"]; exists {
			authType = str(v)
			if !planAuthenticationTypeRE.MatchString(authType) {
				return nil, api(400, "invalid_cookie_account_declaration", "身份auth_type须为以小写字母开头的认证类型名称，最多40个字母、数字、下划线或短横线")
			}
		}
		required := false
		if v, exists := row["required"]; exists {
			var valid bool
			required, valid = v.(bool)
			if !valid {
				return nil, api(400, "invalid_cookie_account_declaration", "身份required须为布尔值")
			}
		}
		requiredFields := []any{}
		if v, exists := row["required_fields"]; exists {
			var err error
			requiredFields, err = identityFieldNames(v)
			if err != nil {
				return nil, err
			}
		}
		fields := requiredFields
		if v, exists := row["fields"]; exists {
			var err error
			fields, err = identityFieldNames(v)
			if err != nil {
				return nil, err
			}
		}
		for _, field := range requiredFields {
			if !contains(stringList(fields), str(field)) {
				return nil, api(400, "invalid_cookie_account_declaration", "必填字段须在身份fields中声明")
			}
		}
		result = append(result, Obj{"name": name, "label": label, "purpose": purpose, "required": required, "fields": fields, "required_fields": requiredFields, "auth_type": authType})
	}
	return result, nil
}

func planCookieAccountMetadata(content string) ([]any, error) {
	roots, err := parseXMLFragment(content)
	if err != nil {
		return nil, err
	}
	result, declarations := []any{}, 0
	var walk func(*xmlElement) error
	walk = func(node *xmlElement) error {
		if node.Name.Local == "TestPlan" {
			for _, child := range node.children() {
				if child.Name.Local != "stringProp" || child.attr("name") != cookieAccountsMarker {
					continue
				}
				declarations++
				if declarations > 1 || len(child.text()) > 64*1024 {
					return api(400, "invalid_cookie_account_declaration", "脚本身份声明重复或过大")
				}
				var value any
				if json.Unmarshal([]byte(child.text()), &value) != nil {
					return api(400, "invalid_cookie_account_declaration", "脚本身份声明须为JSON数组")
				}
				var err error
				result, err = normalizePlanCookieAccounts(value)
				if err != nil {
					return err
				}
			}
		}
		for _, child := range node.children() {
			if err := walk(child); err != nil {
				return err
			}
		}
		return nil
	}
	for _, root := range roots {
		if err := walk(root); err != nil {
			return nil, err
		}
	}
	return result, nil
}
