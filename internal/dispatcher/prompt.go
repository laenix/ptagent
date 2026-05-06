package dispatcher

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/ptagent/ptagent/internal/models"
	"github.com/ptagent/ptagent/internal/worker"
)

// PromptManager 管理 prompt 模板的加载与渲染
type PromptManager struct {
	group    string
	basePath string
}

// NewPromptManager 创建 prompt 管理器
func NewPromptManager(group string) *PromptManager {
	return &PromptManager{
		group:    group,
		basePath: "prompts",
	}
}

// Render 渲染指定任务类型的 prompt
func (pm *PromptManager) Render(taskType worker.TaskType, detail *models.ProjectDetailResponse) (string, error) {
	var filename string
	switch taskType {
	case worker.TaskBootstrap:
		filename = "bootstrap.md"
	case worker.TaskReason:
		filename = "reason.md"
	case worker.TaskExplore:
		filename = "explore.md"
	default:
		return "", fmt.Errorf("unknown task type: %s", taskType)
	}

	template, err := pm.loadTemplate(filename)
	if err != nil {
		return "", err
	}

	return pm.renderTemplateWithYAML(template, taskType, detail, pm.buildGraphYAML(detail)), nil
}

// RenderWithExport 使用 Server 导出的 YAML 渲染 prompt
func (pm *PromptManager) RenderWithExport(taskType worker.TaskType, detail *models.ProjectDetailResponse, exportYAML string) (string, error) {
	var filename string
	switch taskType {
	case worker.TaskBootstrap:
		filename = "bootstrap.md"
	case worker.TaskReason:
		filename = "reason.md"
	case worker.TaskExplore:
		filename = "explore.md"
	default:
		return "", fmt.Errorf("unknown task type: %s", taskType)
	}

	template, err := pm.loadTemplate(filename)
	if err != nil {
		return "", err
	}

	// 如果有 Server 导出的 YAML，直接传入渲染，不再内置构建 graph YAML
	graphYAML := exportYAML
	if graphYAML == "" && detail != nil {
		graphYAML = pm.buildGraphYAML(detail)
	}
	return pm.renderTemplateWithYAML(template, taskType, detail, graphYAML), nil
}

// RenderConclude 渲染收尾 prompt
func (pm *PromptManager) RenderConclude(taskType worker.TaskType, detail *models.ProjectDetailResponse) (string, error) {
	var filename string
	switch taskType {
	case worker.TaskBootstrap:
		filename = "bootstrap_conclude.md"
	case worker.TaskExplore:
		filename = "explore_conclude.md"
	default:
		return "", fmt.Errorf("no conclude template for task type: %s", taskType)
	}

	template, err := pm.loadTemplate(filename)
	if err != nil {
		return "", err
	}

	return pm.renderTemplateWithYAML(template, taskType, detail, pm.buildGraphYAML(detail)), nil
}

func (pm *PromptManager) loadTemplate(filename string) (string, error) {
	path := filepath.Join(pm.basePath, pm.group, filename)
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("load template %s: %w", path, err)
	}
	return string(data), nil
}

func (pm *PromptManager) renderTemplateWithYAML(template string, taskType worker.TaskType, detail *models.ProjectDetailResponse, graphYAML string) string {
	if detail == nil {
		return strings.ReplaceAll(template, "{graph_yaml}", graphYAML)
	}

	// 提取 origin 和 goal
	var origin, goal string
	for _, f := range detail.Facts {
		switch f.ID {
		case "origin":
			origin = f.Description
		case "goal":
			goal = f.Description
		}
	}

	// 构造 hints JSON（使用 json.Marshal 确保安全转义）
	type hintEntry struct {
		Content string `json:"content"`
		Creator string `json:"creator"`
	}
	hintEntries := make([]hintEntry, 0, len(detail.Hints))
	for _, h := range detail.Hints {
		hintEntries = append(hintEntries, hintEntry{Content: h.Content, Creator: h.Creator})
	}
	hintsBytes, _ := json.Marshal(hintEntries)
	hintsJSON := string(hintsBytes)

	// 构造 fact_ids JSON
	factIDs := make([]string, 0)
	for _, f := range detail.Facts {
		if f.ID != "goal" {
			factIDs = append(factIDs, f.ID)
		}
	}
	factIDsBytes, _ := json.Marshal(factIDs)
	factIDsJSON := string(factIDsBytes)

	// 构造 open_intents JSON
	type intentEntry struct {
		ID          string   `json:"id"`
		From        []string `json:"from,omitempty"`
		Description string   `json:"description"`
		Worker      *string  `json:"worker,omitempty"`
	}
	intentEntries := make([]intentEntry, 0)
	for _, i := range detail.Intents {
		if i.IsOpen() {
			intentEntries = append(intentEntries, intentEntry{
				ID:          i.ID,
				From:        i.From,
				Description: i.Description,
				Worker:      i.Worker,
			})
		}
	}
	openIntentsBytes, _ := json.Marshal(intentEntries)
	openIntentsJSON := string(openIntentsBytes)

	// 替换占位符
	result := template
	result = strings.ReplaceAll(result, "{origin}", origin)
	result = strings.ReplaceAll(result, "{goal}", goal)
	result = strings.ReplaceAll(result, "{hints}", hintsJSON)
	result = strings.ReplaceAll(result, "{graph_yaml}", graphYAML)
	result = strings.ReplaceAll(result, "{fact_ids}", factIDsJSON)
	result = strings.ReplaceAll(result, "{open_intents}", openIntentsJSON)
	result = strings.ReplaceAll(result, "{max_intents}", "3")

	// Explore 特有
	if taskType == worker.TaskExplore {
		// 找第一个未认领 intent
		for _, i := range detail.Intents {
			if i.IsOpen() && !i.IsClaimed() {
				result = strings.ReplaceAll(result, "{intent_id}", i.ID)
				result = strings.ReplaceAll(result, "{intent_description}", i.Description)
				break
			}
		}
	}

	return result
}

func (pm *PromptManager) buildGraphYAML(detail *models.ProjectDetailResponse) string {
	var sb strings.Builder
	sb.WriteString("project:\n")
	sb.WriteString(fmt.Sprintf("  title: %q\n", detail.Project.Title))

	// origin and goal
	for _, f := range detail.Facts {
		if f.ID == "origin" {
			sb.WriteString(fmt.Sprintf("  origin: %q\n", f.Description))
		}
		if f.ID == "goal" {
			sb.WriteString(fmt.Sprintf("  goal: %q\n", f.Description))
		}
	}

	// hints
	if len(detail.Hints) > 0 {
		sb.WriteString("\nhints:\n")
		for _, h := range detail.Hints {
			sb.WriteString(fmt.Sprintf("  - content: %q\n    creator: %q\n", h.Content, h.Creator))
		}
	}

	// facts
	sb.WriteString("\nfacts:\n")
	for _, f := range detail.Facts {
		sb.WriteString(fmt.Sprintf("  - id: %s\n    description: %q\n", f.ID, f.Description))
	}

	// intents
	if len(detail.Intents) > 0 {
		sb.WriteString("\nintents:\n")
		for _, i := range detail.Intents {
			sb.WriteString(fmt.Sprintf("  - from: [%s]\n", strings.Join(i.From, ", ")))
			if i.To != nil {
				sb.WriteString(fmt.Sprintf("    to: %s\n", *i.To))
			} else {
				sb.WriteString("    to: null\n")
			}
			sb.WriteString(fmt.Sprintf("    description: %q\n", i.Description))
			sb.WriteString(fmt.Sprintf("    creator: %q\n", i.Creator))
			if i.Worker != nil {
				sb.WriteString(fmt.Sprintf("    worker: %q\n", *i.Worker))
			} else {
				sb.WriteString("    worker: null\n")
			}
		}
	}

	return sb.String()
}
