// Package main 是 API 进程入口。
package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"

	"bike-enroll/server/internal/config"
	adminhandler "bike-enroll/server/internal/handler/admin"
	authhandler "bike-enroll/server/internal/handler/auth"
	enrollmenthandler "bike-enroll/server/internal/handler/enrollment"
	eventhandler "bike-enroll/server/internal/handler/event"
	mockpayhandler "bike-enroll/server/internal/handler/mockpay"
	notificationhandler "bike-enroll/server/internal/handler/notification"
	paymenthandler "bike-enroll/server/internal/handler/payment"
	"bike-enroll/server/internal/job"
	"bike-enroll/server/internal/pkg/jwt"
	paymentpkg "bike-enroll/server/internal/pkg/payment"
	"bike-enroll/server/internal/pkg/wechat"
	subscribeclient "bike-enroll/server/internal/pkg/wechat/subscribe"
	"bike-enroll/server/internal/repository"
	adminrepo "bike-enroll/server/internal/repository/admin"
	enrollmentrepo "bike-enroll/server/internal/repository/enrollment"
	eventrepo "bike-enroll/server/internal/repository/event"
	lockrepo "bike-enroll/server/internal/repository/lock"
	notificationrepo "bike-enroll/server/internal/repository/notification"
	paymentrepo "bike-enroll/server/internal/repository/payment"
	userrepo "bike-enroll/server/internal/repository/user"
	"bike-enroll/server/internal/router"
	adminservice "bike-enroll/server/internal/service/admin"
	authservice "bike-enroll/server/internal/service/auth"
	enrollmentservice "bike-enroll/server/internal/service/enrollment"
	eventservice "bike-enroll/server/internal/service/event"
	notificationservice "bike-enroll/server/internal/service/notification"
	paymentservice "bike-enroll/server/internal/service/payment"
)

func main() {
	cfg := config.Load()
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: cfg.LogLevel}))
	slog.SetDefault(logger)

	if cfg.Env == "production" {
		gin.SetMode(gin.ReleaseMode)
	}

	db, err := repository.OpenMySQL(cfg.MySQL)
	if err != nil {
		logger.Error("mysql init failed", "error", err)
		os.Exit(1)
	}
	sqlDB, err := db.DB()
	if err != nil {
		logger.Error("mysql sql.DB init failed", "error", err)
		os.Exit(1)
	}
	defer sqlDB.Close()

	redisClient, err := repository.OpenRedis(cfg.Redis)
	if err != nil {
		logger.Error("redis init failed", "error", err)
		os.Exit(1)
	}
	defer func() {
		_ = redisClient.Close()
	}()

	tokenManager, err := jwt.NewManager(cfg.JWT.Secret, cfg.JWT.ExpireDuration())
	if err != nil {
		logger.Error("jwt init failed", "error", err)
		os.Exit(1)
	}

	if cfg.WeChat.AppID == "" || cfg.WeChat.Secret == "" {
		logger.Warn("wechat appid/secret not configured; wechat login will fail until configured")
	}

	users := userrepo.NewGORM(db)
	wechatClient := wechat.NewClient(cfg.WeChat.AppID, cfg.WeChat.Secret)
	loginService := authservice.New(users, wechatClient, tokenManager)
	loginHandler := authhandler.New(loginService)

	eventRepository := eventrepo.NewGORM(db)
	slotRepository := eventrepo.NewRedisSlots(redisClient)
	eventService := eventservice.New(eventRepository, slotRepository, logger)
	eventHandler := eventhandler.New(eventService)

	lockRepository := lockrepo.NewGORM(db)
	lockStore := lockrepo.NewRedisStore(redisClient)
	lockTTL := cfg.LockTTLDuration()
	enrollmentRepository := enrollmentrepo.NewGORM(db)
	enrollmentService := enrollmentservice.New(lockRepository, lockStore, enrollmentRepository, lockTTL, logger)
	enrollmentHandler := enrollmenthandler.New(enrollmentService, enrollmentService, enrollmentService)

	paymentRepository := paymentrepo.NewGORM(db)
	paymentProvider, err := paymentpkg.NewProvider(paymentpkg.Config{
		Mode:   cfg.PaymentMode,
		AppEnv: cfg.Env,
		WeChat: paymentpkg.WeChatConfig{
			AppID:            cfg.WeChat.AppID,
			MchID:            cfg.WeChatPay.MchID,
			SerialNo:         cfg.WeChatPay.SerialNo,
			NotifyURL:        cfg.WeChatPay.NotifyURL,
			PrivateKeyPath:   cfg.WeChatPay.PrivateKeyPath,
			APIv3Key:         cfg.WeChatPay.APIv3Key,
			PlatformCertPath: cfg.WeChatPay.PlatformCertPath,
		},
	}, logger)
	if err != nil {
		logger.Error("payment provider init failed", "error", err)
		os.Exit(1)
	}
	paymentService := paymentservice.New(paymentRepository, logger)
	paymentService.SetProvider(paymentProvider)
	paymentService.SetRefundRepository(paymentrepo.NewGORMRefund(db, redisClient))
	paymentHandler := paymenthandler.New(paymentProvider, paymentService)
	mockPayHandler := mockpayhandler.New(paymentService, cfg.MockPaySecret, logger)
	if cfg.PaymentMode == paymentpkg.ModeMock && cfg.MockPaySecret == "" {
		logger.Warn("MOCK_PAY_SECRET is empty; /internal/mock-pay endpoints will reject all requests")
	}

	notificationRepository := notificationrepo.NewGORM(db)
	var notificationSender notificationservice.Sender
	if client, err := subscribeclient.NewClient(subscribeclient.Config{
		AppID:  cfg.WeChat.AppID,
		Secret: cfg.WeChat.Secret,
	}); err != nil {
		logger.Warn("wechat subscribe client not configured; notifications will be skipped", "error", err)
	} else {
		notificationSender = client
	}
	notificationService := notificationservice.New(
		notificationRepository,
		notificationSender,
		cfg.WeChat.TemplateEnrollResult,
		logger,
	)
	paymentService.SetNotifier(notificationService)
	notificationHandler := notificationhandler.New(notificationService)

	adminRepository := adminrepo.NewGORM(db, redisClient)
	adminService := adminservice.New(adminRepository, logger)
	adminHandler := adminhandler.New(adminService)

	lockExpiryJob := job.NewLockExpiryJob(db, redisClient, logger)
	payTimeoutJob := job.NewPayTimeoutJob(
		db,
		redisClient,
		time.Duration(cfg.PayTimeoutMinutes)*time.Minute,
		logger,
	)
	reconcileJob := job.NewReconcileJob(db, redisClient, logger)
	scheduler := job.NewScheduler(lockExpiryJob, payTimeoutJob, reconcileJob, logger)
	defer func() {
		closeCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := notificationService.Close(closeCtx); err != nil {
			logger.Warn("notification service close failed", "error", err)
		}
	}()

	srv := &http.Server{
		Addr: cfg.HTTPAddr,
		Handler: router.New(
			logger,
			router.WithAuth(loginHandler),
			router.WithEvent(eventHandler),
			router.WithEnrollment(enrollmentHandler),
			router.WithPayment(paymentHandler),
			router.WithNotification(notificationHandler),
			router.WithAdmin(adminHandler),
			router.WithMockPay(mockPayHandler, cfg.PaymentMode == paymentpkg.ModeMock),
			router.WithTokenVerifier(tokenManager),
		),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	errCh := make(chan error, 1)
	go func() {
		logger.Info("http server starting", "addr", cfg.HTTPAddr, "env", cfg.Env)
		errCh <- srv.ListenAndServe()
	}()

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	go scheduler.Run(ctx)

	select {
	case err := <-errCh:
		if !errors.Is(err, http.ErrServerClosed) {
			logger.Error("http server failed", "error", err)
			os.Exit(1)
		}
	case <-ctx.Done():
		logger.Info("shutdown signal received")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := srv.Shutdown(shutdownCtx); err != nil {
			logger.Error("graceful shutdown failed", "error", err)
			os.Exit(1)
		}
		logger.Info("http server stopped")
	}
}
