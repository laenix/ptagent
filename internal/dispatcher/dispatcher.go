package dispatcher

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"math/rand/v2"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/ptagent/ptagent/internal/config"
	"github.com/ptagent/ptagent/internal/container"
	"github.com/ptagent/ptagent/internal/models"
	"github.com/ptagent/ptagent/internal/tools"
	"github.com/ptagent/ptagent/internal/worker"
	"github.com/ptagent/ptagent/internal/worker/driver"
)

// reasonCheckpoint reason 任务的图状态快照，用于判断图是否有变化
type reasonCheckpoint struct {
	factCount       int
	hintCount       int
	openIntentCount int
}

// runningTask 正在执行的任务信息，用于取消非活跃项目的任务
type runningTask struct {
	projectID string
	taskType  worker.TaskType
	worker    string
	cancel    context.CancelFunc
}

// Dispatcher 核心调度器
type Dispatcher struct {
	cfg          *config.DispatchConfig
	client       *http.Client
	drivers      map[string]worker.Driver
	prompts      *PromptManager
	containerMgr *container.Manager // 容器管理器（可选，nil 时本地执行）

	// 运行状态
	mu                   sync.Mutex
	dispatchCond         *sync.Cond               // 任务完成时唤醒调度线程
	runningTasks         int
	projectWorkers       map[string]int              // projectID -> running count
	workerRunning        map[string]int              // worker name -> running count
	admitted             map[string]bool             // admitted project IDs
	bootstrapping        map[string]bool             // projects currently in bootstrap
	bootstrapped         map[string]bool             // projects that have successfully bootstrapped
	reasoning            map[string]bool             // projects currently in reason
	exploringIntents     map[string]bool             // intent IDs currently being explored
	projectExploring    map[string]bool             // project IDs currently in explore (for direction lock)
	reasonCheckpoints    map[string]reasonCheckpoint // projectID -> last reason graph snapshot
	runningTasks_        map[string]*runningTask     // taskKey -> running task (for cancellation)
	workerUnhealthyUntil map[string]time.Time        // worker name -> unhealthy until
	workerRejectedUntil  map[rejectionKey]time.Time  // (project,task,worker) -> rejected until
}

// rejectionKey worker 拒绝的键
type rejectionKey struct {
	projectID string
	taskType  string
	worker    string
}

// New 创建 Dispatcher
func New(cfg *config.DispatchConfig) (*Dispatcher, error) {
	d := &Dispatcher{
		cfg:                  cfg,
		client:               &http.Client{Timeout: 30 * time.Second},
		drivers:              make(map[string]worker.Driver),
		projectWorkers:       make(map[string]int),
		workerRunning:        make(map[string]int),
		admitted:             make(map[string]bool),
		bootstrapping:        make(map[string]bool),
		bootstrapped:         make(map[string]bool),
		reasoning:            make(map[string]bool),
		exploringIntents:     make(map[string]bool),
		projectExploring:     make(map[string]bool),
		reasonCheckpoints:    make(map[string]reasonCheckpoint),
		runningTasks_:        make(map[string]*runningTask),
		workerUnhealthyUntil: make(map[string]time.Time),
		workerRejectedUntil:  make(map[rejectionKey]time.Time),
	}
	d.dispatchCond = sync.NewCond(&d.mu)

	// 初始化容器管理器（如果启用）
	if cfg.Container.Enabled {
		cm, err := container.New(&cfg.Container)
		if err != nil {
			log.Printf("[dispatcher] container manager init failed (running in local mode): %v", err)
		} else {
			d.containerMgr = cm
			log.Printf("[dispatcher] container mode enabled, image=%s", cfg.Container.Image)
		}
	}

	// 初始化 prompt manager
	d.prompts = NewPromptManager(cfg.Runtime.PromptGroup)

	// 初始化 Worker drivers
	for _, w := range cfg.Workers {
		var drv worker.Driver
		switch w.Type {
		case "openai":
			drv = driver.NewOpenAIDriver(w.Name, w.Env, &cfg.Proxy)
		case "mock":
			drv = driver.NewMockDriver(w.Name, w.Env)
		default:
			return nil, fmt.Errorf("unsupported driver type: %s", w.Type)
		}
		d.drivers[w.Name] = drv
	}

	return d, nil
}

// Run 启动调度主循环
func (d *Dispatcher) Run(ctx context.Context) error {
	log.Printf("[dispatcher] started, interval=%ds, max_workers=%d, max_projects=%d",
		d.cfg.Runtime.Interval, d.cfg.Runtime.MaxWorkers, d.cfg.Runtime.MaxRunningProjects)

	ticker := time.NewTicker(time.Duration(d.cfg.Runtime.Interval) * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			log.Println("[dispatcher] shutting down")
			return nil
		case <-ticker.C:
			d.schedulingRound(ctx)
		}
	}
}

// schedulingRound 一次调度轮
func (d *Dispatcher) schedulingRound(ctx context.Context) {
	projects, err := d.listProjects(ctx)
	if err != nil {
		log.Printf("[dispatcher] list projects error: %v", err)
		return
	}

	// 0. 清理不再 active 的 admitted 项目，并取消对应的运行任务
	activeIDs := make(map[string]bool)
	statusByID := make(map[string]models.ProjectStatus)
	for _, p := range projects {
		statusByID[p.ID] = p.Status
		if p.Status == models.ProjectStatusActive {
			activeIDs[p.ID] = true
		}
	}
	for id := range d.admitted {
		if !activeIDs[id] {
			delete(d.admitted, id)
			log.Printf("[dispatcher] removed non-active project %s from admitted set", id)
		}
	}
	d.cancelInactiveTasks(statusByID)

	// 计算项目配额：每个项目应该获得的 worker 上限
	// 公平调度：maxWorkers / maxRunningProjects，向上取整
	maxProjects := d.cfg.Runtime.MaxRunningProjects
	if maxProjects <= 0 {
		maxProjects = 1
	}
	quota := (d.cfg.Runtime.MaxWorkers + maxProjects - 1) / maxProjects
	if quota < 1 {
		quota = 1
	}

	dispatched := false

	// 1. 遍历已 admitted 的 active 项目（公平调度）
	for _, p := range projects {
		if p.Status != models.ProjectStatusActive {
			continue
		}

		if !d.admitted[p.ID] {
			continue
		}

		if !d.canDispatch() {
			log.Printf("[dispatcher] cannot dispatch, runningTasks=%d maxWorkers=%d", d.runningTasks, d.cfg.Runtime.MaxWorkers)
			break
		}

		// 检查项目是否已达配额
		d.mu.Lock()
		projectCount := d.projectWorkers[p.ID]
		d.mu.Unlock()
		if projectCount >= quota {
			log.Printf("[dispatcher] project %s已达配额(%d>=%d), 跳过", p.ID, projectCount, quota)
			continue
		}

		log.Printf("[dispatcher] 准备为项目 %s 调度, projectCount=%d quota=%d", p.ID, projectCount, quota)
		d.dispatchForProject(ctx, &p, quota)
		dispatched = true
	}

	// 2. admit 新项目
	if d.admittedCount() < d.cfg.Runtime.MaxRunningProjects {
		for _, p := range projects {
			if p.Status != models.ProjectStatusActive {
				continue
			}
			if d.admitted[p.ID] {
				continue
			}
			d.admitted[p.ID] = true
			log.Printf("[dispatcher] admitted project %s (%s)", p.ID, p.Title)
			break
		}
	}

	// 3. 如果没有派发任务，等待一段时间让任务完成
	if !dispatched {
		d.mu.Lock()
		if d.runningTasks < d.cfg.Runtime.MaxWorkers {
			waitInterval := time.Duration(d.cfg.Runtime.Interval) * time.Second
			log.Printf("[dispatcher] no tasks dispatched, sleeping %v before next round", waitInterval)
			d.mu.Unlock()
			time.Sleep(waitInterval)
		} else {
			d.mu.Unlock()
		}
	}
}

// dispatchForProject 为单个项目调度任务（可能派发多个 explore）
// quota: 项目配额，超过则不再派发新任务
func (d *Dispatcher) dispatchForProject(ctx context.Context, p *models.ProjectSummary, quota int) {
	log.Printf("[dispatcher] dispatchForProject called: project=%s factCount=%d intentCount=%d", p.ID, p.FactCount, p.IntentCount)
	d.mu.Lock()
	projectCount := d.projectWorkers[p.ID]
	d.mu.Unlock()

	// 使用配额和 MaxProjectWorkers 中的较小值作为限制
	maxAllowed := quota
	if d.cfg.Runtime.MaxProjectWorkers < maxAllowed {
		maxAllowed = d.cfg.Runtime.MaxProjectWorkers
	}

	if projectCount >= maxAllowed {
		log.Printf("[dispatcher] dispatchForProject: project %s at quota limit (%d >= %d), skipping", p.ID, projectCount, maxAllowed)
		return
	}

	// 判断项目状态决定任务类型
	log.Printf("[dispatcher] dispatchForProject: checking project %s, isInitialState=%v, UnclaimedIntentCount=%d, Reason=%v",
		p.ID, d.isInitialState(p), p.UnclaimedIntentCount, p.Reason)
	if d.isInitialState(p) {
		// Bootstrap - 只派遣一次
		d.mu.Lock()
		already := d.bootstrapping[p.ID]
		if !already {
			d.bootstrapping[p.ID] = true
		}
		d.mu.Unlock()
		if !already {
			d.dispatchTask(ctx, p.ID, worker.TaskBootstrap)
		}
	} else if p.UnclaimedIntentCount > 0 {
		// Explore - 检查项目是否已有其他方向的任务在运行
		d.mu.Lock()
		isReasoning := d.reasoning[p.ID]
		d.mu.Unlock()
		if isReasoning {
			return // 项目已在 reason 中，等待
		}

		// 循环派发直到填满配额
		for {
			if !d.canDispatch() {
				return
			}
			d.mu.Lock()
			pc := d.projectWorkers[p.ID]
			d.mu.Unlock()
			if pc >= maxAllowed {
				return
			}
			d.dispatchTask(ctx, p.ID, worker.TaskExplore)
			// 每次派发后重新检查是否还有未认领 intent
			break
		}
	} else if p.Reason == nil {
		// Reason - 检查项目是否已有 Explore 在运行
		d.mu.Lock()
		hasExploring := d.projectExploring[p.ID]
		d.mu.Unlock()
		if hasExploring {
			return // 项目已在 explore 中，等待
		}

		// Reason - 只有图有实质变化才触发
		trigger := d.reasonTrigger(p)
		if trigger == "" {
			return
		}
		d.mu.Lock()
		alreadyReasoning := d.reasoning[p.ID]
		if !alreadyReasoning {
			d.reasoning[p.ID] = true
		}
		d.mu.Unlock()
		if !alreadyReasoning {
			d.dispatchTask(ctx, p.ID, worker.TaskReason, trigger)
		}
	}
}

func (d *Dispatcher) isInitialState(p *models.ProjectSummary) bool {
	// 已成功 bootstrap 的项目不再是初始态
	d.mu.Lock()
	_, alreadyBootstrapped := d.bootstrapped[p.ID]
	d.mu.Unlock()
	if alreadyBootstrapped {
		return false
	}
	// 初始态：只有 origin 和 goal 两个 fact，且没有非 bootstrap 的 intent
	// IntentCount 包含 bootstrap intent，所以允许最多 1 个 intent（bootstrap intent 本身）
	return p.FactCount <= 2 && p.IntentCount <= 1
}

// reasonTrigger 检查图是否有实质变化，返回触发原因；无变化返回空字符串
func (d *Dispatcher) reasonTrigger(p *models.ProjectSummary) string {
	d.mu.Lock()
	cp, exists := d.reasonCheckpoints[p.ID]
	d.mu.Unlock()

	openIntentCount := p.WorkingIntentCount + p.UnclaimedIntentCount
	if !exists {
		// 初始化检查点（有 open intent 时）
		if openIntentCount > 0 {
			d.mu.Lock()
			d.reasonCheckpoints[p.ID] = reasonCheckpoint{
				factCount:       p.FactCount,
				hintCount:       p.HintCount,
				openIntentCount: openIntentCount,
			}
			d.mu.Unlock()
		}
		return "initial"
	}

	var changes []string
	if p.FactCount > cp.factCount {
		changes = append(changes, fmt.Sprintf("facts:%d->%d", cp.factCount, p.FactCount))
	}
	if p.HintCount > cp.hintCount {
		changes = append(changes, fmt.Sprintf("hints:%d->%d", cp.hintCount, p.HintCount))
	}
	if cp.openIntentCount > 0 && openIntentCount == 0 {
		changes = append(changes, fmt.Sprintf("open_intents:%d->0", cp.openIntentCount))
	}
	if len(changes) == 0 {
		return ""
	}
	return strings.Join(changes, ",")
}

// cancelInactiveTasks 取消不再 active 项目的运行中任务
func (d *Dispatcher) cancelInactiveTasks(statusByID map[string]models.ProjectStatus) {
	d.mu.Lock()
	defer d.mu.Unlock()
	for key, rt := range d.runningTasks_ {
		status, exists := statusByID[rt.projectID]
		if !exists {
			status = "deleted"
		}
		if status != models.ProjectStatusActive {
			log.Printf("[dispatcher] cancelling task for inactive project project=%s task=%s worker=%s status=%s",
				rt.projectID, rt.taskType, rt.worker, status)
			rt.cancel()
			delete(d.runningTasks_, key)
		}
	}
}

// dispatchTask 派发任务
func (d *Dispatcher) dispatchTask(ctx context.Context, projectID string, taskType worker.TaskType, triggerArgs ...string) {
	// 选择 worker
	w := d.selectWorker(taskType)
	if w == nil {
		log.Printf("[dispatcher] dispatchTask: no worker available for task type %s", taskType)
		return
	}

	drv := d.drivers[w.Name]

	// 健康检查
	hcCtx, cancel := context.WithTimeout(ctx, time.Duration(d.cfg.Runtime.HealthcheckTimeout)*time.Second)
	defer cancel()
	if err := drv.Healthcheck(hcCtx); err != nil {
		log.Printf("[dispatcher] worker %s healthcheck failed: %v", w.Name, err)
		d.markWorkerUnhealthy(w.Name)
		d.recordEvent(projectID, string(taskType), "", w.Name, "healthcheck_failed", "", "", err.Error(), 0)
		return
	}

	// 获取项目详情构造 prompt
	detail, err := d.getProjectDetail(ctx, projectID)
	if err != nil {
		log.Printf("[dispatcher] get project detail error: %v", err)
		return
	}

	// 获取 YAML 导出用于 reason/explore
	var exportYAML string
	if taskType == worker.TaskReason || taskType == worker.TaskExplore {
		exportYAML, err = d.exportProjectYAML(ctx, projectID)
		if err != nil {
			log.Printf("[dispatcher] export YAML error: %v", err)
			// fallback 到 prompt manager 内置的 graph 构建
		}
	}

	prompt, err := d.prompts.RenderWithExport(taskType, detail, exportYAML)
	if err != nil {
		log.Printf("[dispatcher] render prompt error: %v", err)
		return
	}

	// explore 时通过 Server API 认领 intent
	var intentID string
	if taskType == worker.TaskExplore && detail != nil {
		d.mu.Lock()
		for _, intent := range detail.Intents {
			if intent.IsOpen() && !intent.IsClaimed() && !d.exploringIntents[intent.ID] {
				intentID = intent.ID
				break
			}
		}
		d.mu.Unlock()
		if intentID == "" {
			log.Printf("[dispatcher] no available intent for explore in project %s, skipping", projectID)
			return
		}
		// 通过 Server API 认领（原子操作）
		claimStatus := d.heartbeatIntent(ctx, projectID, intentID, w.Name)
		if claimStatus != http.StatusOK {
			log.Printf("[dispatcher] intent claim failed project=%s intent=%s status=%d", projectID, intentID, claimStatus)
			return
		}
		d.mu.Lock()
		d.exploringIntents[intentID] = true
		d.projectExploring[projectID] = true
		d.mu.Unlock()
		// 替换 prompt 中的占位符
		for _, intent := range detail.Intents {
			if intent.ID == intentID {
				prompt = strings.ReplaceAll(prompt, "{intent_id}", intent.ID)
				prompt = strings.ReplaceAll(prompt, "{intent_description}", intent.Description)
				break
			}
		}
	}

	// reason 时通过 Server API 认领
	if taskType == worker.TaskReason {
		trigger := "dispatch"
		if len(triggerArgs) > 0 && triggerArgs[0] != "" {
			trigger = triggerArgs[0]
		}
		claimStatus := d.claimReason(ctx, projectID, w.Name, trigger)
		if claimStatus != http.StatusOK {
			log.Printf("[dispatcher] reason claim failed project=%s status=%d", projectID, claimStatus)
			d.mu.Lock()
			delete(d.reasoning, projectID)
			d.mu.Unlock()
			return
		}
	}

	// 增加计数，注册可取消的任务
	taskKey := fmt.Sprintf("%s:%s:%s", projectID, taskType, intentID)
	taskCtx, taskCancel := context.WithCancel(ctx)
	d.mu.Lock()
	d.runningTasks++
	d.projectWorkers[projectID]++
	d.workerRunning[w.Name]++
	d.runningTasks_[taskKey] = &runningTask{
		projectID: projectID,
		taskType:  taskType,
		worker:    w.Name,
		cancel:    taskCancel,
	}
	d.mu.Unlock()

	wName := w.Name // capture for goroutine

	// 异步执行
	go func() {
		defer taskCancel()
		defer func() {
			d.mu.Lock()
			d.runningTasks--
			d.projectWorkers[projectID]--
			d.workerRunning[wName]--
			delete(d.runningTasks_, taskKey)
			// 唤醒等待中的调度线程
			d.dispatchCond.Signal()
			d.mu.Unlock()
		}()

		task := &worker.Task{
			Type:      taskType,
			ProjectID: projectID,
			IntentID:  intentID,
			Prompt:    prompt,
			Env:       w.Env,
			Timeout:   d.getTimeout(taskType),
		}

		// 启动心跳
		heartbeatInterval := time.Duration(d.cfg.Runtime.Interval) * time.Second
		var lease *HeartbeatLease
		if taskType == worker.TaskReason {
			lease = NewReasonHeartbeat(d.client, d.cfg.Server, projectID, wName, heartbeatInterval)
		} else if intentID != "" {
			lease = NewIntentHeartbeat(d.client, d.cfg.Server, projectID, intentID, wName, heartbeatInterval)
		}
		if lease != nil {
			lease.SetOnFail(taskCancel) // 心跳失败时立即取消任务上下文
			lease.Start()
			defer lease.Stop()
		}

		// 清理函数
		defer func() {
			if task.IntentID != "" {
				d.mu.Lock()
				delete(d.exploringIntents, task.IntentID)
				delete(d.projectExploring, projectID)
				d.mu.Unlock()
			}
			if taskType == worker.TaskReason {
				d.mu.Lock()
				delete(d.reasoning, projectID)
				d.mu.Unlock()
				// 释放 reason lease（使用独立 ctx，避免主 ctx 已取消时无法释放）
				releaseCtx, releaseCancel := context.WithTimeout(context.Background(), 5*time.Second)
				d.releaseReason(releaseCtx, projectID, wName)
				releaseCancel()
			}
		}()

		log.Printf("[dispatcher] dispatching %s for project %s via worker %s", taskType, projectID, wName)
		d.recordEvent(projectID, string(taskType), intentID, wName, "dispatched", "", "", "", 0)
		taskStarted := time.Now()

		// 为 explore 任务设置容器内工具执行器
		if d.containerMgr != nil && taskType == worker.TaskExplore {
			info, err := d.containerMgr.EnsureRunning(taskCtx, projectID)
			if err != nil {
				log.Printf("[dispatcher] container start failed for project %s: %v", projectID, err)
				d.recordEvent(projectID, string(taskType), intentID, wName, "failed", "", "", "container start: "+err.Error(), 0)
				d.releaseOnFailure(taskCtx, projectID, task, wName)
				return
			}
			if oaiDrv, ok := drv.(*driver.OpenAIDriver); ok {
				containerInfo := info
				oaiDrv.SetContainerExecutor(func(ctx context.Context, name string, args map[string]interface{}) *tools.ToolResult {
					return d.containerMgr.ExecuteTool(ctx, containerInfo, name, args)
				})
				defer oaiDrv.SetContainerExecutor(nil)
			}
		}

		execCtx, execCancel := context.WithTimeout(taskCtx, time.Duration(task.Timeout)*time.Second)
		defer execCancel()

		result, err := drv.Execute(execCtx, task)
		durationMs := time.Since(taskStarted).Milliseconds()

		// 检查心跳是否丢失
		if lease != nil && lease.Failed() != nil {
			log.Printf("[dispatcher] heartbeat lost during %s project=%s worker=%s", taskType, projectID, wName)
			d.recordEvent(projectID, string(taskType), intentID, wName, "failed", task.Prompt, "", "heartbeat lost", durationMs)
			d.releaseOnFailure(ctx, projectID, task, wName)
			return
		}

		if err != nil {
			log.Printf("[dispatcher] %s execution error: %v", taskType, err)
			d.recordEvent(projectID, string(taskType), intentID, wName, "failed", task.Prompt, "", err.Error(), durationMs)
			// 尝试 conclude 双阶段收尾
			if taskType != worker.TaskReason && drv.SupportsConclude() {
				d.tryConclude(ctx, drv, task, projectID, wName, detail)
			} else {
				d.releaseOnFailure(ctx, projectID, task, wName)
			}
			return
		}

		if result == nil || !result.Accepted {
			log.Printf("[dispatcher] %s rejected or nil result for project %s", taskType, projectID)
			d.recordEvent(projectID, string(taskType), intentID, wName, "rejected", task.Prompt, "", "", durationMs)
			d.markWorkerRejected(projectID, string(taskType), wName)
			d.releaseOnFailure(ctx, projectID, task, wName)
			return
		}

		// 记录成功事件
		outputBytes, _ := json.Marshal(result.Data)
		d.recordEvent(projectID, string(taskType), intentID, wName, "succeed", task.Prompt, string(outputBytes), "", durationMs)

		// 写回结果，reason 成功时删除 checkpoint（下一轮会重新初始化正确的快照）
		if taskType == worker.TaskReason {
			d.mu.Lock()
			delete(d.reasonCheckpoints, projectID)
			d.mu.Unlock()
		}
		d.writeBack(ctx, projectID, task, result, wName)

		// Bootstrap 成功时清理标记并标记为已 bootstrap
		if taskType == worker.TaskBootstrap {
			d.mu.Lock()
			delete(d.bootstrapping, projectID)
			d.bootstrapped[projectID] = true
			d.mu.Unlock()
		}
	}()
}

// releaseOnFailure 任务失败时释放认领
func (d *Dispatcher) releaseOnFailure(ctx context.Context, projectID string, task *worker.Task, workerName string) {
	if task.IntentID != "" {
		d.releaseIntent(ctx, projectID, task.IntentID, workerName)
	}
	// Bootstrap 失败时清理标记，允许重试
	if task.Type == worker.TaskBootstrap {
		d.mu.Lock()
		delete(d.bootstrapping, projectID)
		d.mu.Unlock()
	}
}

// recordEvent 通过 Server API 记录任务事件
func (d *Dispatcher) recordEvent(projectID, taskType, intentID, workerName, phase, prompt, output, errMsg string, durationMs int64) {
	event := map[string]interface{}{
		"task_type":   taskType,
		"intent_id":   intentID,
		"worker":      workerName,
		"phase":       phase,
		"prompt":      prompt,
		"output":      output,
		"error":       errMsg,
		"duration_ms": durationMs,
	}
	bodyBytes, _ := json.Marshal(event)
	url := fmt.Sprintf("%s/api/projects/%s/events", d.cfg.Server, projectID)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, "POST", url, strings.NewReader(string(bodyBytes)))
	req.Header.Set("Content-Type", "application/json")
	resp, err := d.client.Do(req)
	if err != nil {
		log.Printf("[dispatcher] record event error: %v", err)
		return
	}
	resp.Body.Close()
}

// tryConclude 双阶段收尾
func (d *Dispatcher) tryConclude(ctx context.Context, drv worker.Driver, task *worker.Task, projectID, workerName string, detail *models.ProjectDetailResponse) {
	concludePrompt, err := d.prompts.RenderConclude(task.Type, detail)
	if err != nil {
		log.Printf("[dispatcher] render conclude prompt error: %v", err)
		d.releaseOnFailure(ctx, projectID, task, workerName)
		return
	}
	task.Prompt = concludePrompt

	timeout := d.cfg.Tasks.Explore.ConcludeTimeout
	if task.Type == worker.TaskBootstrap {
		timeout = d.cfg.Tasks.Bootstrap.ConcludeTimeout
	}

	concludeCtx, cancel := context.WithTimeout(ctx, time.Duration(timeout)*time.Second)
	defer cancel()

	log.Printf("[dispatcher] starting conclude fallback %s project=%s worker=%s", task.Type, projectID, workerName)
	d.recordEvent(projectID, string(task.Type), task.IntentID, workerName, "concluded", concludePrompt, "", "", 0)

	result, err := drv.Conclude(concludeCtx, task, "")
	if err != nil {
		log.Printf("[dispatcher] conclude failed: %v", err)
		d.recordEvent(projectID, string(task.Type), task.IntentID, workerName, "failed", concludePrompt, "", "conclude: "+err.Error(), 0)
		d.releaseOnFailure(ctx, projectID, task, workerName)
		return
	}

	if result != nil && result.Accepted {
		d.writeBack(ctx, projectID, task, result, workerName)
	} else {
		d.releaseOnFailure(ctx, projectID, task, workerName)
	}
}

// writeBack 将结果写回 Server
func (d *Dispatcher) writeBack(ctx context.Context, projectID string, task *worker.Task, result *worker.TaskResult, workerName string) {
	taskType := task.Type
	dataBytes, _ := json.Marshal(result.Data)
	var dataMap map[string]interface{}
	json.Unmarshal(dataBytes, &dataMap)

	switch taskType {
	case worker.TaskBootstrap:
		// bootstrap 可能返回 intents 数组（规划模式）或 fact + complete（直接解决）
		if intents, ok := dataMap["intents"]; ok {
			if arr, ok := intents.([]interface{}); ok {
				for _, item := range arr {
					d.createIntent(ctx, projectID, item, workerName)
				}
			}
		} else if desc, ok := d.extractDescription(dataMap, "fact"); ok {
			log.Printf("[dispatcher] bootstrap produced fact for project %s: %s", projectID, truncate(desc, 80))
		}
		// bootstrap 完成时先 conclude 自身 intent，再 complete 项目
		if task.IntentID != "" {
			if desc, ok := d.extractDescription(dataMap, "fact"); ok {
				d.concludeIntent(ctx, projectID, task.IntentID, desc, workerName)
			} else {
				d.concludeIntent(ctx, projectID, task.IntentID, "bootstrap completed", workerName)
			}
		}
		if _, ok := dataMap["complete"]; ok {
			log.Printf("[dispatcher] bootstrap completed project %s", projectID)
			d.completeProject(ctx, projectID, dataMap, workerName)
		}

	case worker.TaskReason:
		if _, ok := dataMap["complete"]; ok {
			// 项目完成
			d.completeProject(ctx, projectID, dataMap, workerName)
		} else if intentsData, ok := dataMap["intents"]; ok {
			// intents 数组形式（优先）
			if arr, ok := intentsData.([]interface{}); ok {
				created := 0
				for _, item := range arr {
					if d.createIntent(ctx, projectID, item, workerName) {
						created++
					}
				}
				log.Printf("[dispatcher] reason created %d/%d intents for project %s", created, len(arr), projectID)
			}
		} else if intentData, ok := dataMap["intent"]; ok {
			// 单个 intent 形式（向后兼容）
			d.createIntent(ctx, projectID, intentData, workerName)
		}

	case worker.TaskExplore:
		if desc, ok := d.extractDescription(dataMap, ""); ok {
			log.Printf("[dispatcher] explore concluded for project %s: %s", projectID, truncate(desc, 80))
			if task.IntentID != "" {
				d.concludeIntent(ctx, projectID, task.IntentID, desc, workerName)
			}
		}
	}
}

func (d *Dispatcher) extractDescription(data map[string]interface{}, key string) (string, bool) {
	if key != "" {
		sub, ok := data[key].(map[string]interface{})
		if !ok {
			return "", false
		}
		desc, ok := sub["description"].(string)
		return desc, ok
	}
	desc, ok := data["description"].(string)
	return desc, ok
}

func (d *Dispatcher) completeProject(ctx context.Context, projectID string, data map[string]interface{}, workerName string) {
	completeData, _ := data["complete"].(map[string]interface{})
	if completeData == nil {
		return
	}

	body := map[string]interface{}{
		"from":        completeData["from"],
		"description": completeData["description"],
		"worker":      workerName,
	}
	bodyBytes, _ := json.Marshal(body)

	url := fmt.Sprintf("%s/api/projects/%s/complete", d.cfg.Server, projectID)
	req, _ := http.NewRequestWithContext(ctx, "POST", url, strings.NewReader(string(bodyBytes)))
	req.Header.Set("Content-Type", "application/json")

	resp, err := d.client.Do(req)
	if err != nil {
		log.Printf("[dispatcher] complete project error: %v", err)
		return
	}
	defer resp.Body.Close()
	log.Printf("[dispatcher] project %s completed (status %d)", projectID, resp.StatusCode)
}

func (d *Dispatcher) createIntent(ctx context.Context, projectID string, intentData interface{}, workerName string) bool {
	intentMap, ok := intentData.(map[string]interface{})
	if !ok {
		return false
	}

	body := map[string]interface{}{
		"from":        intentMap["from"],
		"description": intentMap["description"],
		"creator":     workerName,
		"worker":      nil,
	}
	bodyBytes, _ := json.Marshal(body)

	url := fmt.Sprintf("%s/api/projects/%s/intents", d.cfg.Server, projectID)
	req, _ := http.NewRequestWithContext(ctx, "POST", url, strings.NewReader(string(bodyBytes)))
	req.Header.Set("Content-Type", "application/json")

	resp, err := d.client.Do(req)
	if err != nil {
		log.Printf("[dispatcher] create intent error: %v", err)
		return false
	}
	defer resp.Body.Close()
	log.Printf("[dispatcher] new intent created for project %s (status %d)", projectID, resp.StatusCode)
	return resp.StatusCode == http.StatusCreated
}

func (d *Dispatcher) concludeIntent(ctx context.Context, projectID, intentID, description, workerName string) {
	body := map[string]interface{}{
		"description": description,
		"worker":      workerName,
	}
	bodyBytes, _ := json.Marshal(body)

	url := fmt.Sprintf("%s/api/projects/%s/intents/%s/conclude", d.cfg.Server, projectID, intentID)
	req, _ := http.NewRequestWithContext(ctx, "POST", url, strings.NewReader(string(bodyBytes)))
	req.Header.Set("Content-Type", "application/json")

	resp, err := d.client.Do(req)
	if err != nil {
		log.Printf("[dispatcher] conclude intent error: %v", err)
		return
	}
	defer resp.Body.Close()
	log.Printf("[dispatcher] intent %s concluded for project %s (status %d)", intentID, projectID, resp.StatusCode)
}

// --- helpers ---

func (d *Dispatcher) selectWorker(taskType worker.TaskType) *config.WorkerConfig {
	d.mu.Lock()
	defer d.mu.Unlock()

	now := time.Now()
	var candidates []*config.WorkerConfig
	for i := range d.cfg.Workers {
		w := &d.cfg.Workers[i]
		if !d.workerSupportsTask(w, taskType) {
			continue
		}
		if d.workerRunning[w.Name] >= w.MaxRunning {
			continue
		}
		if unhealthyUntil, ok := d.workerUnhealthyUntil[w.Name]; ok && now.Before(unhealthyUntil) {
			continue
		}
		candidates = append(candidates, w)
	}

	if len(candidates) == 0 {
		return nil
	}

	// 按 priority 排序，同 priority 取负载最低，同负载随机
	best := candidates[0]
	for _, c := range candidates[1:] {
		if c.Priority < best.Priority {
			best = c
		} else if c.Priority == best.Priority {
			cLoad := d.workerRunning[c.Name]
			bLoad := d.workerRunning[best.Name]
			if cLoad < bLoad || (cLoad == bLoad && rand.Float64() < 0.5) {
				best = c
			}
		}
	}
	return best
}

// markWorkerUnhealthy 标记 worker 不健康，5 秒内不再选用
func (d *Dispatcher) markWorkerUnhealthy(workerName string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.workerUnhealthyUntil[workerName] = time.Now().Add(5 * time.Second)
	log.Printf("[dispatcher] worker marked unhealthy worker=%s retry_after=5s", workerName)
}

// markWorkerRejected 标记 (project, task, worker) 组合拒绝，5 秒内不再选用
func (d *Dispatcher) markWorkerRejected(projectID, taskType, workerName string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	key := rejectionKey{projectID, taskType, workerName}
	d.workerRejectedUntil[key] = time.Now().Add(5 * time.Second)
	log.Printf("[dispatcher] worker marked rejected project=%s task=%s worker=%s retry_after=5s", projectID, taskType, workerName)
}

func (d *Dispatcher) workerSupportsTask(w *config.WorkerConfig, taskType worker.TaskType) bool {
	for _, t := range w.TaskTypes {
		if worker.TaskType(t) == taskType {
			return true
		}
	}
	return false
}

func (d *Dispatcher) canDispatch() bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.runningTasks < d.cfg.Runtime.MaxWorkers
}

func (d *Dispatcher) admittedCount() int {
	count := 0
	for range d.admitted {
		count++
	}
	return count
}

func (d *Dispatcher) getTimeout(taskType worker.TaskType) int {
	switch taskType {
	case worker.TaskBootstrap:
		return d.cfg.Tasks.Bootstrap.Timeout
	case worker.TaskReason:
		return d.cfg.Tasks.Reason.Timeout
	case worker.TaskExplore:
		return d.cfg.Tasks.Explore.Timeout
	}
	return 300
}

func (d *Dispatcher) listProjects(ctx context.Context) ([]models.ProjectSummary, error) {
	url := fmt.Sprintf("%s/api/projects", d.cfg.Server)
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, err
	}

	resp, err := d.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	var projects []models.ProjectSummary
	if err := json.Unmarshal(body, &projects); err != nil {
		return nil, err
	}
	return projects, nil
}

func (d *Dispatcher) getProjectDetail(ctx context.Context, projectID string) (*models.ProjectDetailResponse, error) {
	url := fmt.Sprintf("%s/api/projects/%s", d.cfg.Server, projectID)
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, err
	}

	resp, err := d.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	var detail models.ProjectDetailResponse
	if err := json.Unmarshal(body, &detail); err != nil {
		return nil, err
	}
	return &detail, nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

// --- Server API helpers ---

// heartbeatIntent 通过 Server API 认领/续约 intent
func (d *Dispatcher) heartbeatIntent(ctx context.Context, projectID, intentID, workerName string) int {
	body := fmt.Sprintf(`{"worker":"%s"}`, workerName)
	url := fmt.Sprintf("%s/api/projects/%s/intents/%s/heartbeat", d.cfg.Server, projectID, intentID)
	req, _ := http.NewRequestWithContext(ctx, "POST", url, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")

	resp, err := d.client.Do(req)
	if err != nil {
		log.Printf("[dispatcher] heartbeat intent error: %v", err)
		return 0
	}
	defer resp.Body.Close()
	return resp.StatusCode
}

// releaseIntent 释放 intent 认领
func (d *Dispatcher) releaseIntent(ctx context.Context, projectID, intentID, workerName string) {
	body := fmt.Sprintf(`{"worker":"%s"}`, workerName)
	url := fmt.Sprintf("%s/api/projects/%s/intents/%s/release", d.cfg.Server, projectID, intentID)
	req, _ := http.NewRequestWithContext(ctx, "POST", url, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")

	resp, err := d.client.Do(req)
	if err != nil {
		log.Printf("[dispatcher] release intent error: %v", err)
		return
	}
	defer resp.Body.Close()
}

// claimReason 通过 Server API 认领 reason
func (d *Dispatcher) claimReason(ctx context.Context, projectID, workerName, trigger string) int {
	body := fmt.Sprintf(`{"worker":"%s","trigger":"%s"}`, workerName, trigger)
	url := fmt.Sprintf("%s/api/projects/%s/reason/claim", d.cfg.Server, projectID)
	req, _ := http.NewRequestWithContext(ctx, "POST", url, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")

	resp, err := d.client.Do(req)
	if err != nil {
		log.Printf("[dispatcher] claim reason error: %v", err)
		return 0
	}
	defer resp.Body.Close()
	return resp.StatusCode
}

// releaseReason 释放 reason 认领
func (d *Dispatcher) releaseReason(ctx context.Context, projectID, workerName string) {
	body := fmt.Sprintf(`{"worker":"%s"}`, workerName)
	url := fmt.Sprintf("%s/api/projects/%s/reason/release", d.cfg.Server, projectID)
	req, _ := http.NewRequestWithContext(ctx, "POST", url, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")

	resp, err := d.client.Do(req)
	if err != nil {
		log.Printf("[dispatcher] release reason error: %v", err)
		return
	}
	defer resp.Body.Close()
}

// exportProjectYAML 获取项目 YAML 导出
func (d *Dispatcher) exportProjectYAML(ctx context.Context, projectID string) (string, error) {
	url := fmt.Sprintf("%s/api/projects/%s/export?format=yaml", d.cfg.Server, projectID)
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return "", err
	}

	resp, err := d.client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("export returned status %d", resp.StatusCode)
	}
	return string(body), nil
}
