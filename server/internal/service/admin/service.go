// Package admin 实现管理后台组别名额调整与报名导出业务逻辑。
package admin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/xuri/excelize/v2"

	"bike-enroll/server/internal/model"
	adminrepo "bike-enroll/server/internal/repository/admin"
	"bike-enroll/server/internal/requestid"
)

// MaxExportRows 是单次导出硬上限。
const MaxExportRows = 10000

var (
	ErrInvalidArgument = errors.New("invalid argument")
	ErrEventNotFound   = errors.New("event not found")
)

var allowedExportFields = []string{
	"enrollment_id",
	"user_id",
	"event_id",
	"group_id",
	"order_id",
	"status",
	"created_at",
	"form_data",
}

// ExportResult 是导出文件与元数据。
type ExportResult struct {
	Data      []byte
	Rows      int
	Total     int64
	Truncated bool
	Fields    []string
}

// Service 聚合管理后台仓储。
type Service struct {
	repo   adminrepo.Repository
	logger *slog.Logger
}

// New 创建管理后台服务。
func New(repo adminrepo.Repository, logger *slog.Logger) *Service {
	if logger == nil {
		logger = slog.Default()
	}
	return &Service{repo: repo, logger: logger}
}

// UpdateGroup 校验并更新组别。
func (s *Service) UpdateGroup(ctx context.Context, params adminrepo.UpdateGroupParams) (*adminrepo.GroupUpdateResult, error) {
	if params.GroupID == "" {
		return nil, ErrInvalidArgument
	}
	if params.TotalSlots != nil && *params.TotalSlots < 1 {
		return nil, ErrInvalidArgument
	}
	if params.Price != nil && *params.Price < 0 {
		return nil, ErrInvalidArgument
	}
	if params.Now.IsZero() {
		params.Now = time.Now()
	}
	return s.repo.UpdateGroup(ctx, params)
}

// CreateEvent 创建赛事并返回详情。
func (s *Service) CreateEvent(ctx context.Context, params adminrepo.CreateEventParams) (*adminrepo.EventDetailData, error) {
	if strings.TrimSpace(params.Title) == "" || strings.TrimSpace(params.Location) == "" ||
		strings.TrimSpace(params.Description) == "" {
		return nil, ErrInvalidArgument
	}
	if params.RegistrationOpenAt.IsZero() || params.RegistrationDeadline.IsZero() ||
		params.StartTime.IsZero() || params.EndTime.IsZero() {
		return nil, ErrInvalidArgument
	}
	if err := model.ValidateEventTimeRange(
		params.RegistrationOpenAt, params.RegistrationDeadline, params.StartTime, params.EndTime,
	); err != nil {
		return nil, err
	}
	if params.Now.IsZero() {
		params.Now = time.Now()
	}
	event, err := s.repo.CreateEvent(ctx, params)
	if err != nil {
		return nil, err
	}
	return s.repo.EventDetail(ctx, event.ID)
}

// UpdateEvent 更新赛事并返回详情。
func (s *Service) UpdateEvent(ctx context.Context, params adminrepo.UpdateEventParams) (*adminrepo.EventDetailData, error) {
	if params.EventID == "" {
		return nil, ErrInvalidArgument
	}
	if params.Title != nil && strings.TrimSpace(*params.Title) == "" {
		return nil, ErrInvalidArgument
	}
	if params.Location != nil && strings.TrimSpace(*params.Location) == "" {
		return nil, ErrInvalidArgument
	}
	if params.Description != nil && strings.TrimSpace(*params.Description) == "" {
		return nil, ErrInvalidArgument
	}
	if params.StartTime != nil && params.StartTime.IsZero() {
		return nil, ErrInvalidArgument
	}
	if params.EndTime != nil && params.EndTime.IsZero() {
		return nil, ErrInvalidArgument
	}
	if params.RegistrationOpenAt != nil && params.RegistrationOpenAt.IsZero() {
		return nil, ErrInvalidArgument
	}
	if params.RegistrationDeadline != nil && params.RegistrationDeadline.IsZero() {
		return nil, ErrInvalidArgument
	}
	if params.Now.IsZero() {
		params.Now = time.Now()
	}
	if _, err := s.repo.UpdateEvent(ctx, params); err != nil {
		if errors.Is(err, adminrepo.ErrEventNotFound) {
			return nil, ErrEventNotFound
		}
		return nil, err
	}
	return s.EventDetail(ctx, params.EventID)
}

// EventDetail 返回赛事详情。
func (s *Service) EventDetail(ctx context.Context, eventID string) (*adminrepo.EventDetailData, error) {
	if eventID == "" {
		return nil, ErrInvalidArgument
	}
	result, err := s.repo.EventDetail(ctx, eventID)
	if errors.Is(err, adminrepo.ErrEventNotFound) {
		return nil, ErrEventNotFound
	}
	return result, err
}

// CreateGroup 创建组别并初始化 Redis。
func (s *Service) CreateGroup(ctx context.Context, params adminrepo.CreateGroupParams) (*adminrepo.GroupUpdateResult, error) {
	if params.EventID == "" || strings.TrimSpace(params.Name) == "" ||
		params.Price < 0 || params.TotalSlots < 1 || params.StartTime.IsZero() ||
		params.FormSchema == nil || strings.TrimSpace(params.DisclaimerVersion) == "" {
		return nil, ErrInvalidArgument
	}
	if params.Now.IsZero() {
		params.Now = time.Now()
	}
	result, err := s.repo.CreateGroup(ctx, params)
	if errors.Is(err, adminrepo.ErrEventNotFound) {
		return nil, ErrEventNotFound
	}
	return result, err
}

// ListEnrollments 分页查询并按敏感字段脱敏。
func (s *Service) ListEnrollments(ctx context.Context, filter adminrepo.EnrollmentFilter) (*adminrepo.EnrollmentPage, error) {
	if filter.Status != "" {
		if _, ok := enrollmentStatuses[filter.Status]; !ok {
			return nil, ErrInvalidArgument
		}
	}
	if filter.Page <= 0 || filter.PageSize < 1 || filter.PageSize > 200 {
		return nil, ErrInvalidArgument
	}
	page, err := s.repo.ListEnrollments(ctx, filter)
	if err != nil {
		return nil, err
	}
	for index := range page.Items {
		page.Items[index].FormData = maskFormData(page.Items[index].FormData)
	}
	return page, nil
}

// StatsOverview 返回与对账等式一致的赛事统计。
func (s *Service) StatsOverview(ctx context.Context, eventID string) (*adminrepo.StatsOverviewData, error) {
	if eventID == "" {
		return nil, ErrInvalidArgument
	}
	result, err := s.repo.StatsOverview(ctx, eventID)
	if errors.Is(err, adminrepo.ErrEventNotFound) {
		return nil, ErrEventNotFound
	}
	return result, err
}

var enrollmentStatuses = map[string]struct{}{
	"pending_payment": {},
	"paid":            {},
	"confirmed":       {},
	"cancelled":       {},
	"refunded":        {},
}

func maskFormData(data map[string]any) map[string]any {
	if data == nil {
		return nil
	}
	masked := make(map[string]any, len(data))
	for key, value := range data {
		if isSensitiveField(key) {
			masked[key] = "***"
			continue
		}
		if nested, ok := value.(map[string]any); ok {
			masked[key] = maskFormData(nested)
			continue
		}
		masked[key] = value
	}
	return masked
}

func isSensitiveField(key string) bool {
	normalized := strings.ToLower(key)
	for _, marker := range []string{
		"id_card", "idcard", "id_number", "idnumber",
		"phone", "mobile", "medical", "health",
		"passport", "bank_card", "bankcard", "email",
	} {
		if strings.Contains(normalized, marker) {
			return true
		}
	}
	return false
}

// Export 查询报名并生成 xlsx；最多导出 MaxExportRows 条。
func (s *Service) Export(ctx context.Context, adminID string, filter adminrepo.ExportFilter) (*ExportResult, error) {
	if filter.EventID == "" {
		return nil, ErrInvalidArgument
	}
	fields, err := normalizeExportFields(filter.Fields)
	if err != nil {
		return nil, err
	}
	filter.Limit = MaxExportRows

	data, err := s.repo.ExportEnrollments(ctx, filter)
	if err != nil {
		if errors.Is(err, adminrepo.ErrEventNotFound) {
			return nil, ErrEventNotFound
		}
		return nil, fmt.Errorf("export enrollments: %w", err)
	}

	rows := data.Rows
	truncated := data.Truncated || int64(len(rows)) < data.Total
	if len(rows) > MaxExportRows {
		rows = rows[:MaxExportRows]
		truncated = true
	}

	workbook, err := buildWorkbook(rows, fields)
	if err != nil {
		return nil, fmt.Errorf("build export workbook: %w", err)
	}

	s.logger.InfoContext(ctx, "admin export enrollments",
		"request_id", requestid.FromContext(ctx),
		"admin_id", adminID,
		"event_id", filter.EventID,
		"group_id", filter.GroupID,
		"rows", len(rows),
		"total", data.Total,
		"truncated", truncated,
		"fields", fields,
	)
	if truncated {
		s.logger.WarnContext(ctx, "admin export enrollments truncated",
			"request_id", requestid.FromContext(ctx),
			"admin_id", adminID,
			"event_id", filter.EventID,
			"limit", MaxExportRows,
			"total", data.Total,
		)
	}

	return &ExportResult{
		Data:      workbook,
		Rows:      len(rows),
		Total:     data.Total,
		Truncated: truncated,
		Fields:    fields,
	}, nil
}

func normalizeExportFields(raw []string) ([]string, error) {
	if len(raw) == 0 {
		return append([]string(nil), allowedExportFields...), nil
	}
	allowed := make(map[string]struct{}, len(allowedExportFields))
	for _, field := range allowedExportFields {
		allowed[field] = struct{}{}
	}

	fields := make([]string, 0, len(raw))
	seen := make(map[string]struct{}, len(raw))
	for _, value := range raw {
		field := strings.TrimSpace(value)
		if field == "" {
			continue
		}
		if _, ok := allowed[field]; !ok {
			return nil, ErrInvalidArgument
		}
		if _, ok := seen[field]; ok {
			continue
		}
		seen[field] = struct{}{}
		fields = append(fields, field)
	}
	if len(fields) == 0 {
		return nil, ErrInvalidArgument
	}
	return fields, nil
}

func buildWorkbook(rows []adminrepo.ExportRow, fields []string) ([]byte, error) {
	file := excelize.NewFile()
	defer file.Close()
	if err := file.SetSheetName("Sheet1", "Enrollments"); err != nil {
		return nil, err
	}

	headers := make([]interface{}, 0, len(fields))
	for _, field := range fields {
		headers = append(headers, field)
	}
	if err := file.SetSheetRow("Enrollments", "A1", &headers); err != nil {
		return nil, err
	}

	for index, row := range rows {
		cells := make([]interface{}, 0, len(fields))
		for _, field := range fields {
			cells = append(cells, exportCell(row, field))
		}
		cell := fmt.Sprintf("A%d", index+2)
		if err := file.SetSheetRow("Enrollments", cell, &cells); err != nil {
			return nil, err
		}
	}
	buffer, err := file.WriteToBuffer()
	if err != nil {
		return nil, err
	}
	return buffer.Bytes(), nil
}

func exportCell(row adminrepo.ExportRow, field string) any {
	switch field {
	case "enrollment_id":
		return row.EnrollmentID
	case "user_id":
		return row.UserID
	case "event_id":
		return row.EventID
	case "group_id":
		return row.GroupID
	case "order_id":
		return row.OrderID
	case "status":
		return row.Status
	case "created_at":
		return row.CreatedAt.Format(time.RFC3339)
	case "form_data":
		data, err := json.Marshal(row.FormData)
		if err != nil {
			return ""
		}
		return string(data)
	default:
		return ""
	}
}
