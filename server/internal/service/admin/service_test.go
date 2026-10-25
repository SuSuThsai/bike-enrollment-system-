package admin_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"bike-enroll/server/internal/model"
	adminrepo "bike-enroll/server/internal/repository/admin"
	adminsvc "bike-enroll/server/internal/service/admin"
)

type fakeRepository struct {
	exportData *adminrepo.ExportData
	exportErr  error
	filter     adminrepo.ExportFilter

	rebuildForce      bool
	rebuildResult     *adminrepo.RemainingRebuildResult
	rebuildErr        error
	deleteGroupResult *adminrepo.GroupDeleteResult
	deleteGroupErr    error
	deleteEventResult *adminrepo.EventDeleteResult
	deleteEventErr    error
}

func (f *fakeRepository) DeleteGroup(context.Context, string) (*adminrepo.GroupDeleteResult, error) {
	if f.deleteGroupErr != nil {
		return nil, f.deleteGroupErr
	}
	if f.deleteGroupResult != nil {
		return f.deleteGroupResult, nil
	}
	return &adminrepo.GroupDeleteResult{}, nil
}

func (f *fakeRepository) DeleteEvent(context.Context, string) (*adminrepo.EventDeleteResult, error) {
	if f.deleteEventErr != nil {
		return nil, f.deleteEventErr
	}
	if f.deleteEventResult != nil {
		return f.deleteEventResult, nil
	}
	return &adminrepo.EventDeleteResult{}, nil
}

func (f *fakeRepository) RebuildRemaining(_ context.Context, groupID string, force bool) (*adminrepo.RemainingRebuildResult, error) {
	f.rebuildForce = force
	if f.rebuildErr != nil {
		return nil, f.rebuildErr
	}
	if f.rebuildResult != nil {
		return f.rebuildResult, nil
	}
	return &adminrepo.RemainingRebuildResult{GroupID: groupID, Mode: "created"}, nil
}

func (f *fakeRepository) ReleaseHealth(_ context.Context, eventID string) (*adminrepo.ReleaseHealthData, error) {
	return &adminrepo.ReleaseHealthData{EventID: eventID}, nil
}

func (f *fakeRepository) CloseRegistration(_ context.Context, eventID, reason string, now time.Time) (*model.Event, error) {
	event := &model.Event{ID: eventID, LifecycleStatus: model.LifecyclePublished, RegistrationManuallyClosed: true}
	if reason != "" {
		event.RegistrationClosedReason = &reason
	}
	return event, nil
}

func (f *fakeRepository) OpenRegistration(_ context.Context, eventID string, now time.Time) (*model.Event, error) {
	return &model.Event{ID: eventID, LifecycleStatus: model.LifecyclePublished}, nil
}

func (f *fakeRepository) UpdateGroup(context.Context, adminrepo.UpdateGroupParams) (*adminrepo.GroupUpdateResult, error) {
	return nil, errors.New("not used")
}

func (f *fakeRepository) CreateEvent(context.Context, adminrepo.CreateEventParams) (*model.Event, error) {
	return nil, errors.New("not used")
}

func (f *fakeRepository) UpdateEvent(context.Context, adminrepo.UpdateEventParams) (*model.Event, error) {
	return nil, errors.New("not used")
}

func (f *fakeRepository) EventDetail(context.Context, string) (*adminrepo.EventDetailData, error) {
	return nil, errors.New("not used")
}

func (f *fakeRepository) CreateGroup(context.Context, adminrepo.CreateGroupParams) (*adminrepo.GroupUpdateResult, error) {
	return nil, errors.New("not used")
}

func (f *fakeRepository) ListEnrollments(context.Context, adminrepo.EnrollmentFilter) (*adminrepo.EnrollmentPage, error) {
	return nil, errors.New("not used")
}

func (f *fakeRepository) StatsOverview(context.Context, string) (*adminrepo.StatsOverviewData, error) {
	return nil, errors.New("not used")
}

func (f *fakeRepository) ExportEnrollments(_ context.Context, filter adminrepo.ExportFilter) (*adminrepo.ExportData, error) {
	f.filter = filter
	return f.exportData, f.exportErr
}

func TestExportCapsAt10000Rows(t *testing.T) {
	rows := make([]adminrepo.ExportRow, 10001)
	for i := range rows {
		rows[i] = adminrepo.ExportRow{
			EnrollmentID: "enrollment",
			UserID:       "user",
			EventID:      "event",
			GroupID:      "group",
			OrderID:      "order",
			Status:       "paid",
			CreatedAt:    time.Now(),
			FormData:     map[string]any{"name": "test"},
		}
	}
	repo := &fakeRepository{exportData: &adminrepo.ExportData{
		Rows:      rows,
		Total:     12000,
		Truncated: true,
	}}
	service := adminsvc.New(repo, slog.New(slog.NewTextHandler(io.Discard, nil)))

	result, err := service.Export(context.Background(), "admin-1", adminrepo.ExportFilter{
		EventID: "event-1",
	})
	if err != nil {
		t.Fatalf("Export 失败: %v", err)
	}
	if repo.filter.Limit != adminsvc.MaxExportRows {
		t.Errorf("repository limit = %d，期望 %d", repo.filter.Limit, adminsvc.MaxExportRows)
	}
	if result.Rows != adminsvc.MaxExportRows || !result.Truncated {
		t.Errorf("导出结果 = rows:%d truncated:%v，期望 10000/true", result.Rows, result.Truncated)
	}
	if len(result.Data) == 0 {
		t.Error("xlsx 内容为空")
	}
	if len(result.Fields) == 0 {
		t.Error("默认导出字段为空")
	}
}

func TestExportRejectsUnknownField(t *testing.T) {
	service := adminsvc.New(&fakeRepository{}, nil)
	_, err := service.Export(context.Background(), "admin-1", adminrepo.ExportFilter{
		EventID: "event-1",
		Fields:  []string{"unknown_field"},
	})
	if !errors.Is(err, adminsvc.ErrInvalidArgument) {
		t.Fatalf("err = %v，期望 ErrInvalidArgument", err)
	}
}

func TestRebuildGroupRemainingPassesForceFlag(t *testing.T) {
	repo := &fakeRepository{rebuildResult: &adminrepo.RemainingRebuildResult{
		GroupID: "group-1", TotalSlots: 100, Expected: 100, Mode: "created",
	}}
	service := adminsvc.New(repo, slog.New(slog.NewTextHandler(io.Discard, nil)))

	result, err := service.RebuildGroupRemaining(context.Background(), "admin-1", "group-1", true)
	if err != nil {
		t.Fatalf("RebuildGroupRemaining 失败: %v", err)
	}
	if !repo.rebuildForce {
		t.Error("service 未把 force=true 透传给仓储")
	}
	if result.Mode != "created" || result.Expected != 100 {
		t.Errorf("重建结果异常: %+v", result)
	}
}

func TestLifecycleRejectsEmptyIDs(t *testing.T) {
	service := adminsvc.New(&fakeRepository{}, nil)
	ctx := context.Background()

	if _, err := service.DeleteEvent(ctx, "admin-1", "  "); !errors.Is(err, adminsvc.ErrInvalidArgument) {
		t.Errorf("DeleteEvent 空 event_id: err = %v，期望 ErrInvalidArgument", err)
	}
	if _, err := service.DeleteGroup(ctx, "admin-1", ""); !errors.Is(err, adminsvc.ErrInvalidArgument) {
		t.Errorf("DeleteGroup 空 group_id: err = %v，期望 ErrInvalidArgument", err)
	}
	if _, err := service.RebuildGroupRemaining(ctx, "admin-1", "", false); !errors.Is(err, adminsvc.ErrInvalidArgument) {
		t.Errorf("RebuildGroupRemaining 空 group_id: err = %v，期望 ErrInvalidArgument", err)
	}
}

func TestUpdateGroupRejectsInvalidTotalSlots(t *testing.T) {
	service := adminsvc.New(&fakeRepository{}, nil)
	total := 0
	_, err := service.UpdateGroup(context.Background(), adminrepo.UpdateGroupParams{
		GroupID:    "group-1",
		TotalSlots: &total,
	})
	if !errors.Is(err, adminsvc.ErrInvalidArgument) {
		t.Fatalf("err = %v，期望 ErrInvalidArgument", err)
	}
}
