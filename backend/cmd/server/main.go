package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/worryzyy/upstream-hub/internal/api"
	"github.com/worryzyy/upstream-hub/internal/auth"
	"github.com/worryzyy/upstream-hub/internal/channel"
	"github.com/worryzyy/upstream-hub/internal/config"
	"github.com/worryzyy/upstream-hub/internal/crypto"
	"github.com/worryzyy/upstream-hub/internal/logger"
	"github.com/worryzyy/upstream-hub/internal/monitor"
	"github.com/worryzyy/upstream-hub/internal/notify"
	"github.com/worryzyy/upstream-hub/internal/scheduler"
	"github.com/worryzyy/upstream-hub/internal/settings"
	"github.com/worryzyy/upstream-hub/internal/storage"
	"github.com/worryzyy/upstream-hub/internal/updater"
	"github.com/worryzyy/upstream-hub/internal/version"
	"github.com/worryzyy/upstream-hub/web"

	// 注册 connector 实现。
	_ "github.com/worryzyy/upstream-hub/internal/connector/newapi"
	_ "github.com/worryzyy/upstream-hub/internal/connector/sub2api"
)

func main() {
	configPath := flag.String("config", "", "path to config.yaml (optional; env vars also supported)")
	flag.Parse()

	cfg, err := config.Load(*configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "load config failed: %v\n", err)
		os.Exit(1)
	}

	log := logger.New(cfg.Log.Level, cfg.Log.Format)
	log.Info("starting upstream-hub", "version", version.Version, "port", cfg.Server.Port, "mode", cfg.Server.Mode)

	cipher, err := crypto.NewCipher(cfg.Security.AppSecret)
	if err != nil {
		log.Error("init cipher failed (set APP_SECRET)", "err", err)
		os.Exit(1)
	}

	// Auth：默认禁用（AUTH_ENABLED=false），所有 /api/* 免 token；
	// 显式开启时账号/密码必填，token secret 缺省回退到 AppSecret。
	var authSvc *auth.Service
	if cfg.Auth.Enabled {
		tokenSecret := cfg.Auth.TokenSecret
		if tokenSecret == "" {
			tokenSecret = cfg.Security.AppSecret
		}
		authSvc, err = auth.New(
			cfg.Auth.Username,
			cfg.Auth.Password,
			tokenSecret,
			time.Duration(cfg.Auth.SessionTTLHours)*time.Hour,
			time.Duration(cfg.Auth.MaxSessionTTLHours)*time.Hour,
		)
		if err != nil {
			log.Error("init auth failed (set ADMIN_USERNAME / ADMIN_PASSWORD or AUTH_ENABLED=false)", "err", err)
			os.Exit(1)
		}
		authSvc.SetLogger(log)
		log.Info("auth enabled", "username", cfg.Auth.Username)
	} else {
		log.Warn("auth disabled — all /api/* endpoints are open; set AUTH_ENABLED=true for production exposure")
	}

	db, err := storage.Open(cfg.Database.ToStorageConfig())
	if err != nil {
		log.Error("open database failed", "err", err)
		os.Exit(1)
	}
	if err := storage.AutoMigrate(db); err != nil {
		log.Error("auto migrate failed", "err", err)
		os.Exit(1)
	}

	channels := storage.NewChannels(db)
	authSessions := storage.NewAuthSessions(db)
	captchas := storage.NewCaptchas(db)
	notifies := storage.NewNotifications(db)
	rates := storage.NewRates(db)
	monLogs := storage.NewMonitorLogs(db)
	failures := storage.NewFailures(db)

	// 运行时配置（P3-14，docs/specs/runtime-settings.md）：env / config.yaml
	// 推导基线 + app_settings 单行表覆盖。无行时 eff 与基线逐字段一致，
	// 存量部署行为零变化。
	appSettings := storage.NewAppSettings(db)
	base := settings.FromConfig(cfg)
	settingsSvc := settings.NewService(appSettings, base)
	if err := settingsSvc.Init(); err != nil {
		log.Error("load runtime settings failed", "err", err)
		os.Exit(1)
	}
	eff := settingsSvc.Current()

	channelSvc := channel.NewService(channels, authSessions, captchas, monLogs, cipher)
	// env 静默时段配置非法仍 fail-fast（与热更新路径的 400 校验区分）。
	if _, err := notify.ParseQuietHours(cfg.Notifications.QuietHours); err != nil {
		log.Error("解析静默时段配置失败", "err", err)
		os.Exit(1)
	}
	dispatcher := notify.NewDispatcher(notifies, cipher, log, settings.PolicyOf(eff))
	tracker := monitor.NewFailureTracker(failures, eff.FailureThreshold, log)
	monitorSvc := monitor.NewService(channels, rates, monLogs, channelSvc, dispatcher, log,
		settings.MonitorForecastOf(eff), tracker, eff.BalanceRecovery)

	upd := updater.New(cfg.Updater.GithubRepo, cfg.Updater.GithubProxy, log)

	schCfg := settings.SchedulerConfigOf(eff)
	schCfg.Concurrency = cfg.Scheduler.Concurrency
	sch := scheduler.New(schCfg, monitorSvc, monLogs, rates, notifies, log)
	if err := sch.Start(); err != nil {
		log.Error("start scheduler failed", "err", err)
		os.Exit(1)
	}
	defer sch.Stop()

	// 热更新传播：PUT / DELETE /api/settings 成功后推送新值到各组件。
	// scheduler 的 Concurrency 不在热更新范围，永远取 env 值。
	settingsSvc.OnApply(func(v storage.AppSettings) {
		dispatcher.UpdatePolicy(settings.PolicyOf(v))
	})
	settingsSvc.OnApply(func(v storage.AppSettings) {
		monitorSvc.UpdateSettings(settings.MonitorForecastOf(v), v.BalanceRecovery)
	})
	settingsSvc.OnApply(func(v storage.AppSettings) {
		tracker.UpdateThreshold(v.FailureThreshold)
	})
	settingsSvc.OnApply(func(v storage.AppSettings) {
		scfg := settings.SchedulerConfigOf(v)
		scfg.Concurrency = cfg.Scheduler.Concurrency
		if err := sch.Reconfigure(scfg); err != nil {
			log.Warn("热更新调度配置失败（沿用旧计划，重启后生效）", "err", err)
		}
	})

	gin.SetMode(cfg.Server.Mode)
	router := gin.New()
	router.Use(gin.Recovery())
	if len(cfg.Server.TrustedProxies) > 0 {
		_ = router.SetTrustedProxies(cfg.Server.TrustedProxies)
	}

	// 仅在嵌入了真实前端产物时挂载静态 handler。
	// 本地 `go run` 跑出来的二进制 dist 是空占位，此时由 vite dev server 接管 :3010。
	var frontendFS fs.FS
	if web.HasFrontend() {
		frontendFS = web.DistFS()
		log.Info("frontend embedded, serving SPA on /")
	} else {
		log.Info("no embedded frontend, run vite dev server separately for UI")
	}

	api.Register(router, &api.Deps{
		DB:         db,
		Cipher:     cipher,
		Auth:       authSvc,
		Channels:   channels,
		Sessions:   authSessions,
		Captchas:   captchas,
		Notifies:   notifies,
		Rates:      rates,
		MonLogs:    monLogs,
		ChannelSvc: channelSvc,
		Monitor:    monitorSvc,
		Dispatcher: dispatcher,
		Updater:    upd,
		Log:        log,
		Frontend:   frontendFS,

		MetricsToken: cfg.Server.MetricsToken,
		Settings:     settingsSvc,
	})

	srv := &http.Server{
		Addr:              fmt.Sprintf(":%d", cfg.Server.Port),
		Handler:           router,
		ReadHeaderTimeout: 10 * time.Second,
	}

	go func() {
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Error("http server error", "err", err)
			os.Exit(1)
		}
	}()
	log.Info("http server listening", "addr", srv.Addr)

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	<-stop
	log.Info("shutdown signal received")

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		log.Error("http shutdown error", "err", err)
	}
	log.Info("bye")
}
