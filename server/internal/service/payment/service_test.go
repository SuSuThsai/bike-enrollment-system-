package payment_test

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"

	"bike-enroll/server/internal/model"
	paymentpkg "bike-enroll/server/internal/pkg/payment"
	paymentrepo "bike-enroll/server/internal/repository/payment"
	paymentsvc "bike-enroll/server/internal/service/payment"
)

type fakeRepository struct {
	order    *model.Order
	user     *model.User
	err      error
	outcome  paymentrepo.NotifyOutcome
	applyErr error

	applyCalled bool
	params      paymentrepo.NotifyParams
}

func (f *fakeRepository) GetOrder(context.Context, string) (*model.Order, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.order, nil
}

func (f *fakeRepository) GetOrderWithUser(context.Context, string) (*model.Order, *model.User, error) {
	if f.err != nil {
		return nil, nil, f.err
	}
	return f.order, f.user, nil
}

func (f *fakeRepository) ApplyNotify(_ context.Context, params paymentrepo.NotifyParams) (paymentrepo.NotifyOutcome, error) {
	f.applyCalled = true
	f.params = params
	return f.outcome, f.applyErr
}

type fakeNotifier struct {
	orderIDs []string
}

func (f *fakeNotifier) EnqueueEnrollmentSuccess(orderID, _ string) {
	f.orderIDs = append(f.orderIDs, orderID)
}

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil))
}

func newService(repo *fakeRepository, logger *slog.Logger) *paymentsvc.Service {
	if logger == nil {
		logger = discardLogger()
	}
	return paymentsvc.New(repo, logger)
}

func TestPreparePrepayBuildsRequest(t *testing.T) {
	order := &model.Order{
		ID:     "order-1",
		UserID: "user-1",
		Amount: 10000,
		Status: "pending_payment",
	}
	user := &model.User{ID: "user-1", OpenID: "openid-1"}
	service := newService(&fakeRepository{order: order, user: user}, nil)

	request, err := service.PreparePrepay(context.Background(), "user-1", "order-1")
	if err != nil {
		t.Fatalf("PreparePrepay 失败: %v", err)
	}
	if request.OutTradeNo != order.ID {
		t.Errorf("out_trade_no = %q，期望 %q", request.OutTradeNo, order.ID)
	}
	if request.AmountTotal != order.Amount {
		t.Errorf("amount_total = %d，期望 %d", request.AmountTotal, order.Amount)
	}
	if request.OpenID != user.OpenID {
		t.Errorf("openid = %q，期望 %q", request.OpenID, user.OpenID)
	}
	if !strings.Contains(request.Description, order.ID) {
		t.Errorf("description = %q，期望包含订单号", request.Description)
	}
}

func TestPreparePrepayRejectsNonPendingOrder(t *testing.T) {
	order := &model.Order{ID: "order-1", UserID: "user-1", Amount: 100, Status: "paid"}
	user := &model.User{ID: "user-1", OpenID: "openid-1"}
	service := newService(&fakeRepository{order: order, user: user}, nil)

	_, err := service.PreparePrepay(context.Background(), "user-1", "order-1")
	if !errors.Is(err, paymentsvc.ErrOrderStatusInvalid) {
		t.Fatalf("err = %v，期望 ErrOrderStatusInvalid", err)
	}
}

func TestPreparePrepayRejectsOtherUserOrder(t *testing.T) {
	order := &model.Order{ID: "order-1", UserID: "user-2", Amount: 100, Status: "pending_payment"}
	user := &model.User{ID: "user-2", OpenID: "openid-2"}
	service := newService(&fakeRepository{order: order, user: user}, nil)

	_, err := service.PreparePrepay(context.Background(), "user-1", "order-1")
	if !errors.Is(err, paymentsvc.ErrOrderNotFound) {
		t.Fatalf("err = %v，期望 ErrOrderNotFound", err)
	}
}

func TestPreparePrepayOrderNotFound(t *testing.T) {
	service := newService(&fakeRepository{err: paymentrepo.ErrOrderNotFound}, nil)

	_, err := service.PreparePrepay(context.Background(), "user-1", "missing")
	if !errors.Is(err, paymentsvc.ErrOrderNotFound) {
		t.Fatalf("err = %v，期望 ErrOrderNotFound", err)
	}
}

func TestHandleNotifyAppliesPayment(t *testing.T) {
	repo := &fakeRepository{outcome: paymentrepo.NotifyApplied}
	service := newService(repo, nil)
	notifier := &fakeNotifier{}
	service.SetNotifier(notifier)

	result := &paymentpkg.NotifyResult{
		EventType:     "TRANSACTION.SUCCESS",
		OutTradeNo:    "order-1",
		TransactionID: "wx-transaction-1",
		TradeState:    "SUCCESS",
		Amount:        paymentpkg.NotifyAmount{Total: 10000, Currency: "CNY"},
	}
	if err := service.HandleNotify(context.Background(), result); err != nil {
		t.Fatalf("HandleNotify 失败: %v", err)
	}

	if !repo.applyCalled {
		t.Fatal("HandleNotify 必须调用 ApplyNotify")
	}
	if repo.params.OutTradeNo != "order-1" || repo.params.TransactionID != "wx-transaction-1" ||
		repo.params.AmountTotal != 10000 {
		t.Errorf("ApplyNotify 参数错误: %+v", repo.params)
	}
	if len(notifier.orderIDs) != 1 || notifier.orderIDs[0] != "order-1" {
		t.Errorf("支付成功应投递一次通知: %+v", notifier.orderIDs)
	}
}

func TestHandleNotifyIgnoresNonSuccessEvent(t *testing.T) {
	repo := &fakeRepository{outcome: paymentrepo.NotifyApplied}
	service := newService(repo, nil)

	result := &paymentpkg.NotifyResult{EventType: "REFUND.SUCCESS", OutTradeNo: "order-1"}
	if err := service.HandleNotify(context.Background(), result); err != nil {
		t.Fatalf("非成功事件应返回成功: %v", err)
	}
	if repo.applyCalled {
		t.Error("非 TRANSACTION.SUCCESS 事件不应触发状态迁移")
	}
}

func TestHandleNotifyIgnoresNonSuccessTradeState(t *testing.T) {
	repo := &fakeRepository{outcome: paymentrepo.NotifyApplied}
	service := newService(repo, nil)

	result := &paymentpkg.NotifyResult{
		EventType:  "TRANSACTION.SUCCESS",
		OutTradeNo: "order-1",
		TradeState: "CLOSED",
	}
	if err := service.HandleNotify(context.Background(), result); err != nil {
		t.Fatalf("非 SUCCESS 交易应返回成功: %v", err)
	}
	if repo.applyCalled {
		t.Error("非 SUCCESS 交易不应触发状态迁移")
	}
}

func TestHandleNotifyRejectsMissingTransactionID(t *testing.T) {
	repo := &fakeRepository{outcome: paymentrepo.NotifyApplied}
	service := newService(repo, nil)

	result := &paymentpkg.NotifyResult{
		EventType:  "TRANSACTION.SUCCESS",
		OutTradeNo: "order-1",
		TradeState: "SUCCESS",
	}
	err := service.HandleNotify(context.Background(), result)
	if !errors.Is(err, paymentsvc.ErrInvalidNotification) {
		t.Fatalf("err = %v，期望 ErrInvalidNotification", err)
	}
	if repo.applyCalled {
		t.Error("缺少 transaction_id 时不应触发状态迁移")
	}
}

func TestHandleNotifyAmountMismatchLogsAlert(t *testing.T) {
	var logs bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
	repo := &fakeRepository{outcome: paymentrepo.NotifyAmountMismatch}
	service := newService(repo, logger)
	notifier := &fakeNotifier{}
	service.SetNotifier(notifier)

	result := &paymentpkg.NotifyResult{
		EventType:     "TRANSACTION.SUCCESS",
		OutTradeNo:    "order-1",
		TransactionID: "wx-transaction-1",
		TradeState:    "SUCCESS",
		Amount:        paymentpkg.NotifyAmount{Total: 9999, Currency: "CNY"},
	}
	if err := service.HandleNotify(context.Background(), result); err != nil {
		t.Fatalf("金额不符应返回成功: %v", err)
	}
	if !strings.Contains(logs.String(), "PAY_AMOUNT_MISMATCH") {
		t.Errorf("缺少金额不符告警日志: %s", logs.String())
	}
	if len(notifier.orderIDs) != 0 {
		t.Errorf("金额不符不应投递通知: %+v", notifier.orderIDs)
	}
}
