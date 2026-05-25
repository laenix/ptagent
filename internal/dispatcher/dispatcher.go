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
	"github.com/ptagent/ptagent/internal/toollogger"
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
	projectID  string
	taskType   worker.TaskType
	worker     string
	cancel     context.CancelFunc
	generation uint64 // 唯一标识，防止 cancelInactiveTasks 后旧 goroutine defer 误删新任务
}

// Dispatcher 核心调度器
type Dispatcher struct {
	cfg          *config.DispatchConfig
	client       *http.Client
	drivers      map[string]worker.Driver
	prompts      *PromptManager
	containerMgr *container.Manager                                              // 容器管理器（可选，nil 时本地执行）
	autoSubmitFn func(ctx context.Context, projectID, description string) string // 自动提交回调
	toolLogger   *toollogger.Logger                                              // 工具事件日志

	// 运行状态
	mu                    sync.Mutex
	runningTasks          int
	projectWorkers        map[string]int              // projectID -> running count
	workerRunning         map[string]int              // worker name -> running count
	admitted              map[string]bool             // admitted project IDs
	bootstrapping         map[string]bool             // projects currently in bootstrap
	bootstrapped          map[string]bool             // projects that have successfully bootstrapped
	reasoning             map[string]bool             // projects currently in reason
	exploringIntents      map[string]bool             // intent IDs currently being explored
	projectExploring      map[string]bool             // project IDs currently in explore (for direction lock)
	reasonCheckpoints     map[string]reasonCheckpoint // projectID -> last reason graph snapshot
	runningTasks_         map[string]*runningTask     // taskKey -> running task (for cancellation)
	workerUnhealthyUntil  map[string]time.Time        // worker name -> unhealthy until
	workerUnhealthyCount  map[string]int              // worker name -> consecutive unhealthy count
	workerLastHealthcheck map[string]time.Time        // worker name -> last healthcheck time
	workerRejectedUntil   map[rejectionKey]time.Time  // (project,task,worker) -> rejected until
	bootstrapRetries      map[string]int              // projectID -> bootstrap failure count
	bootstrapBackoff      map[string]time.Time        // projectID -> next retry allowed at
	taskGeneration        uint64                      // 递增的任务代号
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
		cfg:                   cfg,
		client:                &http.Client{Timeout: 30 * time.Second},
		drivers:               make(map[string]worker.Driver),
		projectWorkers:        make(map[string]int),
		workerRunning:         make(map[string]int),
		admitted:              make(map[string]bool),
		bootstrapping:         make(map[string]bool),
		bootstrapped:          make(map[string]bool),
		reasoning:             make(map[string]bool),
		exploringIntents:      make(map[string]bool),
		projectExploring:      make(map[string]bool),
		reasonCheckpoints:     make(map[string]reasonCheckpoint),
		runningTasks_:         make(map[string]*runningTask),
		workerUnhealthyUntil:  make(map[string]time.Time),
		workerUnhealthyCount:  make(map[string]int),
		workerLastHealthcheck: make(map[string]time.Time),
		workerRejectedUntil:   make(map[rejectionKey]time.Time),
		bootstrapRetries:      make(map[string]int),
		bootstrapBackoff:      make(map[string]time.Time),
	}

	// 初始化容器管理器（如果启用）
	if cfg.Container.Enabled {
		cm, err := container.New(&cfg.Container, cfg.Server)
		if err != nil {
			log.Printf("[dispatcher] container manager init failed (running in local mode): %v", err)
		} else {
			d.containerMgr = cm
			log.Printf("[dispatcher] container mode enabled, image=%s server=%s", cfg.Container.Image, cfg.Server)
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
		case "anthropic":
			drv = driver.NewAnthropicDriver(w.Name, w.Env, &cfg.Proxy)
		case "mock":
			drv = driver.NewMockDriver(w.Name, w.Env)
		default:
			return nil, fmt.Errorf("unsupported driver type: %s", w.Type)
		}
		d.drivers[w.Name] = drv
	}

	return d, nil
}

// SetAutoSubmitFunc 设置自动提交 flag 的回调函数
func (d *Dispatcher) SetAutoSubmitFunc(fn func(ctx context.Context, projectID, description string) string) {
	d.autoSubmitFn = fn
}

// SetToolLogger 设置工具事件日志记录器
func (d *Dispatcher) SetToolLogger(logger *toollogger.Logger) {
	d.toolLogger = logger
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
	d.mu.Lock()
	for id := range d.admitted {
		if !activeIDs[id] {
			delete(d.admitted, id)
			log.Printf("[dispatcher] removed non-active project %s from admitted set", id)
		}
	}
	d.mu.Unlock()
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

	// 1. 遍历已 admitted 的 active 项目（公平调度）
	for _, p := range projects {
		if p.Status != models.ProjectStatusActive {
			continue
		}

		d.mu.Lock()
		admitted := d.admitted[p.ID]
		d.mu.Unlock()
		if !admitted {
			continue
		}

		if !d.canDispatch() {
			log.Printf("[dispatcher] cannot dispatch, runningTasks=%d maxWorkers=%d", d.runningTasks, d.cfg.Runtime.MaxWorkers)
			break
		}

		// 检查项目是否已达配额
		d.mu.Lock()
		projectCount := d.projectWorkers[p.ID]
		if projectCount >= quota {
			d.mu.Unlock()
			log.Printf("[dispatcher] project %s已达配额(%d>=%d), 跳过", p.ID, projectCount, quota)
			continue
		}
		d.mu.Unlock()

		log.Printf("[dispatcher] 准备为项目 %s 调度, projectCount=%d quota=%d", p.ID, projectCount, quota)
		d.dispatchForProject(ctx, &p, quota)
	}

	// 2. admit 新项目（最多补充到 maxRunningProjects）
	d.mu.Lock()
	targetCount := d.cfg.Runtime.MaxRunningProjects - d.admittedCountLocked()
	if targetCount > 0 {
		for _, p := range projects {
			if p.Status != models.ProjectStatusActive {
				continue
			}
			if d.admitted[p.ID] {
				continue
			}
			d.admitted[p.ID] = true
			targetCount--
			log.Printf("[dispatcher] admitted project %s (%s)", p.ID, p.Title)
			if targetCount <= 0 {
				break
			}
		}
	}
	d.mu.Unlock()
}

// dispatchForProject 为单个项目调度任务（可能派发多个 explore）
// quota: 项目配额，超过则不再派发新任务
func (d *Dispatcher) dispatchForProject(ctx context.Context, p *models.ProjectSummary, quota int) {
	d.mu.Lock()
	projectCount := d.projectWorkers[p.ID]
	isReasoning := d.reasoning[p.ID]
	isExploring := d.projectExploring[p.ID]
	d.mu.Unlock()
	log.Printf("[dispatcher] dispatchForProject: project=%s facts=%d intents=%d unclaimed=%d reason=%v reasonState=%v exploring=%v projectCount=%d quota=%d",
		p.ID, p.FactCount, p.IntentCount, p.UnclaimedIntentCount, p.Reason != nil, isReasoning, isExploring, projectCount, quota)

	// 使用配额和 MaxProjectWorkers 中的较小值作为限制
	maxAllowed := quota
	if d.cfg.Runtime.MaxProjectWorkers < maxAllowed {
		maxAllowed = d.cfg.Runtime.MaxProjectWorkers
	}

	if projectCount >= maxAllowed {
		log.Printf("[dispatcher] project %s at quota limit (projectCount=%d >= maxAllowed=%d), skipping", p.ID, projectCount, maxAllowed)
		return
	}

	// 判断项目状态决定任务类型
	log.Printf("[dispatcher] dispatchForProject: project=%s isInitial=%v unclaimed=%d reason=%v",
		p.ID, d.isInitialState(p), p.UnclaimedIntentCount, p.Reason != nil)
	if d.isInitialState(p) {
		// Bootstrap - 只派遣一次，带退避重试
		d.mu.Lock()
		already := d.bootstrapping[p.ID]
		backoffUntil := d.bootstrapBackoff[p.ID]
		if !already && time.Now().Before(backoffUntil) {
			d.mu.Unlock()
			log.Printf("[dispatcher] bootstrap backoff for project %s until %s", p.ID, backoffUntil.Format(time.RFC3339))
			return
		}
		if !already {
			d.bootstrapping[p.ID] = true
		}
		d.mu.Unlock()
		if !already {
			d.dispatchTask(ctx, p.ID, worker.TaskBootstrap)
		}
	} else {
		// 获取 fresh detail 来判断任务类型（避免 p.UnclaimedIntentCount 过时问题）
		freshDetail, err := d.getProjectDetail(ctx, p.ID)
		if err != nil {
			log.Printf("[dispatcher] get project detail error: %v", err)
			return
		}

		// 检查是否有未认领的 intent
		hasUnclaimed := false
		for _, intent := range freshDetail.Intents {
			if intent.IsOpen() && !intent.IsClaimed() && !d.exploringIntents[intent.ID] {
				hasUnclaimed = true
				break
			}
		}

		if hasUnclaimed {
			// Explore - 检查项目是否已有其他方向的任务在运行
			d.mu.Lock()
			isReasoning := d.reasoning[p.ID]
			d.mu.Unlock()
			if isReasoning {
				log.Printf("[dispatcher] project %s is in reason state, skipping explore", p.ID)
				return
			}

			// 派发 explore 任务
			if !d.canDispatch() {
				log.Printf("[dispatcher] cannot dispatch explore: global capacity full runningTasks=%d maxWorkers=%d", d.runningTasks, d.cfg.Runtime.MaxWorkers)
				return
			}
			d.mu.Lock()
			pc := d.projectWorkers[p.ID]
			d.mu.Unlock()
			if pc >= maxAllowed {
				return
			}
			d.dispatchTask(ctx, p.ID, worker.TaskExplore)
		} else if p.Reason == nil {
			// Reason - 检查项目是否已有 Explore 在运行
			d.mu.Lock()
			hasExploring := d.projectExploring[p.ID]
			d.mu.Unlock()
			if hasExploring {
				log.Printf("[dispatcher] project %s has exploring task, skipping reason", p.ID)
				return
			}

			// Reason - 只有图有实质变化才触发
			trigger := d.reasonTrigger(p)
			if trigger == "" {
				log.Printf("[dispatcher] reasonTrigger empty for project %s, no graph change, skipping", p.ID)
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
		// 初始化检查点
		d.mu.Lock()
		d.reasonCheckpoints[p.ID] = reasonCheckpoint{
			factCount:       p.FactCount,
			hintCount:       p.HintCount,
			openIntentCount: openIntentCount,
		}
		d.mu.Unlock()
		return "initial"
	}

	var changes []string
	if p.FactCount > cp.factCount {
		changes = append(changes, fmt.Sprintf("facts:%d->%d", cp.factCount, p.FactCount))
	}
	if p.HintCount > cp.hintCount {
		changes = append(changes, fmt.Sprintf("hints:%d->%d", cp.hintCount, p.HintCount))
	}
	// 只要有 intent 被 concluded（openIntentCount 减少）就触发 Reason
	if openIntentCount < cp.openIntentCount {
		changes = append(changes, fmt.Sprintf("open_intents:%d->%d", cp.openIntentCount, openIntentCount))
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
	for _, rt := range d.runningTasks_ {
		status, exists := statusByID[rt.projectID]
		if !exists {
			status = "deleted"
		}
		if status != models.ProjectStatusActive {
			log.Printf("[dispatcher] cancelling task for inactive project project=%s task=%s worker=%s status=%s",
				rt.projectID, rt.taskType, rt.worker, status)
			rt.cancel()
			// 不从 runningTasks_ 中删除，让 goroutine 的 defer 自行清理计数器
		}
	}
}

// dispatchTask 派发任务
func (d *Dispatcher) dispatchTask(ctx context.Context, projectID string, taskType worker.TaskType, triggerArgs ...string) {
	// 选择 worker
	w := d.selectWorker(projectID, taskType)
	if w == nil {
		d.mu.Lock()
		running := d.runningTasks
		maxW := d.cfg.Runtime.MaxWorkers
		var workerStatus []string
		for _, wcfg := range d.cfg.Workers {
			if wcfg.Type == "openai" || wcfg.Type == "anthropic" {
				workerStatus = append(workerStatus, fmt.Sprintf("%s(run=%d max=%d)", wcfg.Name, d.workerRunning[wcfg.Name], wcfg.MaxRunning))
			}
		}
		d.mu.Unlock()
		log.Printf("[dispatcher] dispatchTask: no worker for %s runningTasks=%d maxWorkers=%d workers=%v", taskType, running, maxW, workerStatus)
		return
	}

	drv := d.drivers[w.Name]

	// 健康检查（缓存 2s 内的结果避免重复检查）
	d.mu.Lock()
	lastHC := d.workerLastHealthcheck[w.Name]
	d.mu.Unlock()
	if time.Since(lastHC) > 2*time.Second {
		hcCtx, cancel := context.WithTimeout(ctx, time.Duration(d.cfg.Runtime.HealthcheckTimeout)*time.Second)
		defer cancel()
		if err := drv.Healthcheck(hcCtx); err != nil {
			log.Printf("[dispatcher] worker %s healthcheck failed: %v", w.Name, err)
			d.markWorkerUnhealthy(w.Name)
			d.recordEvent(projectID, string(taskType), "", w.Name, "healthcheck_failed", "", "", err.Error(), 0)
			return
		}
		d.mu.Lock()
		d.workerLastHealthcheck[w.Name] = time.Now()
		delete(d.workerUnhealthyCount, w.Name) // 成功后重置退避计数
		d.mu.Unlock()
	}

	// 获取项目详情构造 prompt
	detail, err := d.getProjectDetail(ctx, projectID)
	if err != nil {
		log.Printf("[dispatcher] get project detail error: %v", err)
		return
	}

	// 上下文窗口优化：根据任务类型过滤图数据
	renderDetail := detail
	var exportYAML string
	switch taskType {
	case worker.TaskReason:
		// reason：剪枝后的图（折叠死胡同分支）
		renderDetail = pruneDeadEnds(detail)
		log.Printf("[dispatcher] reason: pruned graph facts=%d->%d intents=%d->%d",
			len(detail.Facts), len(renderDetail.Facts), len(detail.Intents), len(renderDetail.Intents))
	case worker.TaskExplore:
		// explore：仅传祖先链路（大幅减少 token）
		// 先选出要 explore 的 intent，再构建祖先链
		// 延迟到认领 intent 后再过滤
	}

	if taskType == worker.TaskReason || taskType == worker.TaskExplore {
		exportYAML, err = d.exportProjectYAML(ctx, projectID)
		if err != nil {
			log.Printf("[dispatcher] export YAML error: %v", err)
		}
	}

	prompt, err := d.prompts.RenderWithExport(taskType, renderDetail, exportYAML)
	if err != nil {
		log.Printf("[dispatcher] render prompt error: %v", err)
		return
	}

	// explore 时通过 Server API 认领 intent
	var intentID string
	if taskType == worker.TaskExplore && detail != nil {
		d.mu.Lock()
		var bestIntent string
		bestScore := -1
		for _, intent := range detail.Intents {
			if intent.IsOpen() && !intent.IsClaimed() && !d.exploringIntents[intent.ID] {
				score := scoreIntent(intent.Description)
				if score > bestScore {
					bestScore = score
					bestIntent = intent.ID
				}
			}
		}
		intentID = bestIntent
		d.mu.Unlock()
		if intentID == "" {
			log.Printf("[dispatcher] no available intent for explore in project %s, skipping", projectID)
			return
		}
		// 先标记本地占位，防止其他调度轮选中同一 intent
		d.mu.Lock()
		d.exploringIntents[intentID] = true
		d.mu.Unlock()
		// 通过 Server API 认领（原子操作）
		claimStatus := d.heartbeatIntent(ctx, projectID, intentID, w.Name)
		if claimStatus != http.StatusOK {
			// 认领失败，清除本地占位
			d.mu.Lock()
			delete(d.exploringIntents, intentID)
			d.mu.Unlock()
			log.Printf("[dispatcher] intent claim failed project=%s intent=%s status=%d", projectID, intentID, claimStatus)
			return
		}
		d.mu.Lock()
		d.projectExploring[projectID] = true
		d.mu.Unlock()

		// 上下文窗口优化：explore 只传祖先链路
		var targetIntent *models.Intent
		for _, intent := range detail.Intents {
			if intent.ID == intentID {
				targetIntent = &intent
				break
			}
		}
		if targetIntent != nil {
			ancestorDetail := extractAncestorChain(detail, targetIntent)
			// 用祖先链重新渲染 prompt
			ancestorPrompt, err := d.prompts.RenderWithExport(taskType, ancestorDetail, "")
			if err == nil {
				prompt = ancestorPrompt
				log.Printf("[dispatcher] explore: ancestor chain facts=%d (full=%d) intents=%d (full=%d)",
					len(ancestorDetail.Facts), len(detail.Facts), len(ancestorDetail.Intents), len(detail.Intents))
			}
		}

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
	d.taskGeneration++
	gen := d.taskGeneration
	d.runningTasks++
	d.projectWorkers[projectID]++
	d.workerRunning[w.Name]++
	d.runningTasks_[taskKey] = &runningTask{
		projectID:  projectID,
		taskType:   taskType,
		worker:     w.Name,
		cancel:     taskCancel,
		generation: gen,
	}
	d.mu.Unlock()

	wName := w.Name // capture for goroutine

	// 异步执行
	go func() {
		defer taskCancel()
		defer func() {
			d.mu.Lock()
			// 仅当 generation 匹配时才清理，避免误删被重新分配的 taskKey
			if cur, ok := d.runningTasks_[taskKey]; ok && cur.generation == gen {
				delete(d.runningTasks_, taskKey)
			}
			// 每个 goroutine 在注册时已计数 +1，结束时必须 -1（与 taskKey 是否被复用无关）
			if d.runningTasks > 0 {
				d.runningTasks--
			}
			if d.projectWorkers[projectID] > 0 {
				d.projectWorkers[projectID]--
			}
			if d.workerRunning[wName] > 0 {
				d.workerRunning[wName]--
			}
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
				// 无论成功失败都清除 checkpoint，下轮重新初始化准确快照
				d.mu.Lock()
				delete(d.reasoning, projectID)
				delete(d.reasonCheckpoints, projectID)
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

		if taskType == worker.TaskExplore {
			proxyURL := d.cfg.Proxy.HTTPSProxy
			if proxyURL == "" {
				proxyURL = d.cfg.Proxy.HTTPProxy
			}
			localExec := tools.NewExecutor(proxyURL)

			baseExec := func(ctx context.Context, name string, args map[string]interface{}) *tools.ToolResult {
				return localExec.Execute(ctx, name, args)
			}
			if d.containerMgr != nil {
				info, err := d.containerMgr.EnsureRunning(taskCtx, projectID)
				if err != nil {
					log.Printf("[dispatcher] container start failed for project %s: %v", projectID, err)
					d.recordEvent(projectID, string(taskType), intentID, wName, "failed", "", "", "container start: "+err.Error(), 0)
					d.releaseOnFailure(taskCtx, projectID, task, wName)
					return
				}
				containerInfo := info
				baseExec = func(ctx context.Context, name string, args map[string]interface{}) *tools.ToolResult {
					return d.containerMgr.ExecuteTool(ctx, containerInfo, name, args)
				}
			}

			observedExec := func(ctx context.Context, name string, args map[string]interface{}) *tools.ToolResult {
				start := time.Now()
				result := baseExec(ctx, name, args)
				if result == nil {
					result = &tools.ToolResult{Error: "tool returned nil result"}
				}
				d.emitToolEvent(ctx, &models.ToolEvent{
					ProjectID:  projectID,
					TaskType:   string(taskType),
					IntentID:   intentID,
					Worker:     wName,
					Tool:       name,
					Args:       mustJSON(args),
					Output:     result.Output,
					Error:      result.Error,
					DurationMs: time.Since(start).Milliseconds(),
				})
				return result
			}

			// 通过 context 注入执行器，避免修改共享 driver 实例（并发安全）
			taskCtx = driver.WithToolExecutor(taskCtx, observedExec)
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

		// 写回结果（checkpoint 在 defer 中统一清理）
		d.writeBack(ctx, projectID, task, result, wName)

		// Bootstrap 成功时清理标记并标记为已 bootstrap
		if taskType == worker.TaskBootstrap {
			d.mu.Lock()
			delete(d.bootstrapping, projectID)
			delete(d.bootstrapRetries, projectID)
			delete(d.bootstrapBackoff, projectID)
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
	// Bootstrap 失败时清理标记，增加退避
	if task.Type == worker.TaskBootstrap {
		d.mu.Lock()
		delete(d.bootstrapping, projectID)
		d.bootstrapRetries[projectID]++
		retries := d.bootstrapRetries[projectID]
		// 指数退避：10s, 20s, 40s, 80s, 最大 300s
		backoff := time.Duration(10<<(retries-1)) * time.Second
		if backoff > 300*time.Second {
			backoff = 300 * time.Second
		}
		d.bootstrapBackoff[projectID] = time.Now().Add(backoff)
		d.mu.Unlock()
		log.Printf("[dispatcher] bootstrap failed for project %s (retry #%d, next backoff %v)", projectID, retries, backoff)
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
	if resp.StatusCode >= 400 {
		log.Printf("[dispatcher] record event returned status %d for project %s phase %s", resp.StatusCode, projectID, phase)
	}
}

// recordToolEvent 通过 Server API 记录工具调用事件（写入 store）
func (d *Dispatcher) recordToolEvent(parent context.Context, projectID string, event *models.ToolEvent) {
	if event == nil {
		return
	}
	bodyBytes, _ := json.Marshal(event)
	url := fmt.Sprintf("%s/api/projects/%s/tools", d.cfg.Server, projectID)
	ctx, cancel := context.WithTimeout(parent, 5*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, "POST", url, strings.NewReader(string(bodyBytes)))
	req.Header.Set("Content-Type", "application/json")
	resp, err := d.client.Do(req)
	if err != nil {
		log.Printf("[dispatcher] record tool event error: %v", err)
		return
	}
	resp.Body.Close()
	if resp.StatusCode >= 400 {
		log.Printf("[dispatcher] record tool event returned status %d for project %s tool %s", resp.StatusCode, projectID, event.Tool)
	}
}

func (d *Dispatcher) emitToolEvent(parent context.Context, event *models.ToolEvent) {
	if event == nil {
		return
	}
	d.recordToolEvent(parent, event.ProjectID, event)
	if d.toolLogger == nil {
		return
	}
	if err := d.toolLogger.Record(event); err != nil {
		log.Printf("[dispatcher] tool logger error: %v", err)
	}
}

func mustJSON(v interface{}) string {
	b, err := json.Marshal(v)
	if err != nil {
		return "{}"
	}
	return string(b)
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
			// 自动标记 [SUCCESS]/[FAILURE]/[BLOCKER]
			desc = tagFactDescription(desc)
			log.Printf("[dispatcher] explore concluded for project %s: %s", projectID, truncate(desc, 80))
			if task.IntentID != "" {
				d.concludeIntent(ctx, projectID, task.IntentID, desc, workerName)
			}
			// 尝试自动提交 flag
			if d.autoSubmitFn != nil {
				if result := d.autoSubmitFn(ctx, projectID, desc); result != "" {
					log.Printf("[dispatcher] auto-submit for project %s: %s", projectID, result)
				}
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

func (d *Dispatcher) selectWorker(projectID string, taskType worker.TaskType) *config.WorkerConfig {
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
		rejKey := rejectionKey{projectID, string(taskType), w.Name}
		if rejUntil, ok := d.workerRejectedUntil[rejKey]; ok && now.Before(rejUntil) {
			continue
		}
		candidates = append(candidates, w)
	}

	if len(candidates) == 0 {
		return nil
	}

	// 按 priority 分组，每组内按负载排序，同负载随机
	// 先找最低 priority
	minPriority := candidates[0].Priority
	for _, c := range candidates {
		if c.Priority < minPriority {
			minPriority = c.Priority
		}
	}
	// 收集所有等于最低 priority 的候选
	var bestCandidates []*config.WorkerConfig
	minLoad := -1
	for _, c := range candidates {
		if c.Priority != minPriority {
			continue
		}
		load := d.workerRunning[c.Name]
		if minLoad < 0 || load < minLoad {
			minLoad = load
			bestCandidates = []*config.WorkerConfig{c}
		} else if load == minLoad {
			bestCandidates = append(bestCandidates, c)
		}
	}
	// 同负载的 worker 中随机选一个
	if len(bestCandidates) > 0 {
		return bestCandidates[rand.IntN(len(bestCandidates))]
	}
	return candidates[0]
}

// markWorkerUnhealthy 标记 worker 不健康，指数退避（5s, 10s, 20s, ..., 最大 300s）
func (d *Dispatcher) markWorkerUnhealthy(workerName string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.workerUnhealthyCount[workerName]++
	count := d.workerUnhealthyCount[workerName]
	backoff := time.Duration(5<<(count-1)) * time.Second
	if backoff > 300*time.Second {
		backoff = 300 * time.Second
	}
	d.workerUnhealthyUntil[workerName] = time.Now().Add(backoff)
	log.Printf("[dispatcher] worker marked unhealthy worker=%s count=%d retry_after=%v", workerName, count, backoff)
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
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.admittedCountLocked()
}

// admittedCountLocked 在已持有 mu 的情况下调用
func (d *Dispatcher) admittedCountLocked() int {
	return len(d.admitted)
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

// tagFactDescription 根据内容自动标记 [SUCCESS]/[FAILURE]/[BLOCKER]
func tagFactDescription(desc string) string {
	lower := strings.ToLower(desc)

	// 已有标签则不重复
	if strings.HasPrefix(desc, "[SUCCESS]") || strings.HasPrefix(desc, "[FAILURE]") || strings.HasPrefix(desc, "[BLOCKER]") {
		return desc
	}

	// BLOCKER 关键词
	blockerKW := []string{
		"connection refused", "no route to host", "host unreachable",
		"service not running", "port closed", "filtered",
		"access denied permanently", "firewall blocked",
	}
	for _, kw := range blockerKW {
		if strings.Contains(lower, kw) {
			return "[BLOCKER] " + desc
		}
	}

	// FAILURE 关键词
	failureKW := []string{
		"not vulnerable", "failed", "exploit failed", "no vulnerability",
		"timeout", "unable to", "could not", "unsuccessful",
		"permission denied", "authentication failed", "payload did not",
		"no results", "nothing found", "未发现", "失败", "无法",
	}
	for _, kw := range failureKW {
		if strings.Contains(lower, kw) {
			return "[FAILURE] " + desc
		}
	}

	// SUCCESS 关键词
	successKW := []string{
		"found", "discovered", "vulnerable", "exploited", "obtained",
		"credential", "password", "token", "flag", "shell", "access gained",
		"rce confirmed", "injection successful", "leaked",
		"发现", "成功", "获取", "获得",
	}
	for _, kw := range successKW {
		if strings.Contains(lower, kw) {
			return "[SUCCESS] " + desc
		}
	}

	return desc
}

// extractAncestorChain 提取 intent 的祖先链路（只保留从 origin 到当前 intent 的 fact/intent 路径）
func extractAncestorChain(detail *models.ProjectDetailResponse, targetIntent *models.Intent) *models.ProjectDetailResponse {
	if detail == nil || targetIntent == nil {
		return detail
	}

	// BFS 反向追溯祖先 fact
	neededFacts := make(map[string]bool)
	neededIntents := make(map[string]bool)

	// 始终包含 origin 和 goal
	neededFacts["origin"] = true
	neededFacts["goal"] = true

	// 当前 intent 的 from facts 入队
	queue := make([]string, 0)
	for _, fromID := range targetIntent.From {
		neededFacts[fromID] = true
		queue = append(queue, fromID)
	}

	// 建索引：fact -> 产生它的 intent
	factProducer := make(map[string]*models.Intent)
	for i := range detail.Intents {
		intent := &detail.Intents[i]
		if intent.To != nil {
			factProducer[*intent.To] = intent
		}
	}

	// BFS 追溯
	for len(queue) > 0 {
		factID := queue[0]
		queue = queue[1:]

		producer, ok := factProducer[factID]
		if !ok {
			continue
		}
		neededIntents[producer.ID] = true
		for _, fromID := range producer.From {
			if !neededFacts[fromID] {
				neededFacts[fromID] = true
				queue = append(queue, fromID)
			}
		}
	}

	// 包含目标 intent 本身
	neededIntents[targetIntent.ID] = true

	// 构造过滤后的 detail
	filteredFacts := make([]models.Fact, 0)
	for _, f := range detail.Facts {
		if neededFacts[f.ID] {
			filteredFacts = append(filteredFacts, f)
		}
	}

	filteredIntents := make([]models.Intent, 0)
	for _, i := range detail.Intents {
		if neededIntents[i.ID] {
			filteredIntents = append(filteredIntents, i)
		}
	}

	return &models.ProjectDetailResponse{
		Project: detail.Project,
		Facts:   filteredFacts,
		Intents: filteredIntents,
		Hints:   detail.Hints,
	}
}

// pruneDeadEnds 图剪枝：折叠 BLOCKER/FAILURE 死胡同分支，返回剪枝后的 detail 副本
func pruneDeadEnds(detail *models.ProjectDetailResponse) *models.ProjectDetailResponse {
	if detail == nil {
		return nil
	}

	// 标记死胡同 fact（以 [BLOCKER] 或 [FAILURE] 开头）
	deadFacts := make(map[string]bool)
	for _, f := range detail.Facts {
		if strings.HasPrefix(f.Description, "[BLOCKER]") || strings.HasPrefix(f.Description, "[FAILURE]") {
			deadFacts[f.ID] = true
		}
	}

	// 保留存活的 facts 和 intents，死胡同压缩为摘要
	prunedFacts := make([]models.Fact, 0, len(detail.Facts))
	for _, f := range detail.Facts {
		if f.ID == "origin" || f.ID == "goal" || !deadFacts[f.ID] {
			prunedFacts = append(prunedFacts, f)
		} else {
			prunedFacts = append(prunedFacts, models.Fact{
				ID:          f.ID,
				Description: "[PRUNED] " + truncate(f.Description, 60),
			})
		}
	}

	prunedIntents := make([]models.Intent, 0, len(detail.Intents))
	for _, i := range detail.Intents {
		if i.To != nil && deadFacts[*i.To] {
			prunedIntents = append(prunedIntents, models.Intent{
				ID:          i.ID,
				From:        i.From,
				To:          i.To,
				Description: "[PRUNED] " + truncate(i.Description, 40),
				Creator:     i.Creator,
				ConcludedAt: i.ConcludedAt,
			})
		} else {
			prunedIntents = append(prunedIntents, i)
		}
	}

	return &models.ProjectDetailResponse{
		Project: detail.Project,
		Facts:   prunedFacts,
		Intents: prunedIntents,
		Hints:   detail.Hints,
	}
}

// scoreIntent 按关键词对 intent 打分，高价值优先调度
// flag > exploit > credential/shell > privilege > injection > recon
func scoreIntent(description string) int {
	lower := strings.ToLower(description)
	score := 0

	// 最高优先级：直接目标相关
	flagKeywords := []string{"flag", "ctf", "getflag", "get_flag", "read flag", "capture"}
	for _, kw := range flagKeywords {
		if strings.Contains(lower, kw) {
			score += 100
		}
	}

	// 高优先级：利用类
	exploitKeywords := []string{"exploit", "rce", "remote code", "command injection", "code execution",
		"reverse shell", "shell", "payload", "漏洞利用", "getshell"}
	for _, kw := range exploitKeywords {
		if strings.Contains(lower, kw) {
			score += 80
		}
	}

	// 中高优先级：凭证/提权
	credentialKeywords := []string{"credential", "password", "token", "secret", "api_key", "apikey",
		"jwt", "session", "cookie", "auth", "login", "密码", "凭证", "提权", "privilege", "escalat", "sudo", "suid"}
	for _, kw := range credentialKeywords {
		if strings.Contains(lower, kw) {
			score += 60
		}
	}

	// 中优先级：注入类
	injectionKeywords := []string{"inject", "sqli", "sql注入", "xss", "ssrf", "lfi", "rfi",
		"deserialization", "反序列化", "file inclusion", "upload"}
	for _, kw := range injectionKeywords {
		if strings.Contains(lower, kw) {
			score += 40
		}
	}

	// 低优先级：侦查
	reconKeywords := []string{"scan", "enum", "recon", "discover", "扫描", "探测", "侦查",
		"dirsearch", "nmap", "nikto", "information", "fingerprint"}
	for _, kw := range reconKeywords {
		if strings.Contains(lower, kw) {
			score += 20
		}
	}

	return score
}

// --- Server API helpers ---

// heartbeatIntent 通过 Server API 认领/续约 intent
func (d *Dispatcher) heartbeatIntent(ctx context.Context, projectID, intentID, workerName string) int {
	bodyBytes, _ := json.Marshal(map[string]string{"worker": workerName})
	url := fmt.Sprintf("%s/api/projects/%s/intents/%s/heartbeat", d.cfg.Server, projectID, intentID)
	req, _ := http.NewRequestWithContext(ctx, "POST", url, strings.NewReader(string(bodyBytes)))
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
	bodyBytes, _ := json.Marshal(map[string]string{"worker": workerName})
	url := fmt.Sprintf("%s/api/projects/%s/intents/%s/release", d.cfg.Server, projectID, intentID)
	req, _ := http.NewRequestWithContext(ctx, "POST", url, strings.NewReader(string(bodyBytes)))
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
	bodyBytes, _ := json.Marshal(map[string]string{"worker": workerName, "trigger": trigger})
	url := fmt.Sprintf("%s/api/projects/%s/reason/claim", d.cfg.Server, projectID)
	req, _ := http.NewRequestWithContext(ctx, "POST", url, strings.NewReader(string(bodyBytes)))
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
	bodyBytes, _ := json.Marshal(map[string]string{"worker": workerName})
	url := fmt.Sprintf("%s/api/projects/%s/reason/release", d.cfg.Server, projectID)
	req, _ := http.NewRequestWithContext(ctx, "POST", url, strings.NewReader(string(bodyBytes)))
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
