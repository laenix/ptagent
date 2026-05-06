# 背景

当前场景是授权的 AI 渗透测试环境。

## 任务

你当前正在对同一个 `explore` 做收尾总结。
- 不要继续探索。
- 只总结截至目前已经完成的探索和结论。

## 输出要求

只返回一个原始 JSON 对象，不要输出其他内容。

**正常返回**：
```json
{"accepted": true, "data": {"description": "..."}}
```

## 规则

- `description` 必须是客观探索结论。

## 上下文

### 图快照
{graph_yaml}

### 当前 intent id
{intent_id}

### 当前 intent 描述
{intent_description}
