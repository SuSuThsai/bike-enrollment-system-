//go:build integration

package integration_test

import (
	"context"
	"errors"
	"testing"

	enrollmentsvc "bike-enroll/server/internal/service/enrollment"
)

// TestIntegrationMyEnrollments 验证列表按用户隔离、详情仅本人可读。
func TestIntegrationMyEnrollments(t *testing.T) {
	env := newEnv(t)
	_, firstEnrollmentID, _ := seedMockPayOrder(t, env, 100)
	_, secondEnrollmentID, _ := seedMockPayOrder(t, env, 100)

	first := loadEnrollment(t, env, firstEnrollmentID)
	second := loadEnrollment(t, env, secondEnrollmentID)
	service := newEnrollmentService(env)
	ctx := context.Background()

	firstItems, err := service.ListMine(ctx, first.UserID)
	if err != nil {
		t.Fatalf("ListMine(first) 失败: %v", err)
	}
	if len(firstItems) != 1 || firstItems[0].ID != firstEnrollmentID {
		t.Fatalf("first 用户列表错误: %+v", firstItems)
	}

	secondItems, err := service.ListMine(ctx, second.UserID)
	if err != nil {
		t.Fatalf("ListMine(second) 失败: %v", err)
	}
	if len(secondItems) != 1 || secondItems[0].ID != secondEnrollmentID {
		t.Fatalf("second 用户列表错误: %+v", secondItems)
	}

	detail, err := service.Detail(ctx, first.UserID, firstEnrollmentID)
	if err != nil {
		t.Fatalf("本人详情读取失败: %v", err)
	}
	if detail.ID != firstEnrollmentID {
		t.Errorf("detail.ID = %q，期望 %q", detail.ID, firstEnrollmentID)
	}

	if _, err := service.Detail(ctx, second.UserID, firstEnrollmentID); !errors.Is(err, enrollmentsvc.ErrEnrollmentNotFound) {
		t.Fatalf("越权读取 err = %v，期望 ErrEnrollmentNotFound", err)
	}
}
