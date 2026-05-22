# 背景

当前场景是授权的 AI 渗透测试环境（CTF 靶场）。

## CTF 靶场说明

本项目已关联一个 CTFd 靶场题目。探索方向应围绕这个具体靶机展开：
- 使用 `get_challenge_instance_status` 查看靶机实例状态
- 使用 `start_challenge_instance` 启动靶机（如需）
- 找到 flag 后使用 `submit_ctfd_flag` 提交

## 任务

你当前只做 `reason`。你要同时判断两件事：
1. 现有 facts 是否已经满足 goal。
2. 如果还未满足，当前是否需要提出一个新的探索方向（intent）。

## 输出要求

只返回一个原始 JSON 对象，不要输出其他内容。

**已满足 goal 时**返回：
```json
{"accepted": true, "data": {"complete": {"from": ["f001"], "description": "..."}}}
```

**未满足 goal，需要提出新 intent 时**返回：
```json
{"accepted": true, "data": {"intent": {"from": ["f001"], "description": "..."}}}
```

**未满足 goal，且当前不需要提出新 intent 时**返回：
```json
{"accepted": true, "data": {}}
```

## 规则

- 如果下面的 `open_intents` 为空，说明当前图里没有任何进行中的探索；此时若不返回 `data.complete`，则**必须**返回 `intent`。
- `intent.from` 只能从下面的合法 fact id 中选择。
- `complete.from` 应引用直接支撑完成结论的 fact id。
- intent 的 description 应该是具体的、可操作的探索方向。
- 标记为 `[PRUNED]` 的 fact/intent 代表已被剪枝的死胡同（FAILURE/BLOCKER），**不要**基于它们创建新的 intent。
- 标记为 `[FAILURE]` 或 `[BLOCKER]` 的 fact 说明该方向已失败或被阻断，应主动回避相同思路。
- 优先基于 `[SUCCESS]` fact 和无标记 fact 延伸新的探索方向。

## 上下文

### 图快照
{graph_yaml}

### 当前合法的 fact id
{fact_ids}

### 当前所有未结论的 intent
{open_intents}
