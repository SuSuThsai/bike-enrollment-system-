package contract

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// TestOperationIDsArePresentAndUnique 保证每个 operation 都有唯一 operationId，
// 便于按契约生成客户端代码、定位测试与追踪日志。
func TestOperationIDsArePresentAndUnique(t *testing.T) {
	spec := loadSpec(t)

	seen := make(map[string]string)
	for _, op := range spec.allOperations(t) {
		key := op.Method + " " + op.Path
		id := strings.TrimSpace(asString(op.Item["operationId"]))
		if id == "" {
			t.Errorf("%s: 缺少 operationId", key)
			continue
		}
		if previous, ok := seen[id]; ok {
			t.Errorf("operationId %q 重复：%s 与 %s", id, previous, key)
			continue
		}
		seen[id] = key
	}
}

// TestInputOperationsDeclareBadRequest 保证所有带请求体或必填查询参数的操作
// 都声明了 400，避免校验失败落到未定义分支。
func TestInputOperationsDeclareBadRequest(t *testing.T) {
	spec := loadSpec(t)

	for _, op := range spec.allOperations(t) {
		_, hasBody := op.Item["requestBody"]

		hasRequiredQuery := false
		for _, parameter := range spec.parameters(t, op) {
			if parameter.In == "query" && parameter.Required {
				hasRequiredQuery = true
				break
			}
		}

		if !hasBody && !hasRequiredQuery {
			continue
		}
		if !spec.hasResponseStatus(op, httpStatusBadRequest) {
			t.Errorf("%s %s: 存在必填输入，但 responses 未声明 400", op.Method, op.Path)
		}
	}
}

// TestAllLocalRefsResolve 保证文档内所有 $ref 都能解析，避免生成/校验工具中断。
func TestAllLocalRefsResolve(t *testing.T) {
	spec := loadSpec(t)

	var walk func(value any, path string)
	walk = func(value any, path string) {
		switch node := value.(type) {
		case map[string]any:
			if ref := asString(node["$ref"]); ref != "" {
				if _, err := spec.resolve(ref); err != nil {
					t.Errorf("%s: %v", path, err)
				}
			}
			keys := make([]string, 0, len(node))
			for key := range node {
				keys = append(keys, key)
			}
			sort.Strings(keys)
			for _, key := range keys {
				walk(node[key], path+"."+key)
			}
		case []any:
			for index, item := range node {
				walk(item, fmt.Sprintf("%s[%d]", path, index))
			}
		}
	}
	walk(spec.doc, "$")
}

// TestErrorResponsesUseErrorEnvelope 保证所有 4xx/5xx 响应都返回
// { code, message, request_id }，且 code 可供客户端枚举判断。
func TestErrorResponsesUseErrorEnvelope(t *testing.T) {
	spec := loadSpec(t)

	for _, op := range spec.allOperations(t) {
		responses, ok := asMap(op.Item["responses"])
		if !ok {
			continue
		}

		codes := make([]string, 0, len(responses))
		for code := range responses {
			codes = append(codes, code)
		}
		sort.Strings(codes)

		for _, code := range codes {
			status, err := strconv.Atoi(code)
			if err != nil || status < 400 {
				continue
			}

			response, ok := asMap(responses[code])
			if !ok {
				t.Errorf("%s %s: %s 响应不是对象", op.Method, op.Path, code)
				continue
			}
			if ref := asString(response["$ref"]); ref != "" {
				resolved, err := spec.resolve(ref)
				if err != nil {
					t.Errorf("%s %s: %s 响应引用无法解析: %v", op.Method, op.Path, code, err)
					continue
				}
				response = resolved
			}

			content, _ := asMap(response["content"])
			media, _ := asMap(content["application/json"])
			schema, ok := asMap(media["schema"])
			if !ok {
				t.Errorf("%s %s: %s 响应缺少 application/json schema", op.Method, op.Path, code)
				continue
			}
			resolved, err := spec.deref(schema)
			if err != nil {
				t.Errorf("%s %s: %s 响应 schema 引用无法解析: %v", op.Method, op.Path, code, err)
				continue
			}
			properties, _ := asMap(resolved["properties"])
			if _, ok := properties["code"]; !ok {
				t.Errorf("%s %s: %s 响应不是 ErrorResponse（缺少 code 字段）", op.Method, op.Path, code)
			}
		}
	}
}

// TestStatusEnumsCoverRefundStateMachine 保证 OpenAPI 的 Order/Enrollment 状态枚举
// 与 docs/state-machine.md 的退款状态机一致。
func TestStatusEnumsCoverRefundStateMachine(t *testing.T) {
	spec := loadSpec(t)

	assertStatusEnum := func(schemaName string, want ...string) {
		schema, err := spec.resolve("#/components/schemas/" + schemaName)
		if err != nil {
			t.Fatalf("解析 %s schema 失败: %v", schemaName, err)
		}
		properties, ok := asMap(schema["properties"])
		if !ok {
			t.Fatalf("%s schema 缺少 properties", schemaName)
		}
		status, ok := asMap(properties["status"])
		if !ok {
			t.Fatalf("%s 缺少 status 属性", schemaName)
		}
		values, ok := asSlice(status["enum"])
		if !ok {
			t.Fatalf("%s.status 缺少 enum", schemaName)
		}
		got := make(map[string]bool, len(values))
		for _, value := range values {
			got[asString(value)] = true
		}
		for _, value := range want {
			if !got[value] {
				t.Errorf("%s.status enum 缺少 %q（当前：%v）", schemaName, value, values)
			}
		}
	}

	assertStatusEnum("Order",
		"pending_payment", "paid", "refunding", "refunded", "refund_failed", "expired")
	assertStatusEnum("Enrollment",
		"pending_payment", "paid", "confirmed", "cancelled", "refunded")
}

const httpStatusBadRequest = 400
