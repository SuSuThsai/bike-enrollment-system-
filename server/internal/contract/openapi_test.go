// Package contract 存放基于 docs/openapi.yaml 的 HTTP 契约测试骨架。
//
// 本包只有 *_test.go 文件，不参与生产构建。测试只验证四件事：
//  1. 合法请求是否被接受；
//  2. 缺失必填字段是否被拒绝；
//  3. 成功响应是否包含全部 required 字段；
//  4. 错误响应的 code 是否落在 ErrorResponse.code 枚举内。
//
// 业务逻辑尚未实现（处理器统一返回 501），相关断言会以 skip 形式保留，
// 待处理器落地后自动生效；设置 CONTRACT_STRICT=1 可把 skip 升级为失败。
package contract

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"

	"gopkg.in/yaml.v3"
)

const dateTimeSample = "2026-01-01T00:00:00Z"

// openAPISpec 是 openapi.yaml 的最小只读视图。
type openAPISpec struct {
	doc    map[string]any
	source string
}

var (
	specOnce   sync.Once
	cachedSpec *openAPISpec
	specErr    error
)

// apiOperation 是 paths.{path}.{method} 的投影。
type apiOperation struct {
	Method   string
	Path     string
	Item     map[string]any
	PathItem map[string]any
}

// apiParameter 合并 path item 与 operation 两个层级的参数声明。
type apiParameter struct {
	Name     string
	In       string
	Required bool
	Schema   map[string]any
}

func loadSpec(t *testing.T) *openAPISpec {
	t.Helper()

	specOnce.Do(func() {
		path, err := findSpecPath()
		if err != nil {
			specErr = err
			return
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			specErr = fmt.Errorf("读取 %s: %w", path, err)
			return
		}
		var doc map[string]any
		if err := yaml.Unmarshal(raw, &doc); err != nil {
			specErr = fmt.Errorf("解析 %s: %w", path, err)
			return
		}
		cachedSpec = &openAPISpec{doc: doc, source: path}
	})

	if specErr != nil {
		t.Fatalf("加载 OpenAPI 失败: %v", specErr)
	}
	return cachedSpec
}

// findSpecPath 从工作目录和当前源文件目录分别向上查找 docs/openapi.yaml。
func findSpecPath() (string, error) {
	var starts []string
	if wd, err := os.Getwd(); err == nil {
		starts = append(starts, wd)
	}
	if _, file, _, ok := runtime.Caller(0); ok {
		starts = append(starts, filepath.Dir(file))
	}

	visited := make(map[string]bool)
	for _, start := range starts {
		for dir := start; ; dir = filepath.Dir(dir) {
			if visited[dir] {
				break
			}
			visited[dir] = true

			candidate := filepath.Join(dir, "docs", "openapi.yaml")
			if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
				return candidate, nil
			}

			parent := filepath.Dir(dir)
			if parent == dir {
				break
			}
		}
	}
	return "", errors.New("未找到 docs/openapi.yaml")
}

func asMap(value any) (map[string]any, bool) {
	result, ok := value.(map[string]any)
	return result, ok
}

func asSlice(value any) ([]any, bool) {
	result, ok := value.([]any)
	return result, ok
}

func asString(value any) string {
	result, _ := value.(string)
	return result
}

func asInt(value any) (int, bool) {
	switch n := value.(type) {
	case int:
		return n, true
	case int64:
		return int(n), true
	case float64:
		return int(n), true
	default:
		return 0, false
	}
}

// operation 返回指定 method/path 的 OpenAPI 操作；路径错误直接终止当前测试。
func (s *openAPISpec) operation(t *testing.T, method, path string) *apiOperation {
	t.Helper()

	paths, ok := asMap(s.doc["paths"])
	if !ok {
		t.Fatal("openapi.yaml 缺少 paths")
	}
	pathItem, ok := asMap(paths[path])
	if !ok {
		t.Fatalf("openapi.yaml 未定义路径 %s", path)
	}
	item, ok := asMap(pathItem[strings.ToLower(method)])
	if !ok {
		t.Fatalf("openapi.yaml 未定义 %s %s", method, path)
	}
	return &apiOperation{Method: strings.ToUpper(method), Path: path, Item: item, PathItem: pathItem}
}

// allOperations 返回 openapi.yaml 中声明的全部 HTTP 操作。
func (s *openAPISpec) allOperations(t *testing.T) []*apiOperation {
	t.Helper()

	paths, ok := asMap(s.doc["paths"])
	if !ok {
		t.Fatal("openapi.yaml 缺少 paths")
	}

	httpMethods := map[string]bool{
		"get": true, "post": true, "put": true, "patch": true,
		"delete": true, "head": true, "options": true, "trace": true,
	}

	var operations []*apiOperation
	for path, rawPathItem := range paths {
		pathItem, ok := asMap(rawPathItem)
		if !ok {
			continue
		}
		for method, rawItem := range pathItem {
			if !httpMethods[strings.ToLower(method)] {
				continue
			}
			item, ok := asMap(rawItem)
			if !ok {
				continue
			}
			operations = append(operations, &apiOperation{
				Method:   strings.ToUpper(method),
				Path:     path,
				Item:     item,
				PathItem: pathItem,
			})
		}
	}

	sort.Slice(operations, func(i, j int) bool {
		if operations[i].Path == operations[j].Path {
			return operations[i].Method < operations[j].Method
		}
		return operations[i].Path < operations[j].Path
	})
	return operations
}

// resolve 解析本文件内的 #/... 引用。
func (s *openAPISpec) resolve(ref string) (map[string]any, error) {
	if !strings.HasPrefix(ref, "#/") {
		return nil, fmt.Errorf("暂不支持外部引用 %q", ref)
	}

	var current any = s.doc
	for _, segment := range strings.Split(strings.TrimPrefix(ref, "#/"), "/") {
		segment = strings.ReplaceAll(segment, "~1", "/")
		segment = strings.ReplaceAll(segment, "~0", "~")

		mapping, ok := asMap(current)
		if !ok {
			return nil, fmt.Errorf("引用 %q 在 %q 处不是对象", ref, segment)
		}
		current, ok = mapping[segment]
		if !ok {
			return nil, fmt.Errorf("引用 %q 缺少 %q", ref, segment)
		}
	}

	result, ok := asMap(current)
	if !ok {
		return nil, fmt.Errorf("引用 %q 的目标不是对象", ref)
	}
	return result, nil
}

func (s *openAPISpec) deref(schema map[string]any) (map[string]any, error) {
	for i := 0; i < 16; i++ {
		ref := asString(schema["$ref"])
		if ref == "" {
			return schema, nil
		}
		next, err := s.resolve(ref)
		if err != nil {
			return nil, err
		}
		schema = next
	}
	return nil, errors.New("$ref 嵌套过深")
}

// mergedSchema 展开 allOf 并合并 required/properties，便于统一断言。
func (s *openAPISpec) mergedSchema(schema map[string]any, depth int) (map[string]any, error) {
	if depth > 16 {
		return nil, errors.New("schema 嵌套过深")
	}
	resolved, err := s.deref(schema)
	if err != nil {
		return nil, err
	}

	parts, hasAllOf := asSlice(resolved["allOf"])
	if !hasAllOf {
		return resolved, nil
	}

	out := make(map[string]any)
	properties := make(map[string]any)
	var required []any

	for _, rawPart := range parts {
		part, ok := asMap(rawPart)
		if !ok {
			return nil, errors.New("allOf 元素不是对象")
		}
		mergedPart, err := s.mergedSchema(part, depth+1)
		if err != nil {
			return nil, err
		}
		for key, value := range mergedPart {
			switch key {
			case "properties":
				for name, property := range mustMap(value) {
					properties[name] = property
				}
			case "required":
				if list, ok := asSlice(value); ok {
					required = append(required, list...)
				}
			default:
				out[key] = value
			}
		}
	}

	for key, value := range resolved {
		if key == "allOf" {
			continue
		}
		switch key {
		case "properties":
			for name, property := range mustMap(value) {
				properties[name] = property
			}
		case "required":
			if list, ok := asSlice(value); ok {
				required = append(required, list...)
			}
		default:
			out[key] = value
		}
	}

	if len(properties) > 0 {
		out["properties"] = properties
	}
	if len(required) > 0 {
		out["required"] = required
	}
	return out, nil
}

func mustMap(value any) map[string]any {
	mapping, _ := asMap(value)
	return mapping
}

func (s *openAPISpec) collectRequired(schema map[string]any, set map[string]bool, depth int) {
	if depth > 16 || schema == nil {
		return
	}
	resolved, err := s.deref(schema)
	if err != nil {
		return
	}
	if required, ok := asSlice(resolved["required"]); ok {
		for _, field := range required {
			if name, ok := field.(string); ok {
				set[name] = true
			}
		}
	}
	if parts, ok := asSlice(resolved["allOf"]); ok {
		for _, rawPart := range parts {
			if part, ok := asMap(rawPart); ok {
				s.collectRequired(part, set, depth+1)
			}
		}
	}
}

// requiredFields 返回 schema（含 allOf）声明的全部必填字段名。
func (s *openAPISpec) requiredFields(schema map[string]any) []string {
	set := make(map[string]bool)
	s.collectRequired(schema, set, 0)

	fields := make([]string, 0, len(set))
	for field := range set {
		fields = append(fields, field)
	}
	sort.Strings(fields)
	return fields
}

// requestSchema 返回 application/json 请求体 schema。
func (s *openAPISpec) requestSchema(t *testing.T, op *apiOperation) (map[string]any, bool) {
	t.Helper()

	rawBody, ok := op.Item["requestBody"]
	if !ok {
		return nil, false
	}
	body, ok := asMap(rawBody)
	if !ok {
		t.Fatalf("%s %s: requestBody 不是对象", op.Method, op.Path)
	}
	if ref := asString(body["$ref"]); ref != "" {
		resolved, err := s.resolve(ref)
		if err != nil {
			t.Fatalf("%s %s: 解析 requestBody 失败: %v", op.Method, op.Path, err)
		}
		body = resolved
	}

	content, ok := asMap(body["content"])
	if !ok {
		return nil, false
	}
	media, ok := asMap(content["application/json"])
	if !ok {
		return nil, false
	}
	schema, ok := asMap(media["schema"])
	if !ok {
		return nil, false
	}
	return schema, true
}

// parameters 合并 path item 与 operation 的参数声明。
func (s *openAPISpec) parameters(t *testing.T, op *apiOperation) []apiParameter {
	t.Helper()

	var rawParameters []any
	if list, ok := asSlice(op.PathItem["parameters"]); ok {
		rawParameters = append(rawParameters, list...)
	}
	if list, ok := asSlice(op.Item["parameters"]); ok {
		rawParameters = append(rawParameters, list...)
	}

	parameters := make([]apiParameter, 0, len(rawParameters))
	for _, raw := range rawParameters {
		parameter, ok := asMap(raw)
		if !ok {
			continue
		}
		if ref := asString(parameter["$ref"]); ref != "" {
			resolved, err := s.resolve(ref)
			if err != nil {
				t.Fatalf("%s %s: 解析参数引用失败: %v", op.Method, op.Path, err)
			}
			parameter = resolved
		}
		schema, _ := asMap(parameter["schema"])
		parameters = append(parameters, apiParameter{
			Name:     asString(parameter["name"]),
			In:       asString(parameter["in"]),
			Required: parameter["required"] == true,
			Schema:   schema,
		})
	}
	return parameters
}

func (s *openAPISpec) successStatuses(op *apiOperation) []int {
	responses, _ := asMap(op.Item["responses"])
	var statuses []int
	for code := range responses {
		status, err := strconv.Atoi(code)
		if err == nil && status >= 200 && status < 300 {
			statuses = append(statuses, status)
		}
	}
	sort.Ints(statuses)
	return statuses
}

func (s *openAPISpec) isSuccessStatus(op *apiOperation, status int) bool {
	for _, candidate := range s.successStatuses(op) {
		if candidate == status {
			return true
		}
	}
	return false
}

func (s *openAPISpec) hasResponseStatus(op *apiOperation, status int) bool {
	responses, _ := asMap(op.Item["responses"])
	_, ok := responses[strconv.Itoa(status)]
	return ok
}

// successJSONSchema 返回首个 2xx 响应中的 JSON schema；文件流或无响应体时返回 false。
func (s *openAPISpec) successJSONSchema(t *testing.T, op *apiOperation) (map[string]any, bool) {
	t.Helper()

	responses, _ := asMap(op.Item["responses"])
	for code, rawResponse := range responses {
		status, err := strconv.Atoi(code)
		if err != nil || status < 200 || status >= 300 {
			continue
		}
		response, ok := asMap(rawResponse)
		if !ok {
			continue
		}
		if ref := asString(response["$ref"]); ref != "" {
			resolved, err := s.resolve(ref)
			if err != nil {
				t.Fatalf("%s %s: 解析 %d 响应失败: %v", op.Method, op.Path, status, err)
			}
			response = resolved
		}
		content, ok := asMap(response["content"])
		if !ok {
			continue
		}
		media, ok := asMap(content["application/json"])
		if !ok {
			continue
		}
		schema, ok := asMap(media["schema"])
		if ok {
			return schema, true
		}
	}
	return nil, false
}

func (s *openAPISpec) errorResponseSchema(t *testing.T) map[string]any {
	t.Helper()

	components, ok := asMap(s.doc["components"])
	if !ok {
		t.Fatal("openapi.yaml 缺少 components")
	}
	schemas, ok := asMap(components["schemas"])
	if !ok {
		t.Fatal("openapi.yaml 缺少 components.schemas")
	}
	schema, ok := asMap(schemas["ErrorResponse"])
	if !ok {
		t.Fatal("openapi.yaml 缺少 components.schemas.ErrorResponse")
	}
	return schema
}

func (s *openAPISpec) errorCodeEnum(t *testing.T) []string {
	t.Helper()

	schema := s.errorResponseSchema(t)
	properties, _ := asMap(schema["properties"])
	codeSchema, _ := asMap(properties["code"])
	values, ok := asSlice(codeSchema["enum"])
	if !ok {
		t.Fatal("ErrorResponse.code 缺少 enum")
	}

	allowed := make([]string, 0, len(values))
	for _, value := range values {
		allowed = append(allowed, asString(value))
	}
	return allowed
}

// requiresBearer 依据 operation 级 security 判断是否需要 Bearer JWT。
func (s *openAPISpec) requiresBearer(op *apiOperation) bool {
	rawSecurity, ok := op.Item["security"]
	if !ok {
		rawSecurity = s.doc["security"]
	}

	security, ok := asSlice(rawSecurity)
	if !ok {
		return false
	}
	for _, rawRequirement := range security {
		requirement, ok := asMap(rawRequirement)
		if !ok {
			continue
		}
		if _, ok := requirement["BearerAuth"]; ok {
			return true
		}
	}
	return false
}

// fixtureValue 为样例值提供环境变量覆盖，便于对接真实夹具数据。
//
// 例如路径参数 event_id 可用 CONTRACT_FIXTURE_EVENT_ID 覆盖。
func fixtureValue(name string) string {
	key := "CONTRACT_FIXTURE_" + strings.ToUpper(strings.NewReplacer("-", "_", ".", "_").Replace(name))
	if value := strings.TrimSpace(os.Getenv(key)); value != "" {
		return value
	}
	return "contract-" + name
}

// sampleForSchema 按 schema 递归生成结构合法的请求样例值。
//
// 样例只保证满足 OpenAPI 的类型、枚举与必填约束，不保证引用真实的赛事/组别数据。
func (s *openAPISpec) sampleForSchema(schema map[string]any, name string, depth int) any {
	if depth > 16 {
		return fixtureValue(name)
	}

	resolved, err := s.mergedSchema(schema, depth)
	if err != nil {
		return fixtureValue(name)
	}

	if values, ok := asSlice(resolved["enum"]); ok && len(values) > 0 {
		return values[0]
	}

	switch asString(resolved["type"]) {
	case "object":
		object := make(map[string]any)
		properties, _ := asMap(resolved["properties"])
		required, _ := asSlice(resolved["required"])
		for _, rawField := range required {
			field, ok := rawField.(string)
			if !ok || field == "" {
				continue
			}
			property, _ := asMap(properties[field])
			object[field] = s.sampleForSchema(property, field, depth+1)
		}
		if len(object) == 0 {
			additional, exists := resolved["additionalProperties"]
			if !exists || additional != false {
				object["sample"] = "contract"
			}
		}
		return object

	case "array":
		items, ok := asMap(resolved["items"])
		if !ok {
			return []any{}
		}
		return []any{s.sampleForSchema(items, name, depth+1)}

	case "integer", "number":
		if minimum, ok := asInt(resolved["minimum"]); ok && minimum > 1 {
			return minimum
		}
		// 使用 1 而不是 0：Go 侧 binding:"required" 会把零值当作缺失。
		return 1

	case "boolean":
		return true

	case "string":
		if asString(resolved["format"]) == "date-time" {
			return dateTimeSample
		}
		return fixtureValue(name)

	default:
		return fixtureValue(name)
	}
}

// validateRequiredFields 递归检查 value 是否包含 schema 声明的全部 required 字段。
func (s *openAPISpec) validateRequiredFields(t *testing.T, schema map[string]any, value any) []string {
	t.Helper()

	var problems []string
	s.collectMissingFields(schema, value, "$", &problems, 0)
	return problems
}

func (s *openAPISpec) collectMissingFields(schema map[string]any, value any, path string, problems *[]string, depth int) {
	if depth > 16 {
		return
	}
	merged, err := s.mergedSchema(schema, depth)
	if err != nil {
		*problems = append(*problems, fmt.Sprintf("%s: 解析 schema 失败: %v", path, err))
		return
	}

	properties, _ := asMap(merged["properties"])
	required, _ := asSlice(merged["required"])
	if asString(merged["type"]) == "object" || len(required) > 0 {
		object, ok := value.(map[string]any)
		if !ok {
			*problems = append(*problems, fmt.Sprintf("%s: 期望 object，实际 %T", path, value))
			return
		}
		for _, rawField := range required {
			field, ok := rawField.(string)
			if !ok || field == "" {
				continue
			}
			fieldValue, exists := object[field]
			if !exists {
				*problems = append(*problems, fmt.Sprintf("%s.%s: 缺少 required 字段", path, field))
				continue
			}
			if fieldValue == nil {
				*problems = append(*problems, fmt.Sprintf("%s.%s: required 字段为 null", path, field))
				continue
			}
			if property, ok := asMap(properties[field]); ok {
				s.collectMissingFields(property, fieldValue, path+"."+field, problems, depth+1)
			}
		}
		return
	}

	if asString(merged["type"]) == "array" {
		array, ok := value.([]any)
		if !ok {
			*problems = append(*problems, fmt.Sprintf("%s: 期望 array，实际 %T", path, value))
			return
		}
		items, ok := asMap(merged["items"])
		if !ok {
			return
		}
		for index, item := range array {
			s.collectMissingFields(items, item, fmt.Sprintf("%s[%d]", path, index), problems, depth+1)
		}
	}
}

// decodeJSONObject 把响应体解析成 map，失败时给出截断后的原文。
func decodeJSONObject(t *testing.T, body []byte) map[string]any {
	t.Helper()

	var decoded map[string]any
	if err := json.Unmarshal(body, &decoded); err != nil {
		t.Fatalf("响应不是 JSON 对象: %v；响应体：%s", err, truncate(body))
	}
	return decoded
}

func truncate(body []byte) string {
	const limit = 600
	text := string(body)
	if len(text) <= limit {
		return text
	}
	return text[:limit] + "...(truncated)"
}
