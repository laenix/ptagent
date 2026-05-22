package toollogger

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/ptagent/ptagent/internal/models"
)

// Logger 文件-based 工具事件日志记录器
type Logger struct {
	dir  string
	mu   sync.Mutex
	date string // 当前日志文件日期
	f    *os.File
}

// New 创建工具事件日志记录器
func New(dataDir string) (*Logger, error) {
	dir := filepath.Join(dataDir, "tool_events")
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, fmt.Errorf("create tool events dir: %w", err)
	}

	logger := &Logger{dir: dir}
	if err := logger.rotate(); err != nil {
		return nil, err
	}
	return logger, nil
}

// Record 记录工具调用事件
func (l *Logger) Record(event *models.ToolEvent) error {
	l.mu.Lock()
	defer l.mu.Unlock()

	// 检查是否需要滚动日志文件（按日期）
	today := time.Now().Format("2006-01-02")
	if today != l.date {
		if err := l.rotate(); err != nil {
			return err
		}
	}

	data, err := json.Marshal(event)
	if err != nil {
		return fmt.Errorf("marshal event: %w", err)
	}

	_, err = l.f.Write(append(data, '\n'))
	if err != nil {
		return fmt.Errorf("write event: %w", err)
	}
	return nil
}

// rotate 滚动日志文件
func (l *Logger) rotate() error {
	if l.f != nil {
		l.f.Close()
	}

	date := time.Now().Format("2006-01-02")
	filename := filepath.Join(l.dir, date+".jsonl")
	f, err := os.OpenFile(filename, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		return fmt.Errorf("open log file: %w", err)
	}

	l.date = date
	l.f = f
	return nil
}

// Close 关闭日志文件
func (l *Logger) Close() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.f != nil {
		return l.f.Close()
	}
	return nil
}

// ReadByProject 读取指定项目的工具事件
func (l *Logger) ReadByProject(projectID string, limit int) ([]models.ToolEvent, error) {
	// 读取今天的日志文件
	return l.readFile(l.todayFile(), projectID, limit)
}

func (l *Logger) todayFile() string {
	return filepath.Join(l.dir, time.Now().Format("2006-01-02")+".jsonl")
}

func (l *Logger) readFile(filename string, projectID string, limit int) ([]models.ToolEvent, error) {
	f, err := os.Open(filename)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	defer f.Close()

	var events []models.ToolEvent
	scanner := bufio.NewScanner(f)
	for scanner.Scan() && len(events) < limit {
		var event models.ToolEvent
		if err := json.Unmarshal(scanner.Bytes(), &event); err != nil {
			continue // 跳过无效行
		}
		if projectID != "" && event.ProjectID != projectID {
			continue
		}
		events = append(events, event)
	}

	// 逆序返回（最新的在前）
	for i, j := 0, len(events)-1; i < j; i, j = i+1, j-1 {
		events[i], events[j] = events[j], events[i]
	}

	return events, nil
}

// ReadRecent 读取最近的所有工具事件（用于调试）
func (l *Logger) ReadRecent(limit int) ([]models.ToolEvent, error) {
	filename := l.todayFile()
	if _, err := os.Stat(filename); os.IsNotExist(err) {
		return nil, nil
	}
	return l.readFile(filename, "", limit)
}
