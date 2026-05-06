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
	"github.com/ptagent/ptagent/internal/server/api"
	"github.com/ptagent/ptagent/internal/store/sqlite"
)

func main() {
	addr := flag.String("addr", ":8000", "server listen address")
	dbPath := flag.String("db", "./data/ptagent.db", "SQLite database path")
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
	handler.RegisterRoutes(r)

	// Health check
	r.GET("/health", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
	})

	// Frontend static files (SPA fallback)
	webAbs, _ := filepath.Abs(*webDir)
	if info, err := os.Stat(webAbs); err == nil && info.IsDir() {
		r.Static("/assets", filepath.Join(webAbs, "assets"))
		r.StaticFile("/favicon.ico", filepath.Join(webAbs, "favicon.ico"))
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

	// 优雅关闭
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

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit
	log.Println("Shutting down server...")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		log.Fatalf("server shutdown: %v", err)
	}
	log.Println("Server exited")
}
