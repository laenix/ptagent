package main

import (
	"context"
	"flag"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/ptagent/ptagent/internal/config"
	"github.com/ptagent/ptagent/internal/dispatcher"
	"github.com/ptagent/ptagent/internal/server/api"
	"github.com/ptagent/ptagent/internal/store/sqlite"
)

func main() {
	addr := flag.String("addr", ":8000", "server listen address")
	dbPath := flag.String("db", "./data/ptagent.db", "SQLite database path")
	configPath := flag.String("config", "./configs/dispatch.yaml", "dispatcher config file path")
	webDir := flag.String("web", "./web/dist", "frontend static files directory")
	flag.Parse()

	// 确保数据目录存在
	if err := os.MkdirAll("./data", 0o755); err != nil {
		log.Fatalf("create data dir: %v", err)
	}

	// 初始化存储
	store, err := sqlite.New(*dbPath)
	if err != nil {
		log.Fatalf("init store: %v", err)
	}

	// 启动超时清理 goroutine
	go func() {
		ticker := time.NewTicker(5 * time.Second)
		defer ticker.Stop()
		for range ticker.C {
			ctx := context.Background()
			settings, err := store.GetSettings(ctx)
			if err != nil {
				continue
			}
			_ = store.CleanupExpiredClaims(ctx, settings.IntentTimeout, settings.ReasonTimeout)
		}
	}()

	// 设置 Gin
	r := gin.Default()

	// CORS middleware
	r.Use(func(c *gin.Context) {
		c.Header("Access-Control-Allow-Origin", "*")
		c.Header("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		c.Header("Access-Control-Allow-Headers", "Content-Type, Authorization")
		if c.Request.Method == "OPTIONS" {
			c.AbortWithStatus(http.StatusNoContent)
			return
		}
		c.Next()
	})

	// 注册路由
	handler := api.NewHandler(store)

	// 创建 Dispatcher Manager
	dispManager := dispatcher.NewManager()
	handler.SetDispatcherManager(dispManager)

	handler.RegisterRoutes(r)

	// Health check
	r.GET("/health", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
	})

	// 前端静态文件服务（SPA fallback）
	webAbs, _ := filepath.Abs(*webDir)
	if info, err := os.Stat(webAbs); err == nil && info.IsDir() {
		r.Static("/assets", filepath.Join(webAbs, "assets"))
		r.StaticFile("/favicon.ico", filepath.Join(webAbs, "favicon.ico"))
		// SPA fallback: 未匹配的路由返回 index.html
		r.NoRoute(func(c *gin.Context) {
			c.File(filepath.Join(webAbs, "index.html"))
		})
		log.Printf("Serving frontend from %s", webAbs)
	} else {
		log.Printf("Warning: web dir %s not found, frontend disabled", webAbs)
		r.NoRoute(func(c *gin.Context) {
			c.JSON(http.StatusNotFound, gin.H{"error": "not found"})
		})
	}

	// 启动 HTTP Server
	srv := &http.Server{
		Addr:    *addr,
		Handler: r,
	}

	go func() {
		log.Printf("PTAgent Server listening on %s", *addr)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("listen: %v", err)
		}
	}()

	// 等待 Server 就绪
	waitForServer("http://127.0.0.1" + *addr + "/health")

	// 加载 Dispatcher 配置并启动
	cfg, err := config.LoadDispatchConfig(*configPath)
	if err != nil {
		log.Fatalf("load dispatch config: %v", err)
	}
	// 强制 dispatcher 连接本进程的 server
	cfg.Server = "http://127.0.0.1" + *addr

	d, err := dispatcher.New(cfg)
	if err != nil {
		log.Fatalf("init dispatcher: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// 注册到 manager
	dispManager.Register("disp_001", "default", cfg, d, cancel)

	go func() {
		log.Println("PTAgent Dispatcher starting...")
		if err := d.Run(ctx); err != nil {
			log.Printf("dispatcher run: %v", err)
		}
	}()

	// 优雅关闭
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit
	log.Println("Shutting down...")

	cancel() // 停止 dispatcher

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer shutdownCancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Fatalf("server shutdown: %v", err)
	}
	log.Println("PTAgent exited")
}

func waitForServer(url string) {
	for i := 0; i < 50; i++ {
		resp, err := http.Get(url)
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	log.Println("Warning: server health check timed out, starting dispatcher anyway")
}
