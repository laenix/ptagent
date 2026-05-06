package corrector

import (
	"log"
	"strings"
	"sync"
	"time"
)

// Corrector 纠偏引擎
// 综合多支队伍的纠偏方案：防降智、防沉迷、ABANDON 停损
type Corrector struct {
	mu sync.Mutex

	// 防沉迷：同一命令连续调用检测
	recentCalls map[string][]time.Time // key: projectID+command -> timestamps
	maxRepeats  int

	// ABANDON 停损：三层检测
	abandonKeywords []string
	abandonPatterns []string
	abandonCVEs     map[string]int // CVE编号 -> 尝试次数

	// 防降智：上下文质量检测
	factQuality map[string][]float64 // projectID -> quality scores
}

// NewCorrector 创建纠偏引擎
func NewCorrector() *Corrector {
	return &Corrector{
		recentCalls:     make(map[string][]time.Time),
		maxRepeats:      3,
		abandonKeywords: defaultAbandonKeywords(),
		abandonPatterns: defaultAbandonPatterns(),
		abandonCVEs:     make(map[string]int),
		factQuality:     make(map[string][]float64),
	}
}

// CheckRepetition 防沉迷检查：同一操作是否重复过多
func (c *Corrector) CheckRepetition(projectID, command string) *CorrectionAction {
	c.mu.Lock()
	defer c.mu.Unlock()

	key := projectID + ":" + command
	now := time.Now()

	// 清理过期记录（5分钟内）
	window := 5 * time.Minute
	calls := c.recentCalls[key]
	var recent []time.Time
	for _, t := range calls {
		if now.Sub(t) < window {
			recent = append(recent, t)
		}
	}
	recent = append(recent, now)
	c.recentCalls[key] = recent

	if len(recent) > c.maxRepeats {
		log.Printf("[corrector] repetition detected: %s called %d times in %v", command, len(recent), window)
		return &CorrectionAction{
			Type:    ActionForceSwitch,
			Reason:  "command_repetition",
			Message: "同一命令重复执行超过阈值，强制换方向",
		}
	}
	return nil
}

// CheckAbandon ABANDON 停损检查：三层匹配
func (c *Corrector) CheckAbandon(projectID, output string) *CorrectionAction {
	c.mu.Lock()
	defer c.mu.Unlock()

	// 第一层：关键词匹配
	for _, kw := range c.abandonKeywords {
		if strings.Contains(strings.ToLower(output), strings.ToLower(kw)) {
			return &CorrectionAction{
				Type:    ActionAbandon,
				Reason:  "keyword_match:" + kw,
				Message: "检测到停损关键词，建议放弃当前方向",
			}
		}
	}

	// 第二层：CVE 编号追踪
	cves := extractCVEs(output)
	for _, cve := range cves {
		c.abandonCVEs[projectID+":"+cve]++
		if c.abandonCVEs[projectID+":"+cve] > 3 {
			return &CorrectionAction{
				Type:    ActionAbandon,
				Reason:  "cve_exhausted:" + cve,
				Message: "同一 CVE 尝试超过 3 次，建议放弃",
			}
		}
	}

	return nil
}

// CheckDegradation 防降智检查：评估 Fact 质量趋势
func (c *Corrector) CheckDegradation(projectID string, factDescription string) *CorrectionAction {
	c.mu.Lock()
	defer c.mu.Unlock()

	// 简单启发式：描述长度作为质量代理指标
	quality := float64(len(factDescription))
	if quality < 10 {
		quality = 0.1 // 极短描述 = 低质量
	} else if quality > 200 {
		quality = 1.0
	} else {
		quality = quality / 200.0
	}

	scores := c.factQuality[projectID]
	scores = append(scores, quality)
	c.factQuality[projectID] = scores

	// 检测连续下降趋势（最近 5 个 fact 质量持续下降）
	if len(scores) >= 5 {
		recent := scores[len(scores)-5:]
		declining := true
		for i := 1; i < len(recent); i++ {
			if recent[i] >= recent[i-1] {
				declining = false
				break
			}
		}
		if declining {
			return &CorrectionAction{
				Type:    ActionResetContext,
				Reason:  "quality_degradation",
				Message: "检测到输出质量持续下降，建议重置上下文",
			}
		}
	}

	return nil
}

// GenerateAdvisorHint 生成 Advisor 建议（独立旁观者模式）
func (c *Corrector) GenerateAdvisorHint(projectID string, factCount, intentCount int) *string {
	// 当探索过深但无进展时，生成宏观建议
	if intentCount > 10 && factCount < 5 {
		hint := "PRIORITY: 当前探索分支过多但成果不足。建议：收缩方向，优先深入已有发现。"
		return &hint
	}

	if factCount > 15 && intentCount == 0 {
		hint := "DO: 信息充分但缺乏行动。建议：基于现有发现制定具体漏洞利用计划。"
		return &hint
	}

	return nil
}

// --- Types ---

// ActionType 纠偏动作类型
type ActionType string

const (
	ActionForceSwitch  ActionType = "force_switch"  // 强制换方向
	ActionAbandon      ActionType = "abandon"       // 放弃当前 intent
	ActionResetContext ActionType = "reset_context" // 重置上下文
	ActionInjectHint   ActionType = "inject_hint"   // 注入 hint
)

// CorrectionAction 纠偏动作
type CorrectionAction struct {
	Type    ActionType `json:"type"`
	Reason  string     `json:"reason"`
	Message string     `json:"message"`
}

// --- helpers ---

func defaultAbandonKeywords() []string {
	return []string{
		"connection refused",
		"no route to host",
		"permission denied",
		"not vulnerable",
		"exploit failed",
		"payload did not execute",
	}
}

func defaultAbandonPatterns() []string {
	return []string{
		`same error \d+ times`,
		`timeout after \d+ attempts`,
	}
}

func extractCVEs(text string) []string {
	var cves []string
	// 简单的 CVE 提取
	parts := strings.Fields(text)
	for _, p := range parts {
		if strings.HasPrefix(strings.ToUpper(p), "CVE-") && len(p) > 8 {
			cves = append(cves, strings.ToUpper(p))
		}
	}
	return cves
}
