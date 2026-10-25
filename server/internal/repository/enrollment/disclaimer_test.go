package enrollment

import (
	"reflect"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"

	"bike-enroll/server/internal/model"
)

// TestCreateDisclaimerSignatureInsertsOnly 验证签署记录只执行 INSERT，
// 没有 UPDATE/DELETE SQL；若 helper 增加变更语句，sqlmock 会报未预期调用。
func TestCreateDisclaimerSignatureInsertsOnly(t *testing.T) {
	sqlDB, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("创建 sqlmock 失败: %v", err)
	}
	defer sqlDB.Close()

	db, err := gorm.Open(mysql.New(mysql.Config{
		Conn:                      sqlDB,
		SkipInitializeWithVersion: true,
	}), &gorm.Config{SkipDefaultTransaction: true})
	if err != nil {
		t.Fatalf("打开 GORM 失败: %v", err)
	}

	now := time.Now()
	params := CreateParams{
		UserID:             "user-1",
		EnrollmentID:       "enrollment-1",
		GroupID:            "group-1",
		DisclaimerVersion:  "v1",
		DisclaimerSignedAt: now.Add(-time.Minute),
		ClientIP:           "127.0.0.1",
		UserAgent:          "unit-test-agent",
		Now:                now,
	}

	mock.ExpectExec("INSERT INTO .*disclaimer_signatures").
		WithArgs(
			sqlmock.AnyArg(), // id
			params.UserID,
			params.EnrollmentID,
			params.GroupID,
			params.DisclaimerVersion,
			sqlmock.AnyArg(), // signed_at
			params.ClientIP,
			params.UserAgent,
			sqlmock.AnyArg(), // created_at
		).
		WillReturnResult(sqlmock.NewResult(1, 1))

	if err := createDisclaimerSignature(db, params); err != nil {
		t.Fatalf("写入签署记录失败: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("签署记录写路径出现未预期 SQL（可能存在 UPDATE/DELETE）: %v", err)
	}
}

// TestDisclaimerSignatureModelIsInsertOnly 通过模型字段约束防止后续误加 updated_at，
// 配合 createDisclaimerSignature 只调用 tx.Create 保证无更新路径。
func TestDisclaimerSignatureModelIsInsertOnly(t *testing.T) {
	typ := reflect.TypeOf(model.DisclaimerSignature{})
	if _, ok := typ.FieldByName("UpdatedAt"); ok {
		t.Error("DisclaimerSignature 不应包含 UpdatedAt 字段")
	}
}
