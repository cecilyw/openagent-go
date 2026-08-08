# openagent-go 架构重构：从旧架构到新架构

> 本文面向**熟悉旧代码的开发者**：旧文件去哪了、为什么改、怎么迁移。

## 1. 一句话

**旧：Agent 是上帝对象，一切能力挂在配置上，runner.go 单文件驱动循环。
新：Agent 是纯配置（只负责 Reasoning/Decision），kernel.Runtime 是执行引擎，Context/Execution/Governance 三运行时分工，能力全部 Provider 化。**

```
旧：Agent(God Object) → runner → Memory / Tool / PromptBuilder / LLM
新：应用层 (rest/acp/cli) 组装 cfg + deps → kernel.Runtime
     ├─ context.ContextRuntime   (AgentContext 组装：知识/技能/资源注入)
     ├─ execution.ExecutionRuntime (工具执行：Job 化/流式/重试)
     ├─ governance.PolicyEngine  (审批策略链)
     └─ session.Runtime          (会话生命周期)
```

## 2. 模块级对照（旧代码去哪了）

| 旧 | 新 | 变化点 |
|---|---|---|
| `agent.Agent`（持有 Model/Tools/Memory/Prompt/Guards/Approver/Hooks/Observer） | `agent.Agent`（只剩 Model/Prompt/Guards/SubAgents/限额）+ `kernel.Deps`（Tools/SessionStore/Compressor/MemoryProvider/Policy/Hooks/Observer/Providers） | **能力从配置剥离到运行时依赖**。旧 `agent.New(...)` + `WithMemory/WithTools/WithApprover` 全删 |
| `runner.go`（1700 行，`r.agent.X` 间接访问） | `kernel/` 按 8-node 循环拆方法：`run.go`（主循环）/`prompt.go`/`modelcall.go`/`execute.go`/`cancel.go`/`prepare.go`/`subagent.go`/`as_tool.go` | 每节点可单测；run() 只编排 |
| `openagent.Memory`（Recent/Search/Compact/Append 一个接口） | `session.SessionStore`（会话）+ `session.Compressor`（压缩）+ `context.MemoryProvider`（知识） | **一拆三**，接口定义在使用方（Go 惯例）；`Search` 死方法删除（被知识 Recall 取代） |
| `memory/sqlite`、`memory/file`（三合一实现） | `session/sqlite`、`session/file`（会话，`MessageStore` 类型）+ `provider/memory/sqlite`、`provider/memory/file`（知识） | **物理按域拆分**——会话与知识各自独立连接打开同一 .db（WAL 安全），知识表独立 |
| `PromptBuilder`（system+message+memory+skill 全组装） | `context.ContextRuntime.Build → AgentContext`（知识/技能/资源注入）+ `PromptBuilder`（AgentContext → messages） | **PromptBuilder 只管格式化**，上下文组装归 Context Runtime |
| `Approver`（`Approve() (bool, string)` 布尔二元） | `governance.PolicyEngine`：Rules → Safety → Memory → Human 四层链，`Decision{Action, Reason, ModifiedArgs}` | 审批升级为策略引擎；规则/安全/记忆/人工各司其职；审批记忆持久化（`ApprovalMemory`） |
| `SelfApproving.CanSelfApprove`（工具自报安全） | `governance.ToolClassifier`（平台只读白名单） | **安全分类归平台**，工具不再自报（全删） |
| 工具参数手写 JSON Schema + `json.Unmarshal` 匿名 struct | 根包中立 `Parameters` 模型：`SchemaOf[T]()` 定义、`ParseArgs[T]` 解析 | **强类型**，定义与解析同构永不漂移；JSON 即 JSON Schema，OpenAI/Anthropic/MCP 零转换 |
| `Tool.Execute(ctx, args) (string, error)` 双返回 | `Execute(ctx, args) *ToolResult` 单返回（错误进 `result.Error`） | 单通道；`ToolResult` 结构化（Content/JSON/Metadata/Truncated/FileRef/Error） |
| 超长工具结果 hooks/artifact 落盘 hack | `DefaultResultPolicy`（token 阈值 → 落盘 → FileRef 指针） | runtime 内建截断 |
| `SkillLoader`（启动全量 Discover → 全量进 prompt） | `provider/skill.Provider`（`Match(intent)` 意图匹配 ≤5 条注入 + Discover/Load） | **Skill 是 Context**——按需加载；`Agent.SkillLoader` 字段删 |
| 动态 `subagent` 内置工具（模型自造 name/prompt） | `agent.SubAgents` 配置化（Name/Description/SystemPrompt/Tools/Model/MaxTurns）→ 注册为委托工具，`runChild` 嵌套 Runtime | **子 agent 身份由配置声明**，模型只传 task；子 agent 用自己的系统提示、工具白名单；治理继承策略链（委托自动放行，内部照常审批） |
| rest/acp 各自拼 store+memory 管会话 | `session.Runtime`（Create/Save/Get/List/Restore/Checkpoint/Delete，组合元数据+消息） | 统一生命周期 API；Checkpoint 崩溃恢复点；`Store` 字段不再被应用层持有 |
| 规则版 `Extractor`（英文正则提取） | `LLMExtractor`（提取+分类 ADD/UPDATE/SKIP，Mem0 式）+ `AsyncExtractor`（单 worker 后台，按 user 合并去重） | **中文/精炼/更新语义**；不阻塞主 loop；存储层 topic upsert 防膨胀 |
| 旧 Summarizer（`openai.NewSummarizer(apiKey, modelID, baseURL)`） | `summarizer.New(model)`（接受 Model 接口） | 双实现收敛 |
| `Capabilities` 开关（每个能力 flag） | settings.json `"capabilities"` 配置驱动 + flag 仅覆盖 | 已配置化（此前是待办，查代码发现已完成） |
| 会话管理模式工具（plan_*） | 子 agent 工具集排除 plan 工具 | 模式工具不进入隔离上下文 |

## 3. 关键 API 迁移示例

```go
// 旧：一切在 Agent 上
cfg := agent.New("bot",
    agent.WithModel(model),
    agent.WithMemory(mem),          // 一个 Memory 三职责
    agent.WithSkillLoader(loader),  // 全量 skill
    agent.WithTools(tools...),
    agent.WithApprover(approver),
)

// 新：配置 + 运行时依赖分离
cfg := agent.New("bot",
    agent.WithModel(model),
    agent.WithSystemPrompts("..."),
)
cfg.SubAgents = []agent.SubAgent{{Name: "reviewer", SystemPrompt: "...", Tools: []string{"ls", "read"}}}

deps := kernel.Deps{
    SessionStore:   sessionsqlite.NewMessageStore(db),  // 会话
    Compressor:     ms,
    MemoryProvider: memorysqlite.New(db),               // 知识（独立连接同库）
    SkillProvider:  skill.NewFSBridge(skillfs.New(dir)),// 意图匹配
    Tools:          tools,
    HumanApprover:  approver,                           // 审批链人工层
    Extractor:      ctxpkg.NewAsyncExtractor(ctxpkg.NewLLMExtractor(model, knowledge)),
}
rt := kernel.New(cfg, deps)
res, err := rt.Run(ctx, openagent.Session{ID: "s1", UserID: "u1"}, input)
```

## 4. 已删除的机制（历史包袱，勿寻）

| 删除项 | 被什么取代 |
|---|---|
| SemanticMD（LLM 直接读写 md 当长期记忆） | MemoryProvider + 知识闭环 |
| trimToContextWindow（发送前静默丢消息） | 超窗口明确报错 + MaxCompressedTokens 强制 |
| SelfApproving / CanSelfApprove（工具自报安全） | 平台 ToolClassifier 白名单 |
| SkillLoader 接口 / Agent.SkillLoader / WithSkillLoader | provider/skill.Provider + Deps.SkillProvider |
| 动态 subagent 内置工具 / SubAgentParams / NoSpawn / Builtins 注入机制 | Agent.SubAgents 配置化 + runChild |
| Memory.Search + SearchResult（旧检索） | MemoryProvider.Recall（向量+keyword） |
| AgentContext.Goal 死字段 | 冗余于 Messages |
| 旧 Summarizer（model/openai/summarizer.go） | summarizer.New(model) |
| 各包零散 helper（sortStrings/toMap/MemoryProviderOf/MetaStore） | 统一模型/死代码清理 |
| `LoadedSkills` prompt 分节（"## Loaded Skill"） | skill body 经 `load_skill` 工具结果进上下文（业界模式；分节是 turn-0 快照、恒空的死通道） |
| `isWithinWorkspace`（file 工具） | 边界归 sandbox 层——工具只做路径规范化（symlink 解析），目录边界由 bwrap 挂载实施 |
| `keyring.Set("")` 静默删除 | 拒绝空值（`Delete` 显式删除）——误传空串不再销毁密钥 |
| `chSendBlock`（与 `chSend` 逐字重复） | 合并进 `chSend`（ctx 可取消的阻塞发送，有界背压） |
| `execution.Execute`/`ToolDefs`/`context.Commit` 公开方法/接口项 | kernel 不消费的死方法（Execute/ToolDefs 连接口项一并移除） |
| acp plan 白名单副本（`readOnlyToolNames`） | 单源：`governance.NewToolClassifier().ReadOnlyNames` |
| 本地 skill 目录关键词打分 top-5 注入 | 目录全量注入（Discover）：**所有 skill 的完整 frontmatter** 每轮进 prompt（用户拍板保留 frontmatter，比业界的一行 name+description 更重），模型自判相关性 |

## 5. 新增能力（旧架构没有的）

- **知识闭环**：会话结束 → LLM 提取（精炼/分类/去重）→ topic upsert 存储 → 跨会话 recall 注入 prompt——自我进化
- **审批策略链**：规则（settings）→ 平台安全分类 → 审批记忆（Always 持久化）→ 人工（可改参放行）
- **会话 Checkpoint**：崩溃恢复点（checkpoint_msgs 计数 + 全量 Restore）
- **中立 Tool 参数模型**：Anthropic 等新 provider 直接消费同一模型
- **审计事件**（eventbus 6 类，Audit/Observability，非状态源）
- **执行 Job 化**：Start/Wait/Cancel + Retryable 重试 + 并发保序
- **skill 目录全量渐进式加载**（业界形态）：所有 skill 的完整 frontmatter 每轮全量进 prompt，body 按需 `load_skill`；续接语不影响目录可见性
- **eventbus 全量历史回放**：订阅 channel 容量 = max(256, 历史长度)，晚订阅者拿到完整历史（修方向颠倒）
- **SSRF 四层防御**（iac http_request）：URL 策略 + 公网 IP 校验 + DialContext 防 rebinding + 禁重定向
- **跨平台 onnxruntime**：`libraryPath()` 按 GOOS-GOARCH 精确选择，third_party 覆盖 linux-amd64/arm64、darwin-arm64、windows-amd64
- **旧库 schema 迁移**：knowledge 表缺 topic 列 → 自动 `ALTER TABLE ADD COLUMN`（不再依赖手动删表）
- **LLM guard 纠正性重试**：judge 非 JSON 输出重试一次 + reasoning-only 兼容（不再"散文回答全拦"）

## 6. 目录结构变化

```
旧：agent/ runner.go 根包 memory/ skill/ hooks/ 等
新：kernel/       执行引擎（原 runner.go 拆分）
   agent/        纯配置
   context/      Context Runtime + Provider 接口（AgentContext/RuntimeState/MemoryProvider/Extractor）
   session/      SessionStore/Compressor 接口 + Runtime + sqlite/file 实现
   execution/    工具执行（Job/流式/重试/内置工具）
   governance/   策略引擎（四层链/分类器/审批记忆）
   provider/     memory | skill | resource | openviking（能力 Provider）
   tool/ plan/ mcp/ process/ eventbus/ 等保持不变
```

## 7. 验证方式

- 每阶段 `go build ./...` + `go vet ./...` + `go test ./...` 全绿
- 端到端冒烟（本地模型）：配置化 subagent 委托 → 知识闭环（中文提取/更新/跨会话召回）→ session.Runtime 生命周期 → 后台异步提取
- 行为断言：stream_test（事件顺序）、plan_concurrency_test（并发工具集）、策略链单测
