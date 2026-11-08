// Command server 是 llm-hub 的 HTTP 服务入口：装配 config/store/crypto/auth/
// middleware/models/discovery/channel/adapter/router/relay/metering/health/api，
// 注册 /v1/*、/api/*、/healthz、/metrics，端口来自 SERVER_PORT（默认 8080）。
package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"llm-hub/src/internal/api"
	"llm-hub/src/internal/config"
	"llm-hub/src/internal/store"
)

func main() {
	cfg := config.Load()

	dataDir := cfg.DataDir
	if dataDir == "" {
		dataDir = "./.llmhub-data" // 运行时 JSON 持久化目录（非源码）
	}
	st, err := store.New(dataDir)
	if err != nil {
		log.Fatalf("初始化存储失败: %v", err)
	}
	defer func() { _ = st.Save() }()

	srv, err := api.NewServer(cfg, st)
	if err != nil {
		log.Fatalf("装配服务失败: %v", err)
	}

	// 天衡：所有用户都是超级用户，没有任何管理员角色。
	// 启动期把历史数据里残留的 admin/user 角色一律归一为 superuser ——
	// 这与既有语义一致（无角色区分），不会造成越权：superuser 本来就是全部权限。
	if n := st.NormalizeRolesToSuperuser(); n > 0 {
		log.Printf("天衡角色归一：%d 个历史账号已统一为 superuser（无管理员角色）", n)
	}

	// 从 ADMIN_EMAIL / ADMIN_PASSWORD 引导账号（同样是普通超级用户，无特权）。
	if cfg.AdminEmail != "" && cfg.AdminPassword != "" {
		if _, ok := st.GetUserByEmail(cfg.AdminEmail); !ok {
			if _, _, err := srv.Auth.Register(cfg.AdminEmail, cfg.AdminPassword, true, cfg.AdminEmail); err != nil {
				log.Printf("引导超级用户账号失败（可忽略）: %v", err)
			} else {
				log.Printf("已引导超级用户账号: %s", cfg.AdminEmail)
			}
		}
	}

	addr := ":" + strconv.Itoa(cfg.ServerPort)
	httpServer := &http.Server{
		Addr:              addr,
		Handler:           srv.Handler(),
		ReadHeaderTimeout: 15 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	go func() {
		fmt.Printf("天衡 启动于 %s，全员超级用户（无管理员）· 无任何使用限制 · 真实模型池 data/models-real.json（460 条）\n", addr)
		if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("HTTP 服务异常退出: %v", err)
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit
	log.Println("正在关闭服务…")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := httpServer.Shutdown(ctx); err != nil {
		log.Printf("HTTP 关闭: %v", err)
	}
	srv.Stop()
	log.Println("已停止")
}
