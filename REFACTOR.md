# openagent-go Context Architecture 重构总结

> 日期：2026-08 | 范围：P0-P5 + 清理轮 + 语义检索 + Tool 单返回 + toolcall 规范化 + subagent 重构 + 后续修复轮（全量代码审查） | 状态：全包测试全绿

## 1. 背景与动因

重构前的 openagent-go 是功能完整的 Agent Runtime 雏形（8-node 主循环、三层 Memory、
Tool/Approval/Guard/Skill/SubAgent 齐全），但存在结构性问题和与业界不对齐的子系统：

**结构性问题**
- **Agent 是 God Object**：持有 Model/Tools/Memory/Prompt/Guards/Approver/Hooks/Observer
  九类依赖，一切能力都挂在配置对象上
- **runner.go 1700 行巨型函数**：`run()` 单函数 460 行，`runner` 结构只持 agent 指针，
  一切经 `r.agent.X` 间接访问；compaction 只在 turn 1 触发
- **Memory 一个接口扛三种生命周期**：会话存储（短期）、压缩摘要（中期）、知识检索
  （长期）混在一个 `Memory` 接口里
- **错误静默丢弃**：Discover / hooks / PromptBuilder / Compressed 的错误被 `_` 吞掉

**与业界不对齐（重构的三个升级正题）**
1. **审批机制**：布尔二元 `Approve() (bool, string)`；`CanSelfApprove` 由工具自报安全
   边界；`transfer_to_` 前缀豁免硬编码；Allow-Always 不持久化（BUGS.md P1）
2. **Tool 结果**：裸 `(string, error)`；超长结果靠 hooks/artifact 落盘 hack；错误字符串化
3. **自我进化**：完全没有——Memory 只有会话历史+摘要+检索，无"经验提取→存储→注入"闭环

## 2. 设计决策（用户拍板）

| 决策 | 内容 |
|---|---|
| 范围 | P0-P3 核心架构先行，P4/P5 随后补 Provider 体系/OpenViking |
| **Agent API 形态** | **Agent 变纯配置**，新增 `kernel.Runtime` 外部驱动，全部调用方重写 |
| **包结构** | **全面重排目录**；基础类型（Message/Tool/Model/StreamEvent）留根包（根包即 core 层——70 个文件 import 根包且根包仅依赖 tokenizer，天然 DAG 叶节点，新建 core/ 只会制造改名噪音） |
| 兼容层 | **不保留**（未发布项目，一切可改） |
| 审批 | 升级为**分层策略引擎**（规则→安全→记忆→人工，Decision 携带 ModifiedArgs） |
| Tool 结果 | **结构化 ToolResult**（Content/JSON/Metadata/Truncated/FileRef/Error），runtime 内建截断 |
| 自我进化 | **进本次范围**（提取→Store→Recall 注入闭环） |
| Event | 不做 Event Sourcing（Audit/Observability/Replay 辅助） |

## 3. 架构演进对照

```
旧：Agent(God Object) → runner → Memory / Tool / PromptBuilder / LLM
新：
    应用层 (rest/acp/cmd/cli)  ← 组装 cfg + deps，驱动 Runtime
        ↓
    kernel.Runtime (执行引擎，原 runner.go 按节点拆分)
        ├─ context.ContextRuntime   (AgentContext 组装 + 知识注入)
        ├─ execution.ExecutionRuntime (工具执行: Start/Wait/Cancel + 重试)
        ├─ session.SessionStore + Compressor + Runtime (会话/压缩/生命周期)
        ├─ provider/ (memory | skill | resource | openviking)
        ├─ governance.PolicyEngine  (四层审批)
        └─ eventbus.Logger          (审计事件)
        ↓
    根包 openagent (core 层): 基础类型 + ToolResult + token helper
```

依赖方向（无环）：`根包 ← session ← memory/sqlite,file`；
`根包 ← governance`；`根包 ← agent`；`根包+session ← context`；
`根包 ← provider/* ← execution`；`… ← kernel ← rest/acp/cmd`。

## 4. 阶段交付

### P0 — 结构化 ToolResult
- `Tool` 接口签名改 `Execute(ctx, args) *ToolResult`（单返回，错误进 `result.Error`——
  后续一轮再次收敛，去掉 `(result, error)` 双通道冗余），全仓 22+ 工具实现改签名
- `DefaultResultPolicy` 内建超长截断（token 阈值 → 落盘 → FileRef 指针），替代 hooks/artifact hack
- `Message.Result` 携带结构化结果；`RunHooks.OnToolEnd` 接收 `*ToolResult`（可脱敏/改写）
- 新包骨架 + 三接口定义（SessionStore/Compressor/MemoryProvider）

### P1 — Agent 纯配置化 + kernel.Runtime + 分层策略引擎（最大阶段）
- **agent/**：`Agent` 只剩配置（Model/Prompt/Guards/Skills/限额）；Team 用 `AgentBinder`
  机制替代 `PrepareForTeam`
- **kernel/**：`Runtime` + `Deps` 组装；8-node 循环按节点拆方法（run/prompt/modelcall/
  cancel/prepare/execute/subagent/as_tool）；`SetApprover`/`SetSystemPrompts` 支持
  acp exit_plan_mode 的运行时可变能力
- **execution/**：工具执行独立（streaming/阻塞/hooks/结果策略）+ 内置工具
  （load_skill/reload_skills/recall）
- **governance/**：`PolicyEngine` 四层链（Rules→Safety→Memory→Human）+
  `SessionApprovalMemory`（修复 Allow-Always 不持久化）；`transfer_to_*` 豁免入默认规则；
  SelfApproving 遗留（引擎门/分类器/CanSelfApprove 方法）在后续清理轮全删——
  平台白名单（ToolClassifier）是安全分类的唯一来源
- 全仓应用层改写（rest/acp/cli/tui/iac/examples/orchestrate/wasm）

### P2 — 拆 Memory + 知识闭环
- `Memory` 一拆三：SessionStore（会话）/ Compressor（压缩，ThroughIndex 契约保留）/
  MemoryProvider（知识）；sqlite/file 零 schema 迁移实现三接口（新增 knowledge 表/文件）
- 接口定义在使用方（`context.MemoryProvider`/`MemoryItem`，打破循环依赖）
- `rest/team_memory.go` 重写为 scopedMemory 分区包装
- **知识闭环**：`context.Extractor`（规则版偏好/事实提取）→ Store → Build Recall →
  prompt `## Recalled Knowledge` 分节；链路单测通过

### P3 — Execution Job 化 + 收尾
- `Start/Wait/Cancel` Job 模型（并行执行、按调用序保序）；panic recover 移 job 内
- `ToolError.Retryable` 自动重试（指数退避）
- 修复清单落地：PromptBuilder/Discover/hooks 错误不再静默；semanticMD 写副作用迁移
- DESIGN.md 重写为目标架构文档

### 清理轮 — 移除旧版 workaround（用户驱动的两轮审查）
- **semanticMD 机制全家删除**：LLM 直接读写 md 文件当"长期记忆"是绕过存储层的 hack，
  被 MemoryProvider + 知识闭环取代（WithSemanticMD/SemanticMDPath/prompt 分节全删）
- **trimToContextWindow 删除**：发送前静默丢消息是"搬运的妥协"（被丢消息还在 store，
  下轮重读再丢，摘要引用模型没见过的历史）→ 改为**超窗口明确报错** + 补上从未被
  enforce 的 `MaxCompressedTokens`（摘要进入 prompt 前截断）
- **非阻塞 chSend 丢事件修复**：根因是 rest 用 `context.Background()` 派生 run ctx
  （客户端断开 run 不取消 → 被迫非阻塞防死锁）→ rest 改从 `r.Context()` 派生 →
  chSend 改阻塞发送（有界背压，事件不再丢）——同时修复了旧 code review 的
  系统性问题 #3（HTTP handler 不用请求上下文）
- 重复 helper 合并（kernel 自带 token 计数 → 根包统一导出）、死代码删除
  （hooks/artifact 包、sqlite messages.turn 死列、itoa/strconvQuote 等）

### P4 — Provider 体系 + 簿记 + 审计 + Session Runtime
- **provider/skill**：`Provider`（Match/Load）意图匹配替代全量注入，FSBridge 实现
- **provider/resource**：`Provider`（Search/Load）+ FS 目录服务（path 防护）
- **context.RuntimeState**：`{SessionID, Executions, Approvals}` 显式执行簿记，
  与 AgentContext（知识）分离
- **eventbus.Logger**：6 类审计事件（user.input/assistant.message/tool.call/
  tool.result/approval.request/approval.result）+ BusLogger（session 隔离+回放）
- **session.Runtime**：会话生命周期（Create/Save/Restore/Checkpoint/Delete），
  组合元数据 Store + 消息 SessionStore；Checkpoint 为崩溃恢复点

### P5 — OpenViking Provider（真实接入）
- **协议修正**：骨架假设 JSON-over-HTTP（/search /store）与 OpenViking 实际不符——它是
  **MCP over Streamable HTTP**（127.0.0.1:1933，工具：find/remember/read/list）
- **Client 用官方 Go SDK**（用户拍板）：`github.com/volcengine/OpenViking/sdk/go`（REST）——
  Search（FindResult 按 contextType 取 Memories/Resources/Skills）、Remember
  （CreateSession+BatchAddMessages+CommitSession）、Read。**弃用 MCP transport**：
  OpenViking 1.29.0 的 MCP remember 工具乐观返回但消息不落盘（REST 路径正常）
- **Provider 配置形态**（用户拍板）：`context_providers: {memory/skill/resource:
  "builtin"|"openviking"}` 选择器 + `openviking: {endpoint}`——无配置默认本地
  （sqlite/FS），OpenViking 可选
- **embedding 层级修正**（用户指出）：embedding 是编码服务非 Context Provider——
  `provider/embedding` → `embedder/openai`（与 bge 平级，settings `embedding` 段选择）
- 验证：连接/remember（消息入库）/find（检索通道通）✓；**remember→find 闭环受
  OpenViking 异步提取链路影响**（本地 Ollama 小模型 + 慢 embedding，memories 条目
  未及时产出——OpenViking 服务侧问题，非对接问题）
- openviking.Skill 补 Discover（skill.Provider 接口方法）

### 语义检索（embedder/bge + knowledge_vectors）
- **embedder/bge**：单二进制离线中文 embedder——WordPiece tokenizer（自实现，
  修了一个 `start -= 2` 死循环）、onnxruntime_go 动态库（dlopen + SetSharedLibraryPath）、
  94MB fp32 模型 go:embed、512 维 mean pooling、query/doc 前缀区分（BGE 惯例：
  query 加指令前缀 doc 不加——修完检索质量从 0.99/0.98 退化恢复到 0.85/0.31）
- **knowledge_vectors 表**：Store 时向量化；`knowledgeVectorRecall` 向量优先
  （cosine）→ LIKE fallback；复用 sqlite 现有 WithEmbedder 注入
- **知识 scope 修复**：知识是 user 级长期记忆（跨会话）——extract/recall 去掉
  SessionID，否则每开新会话都被自己的 scope 过滤
- **cmd/cli `--embedder` 默认 on**：内嵌零依赖能力不是 opt-in 实验功能，开关只用于显式关闭
- 端到端验证：英文偏好 → 中文查询 → 向量召回 → 模型回答 Terraform ✓
- 已知特性：bge-small-zh 对英文内容的跨语言检索弱（en-doc vs zh-query 相似度 0.39，
  中文同语言 0.84）——中文知识场景质量好，英文知识建议后续换多语言模型或双语存储

### Tool 单返回重构（用户驱动的 API 收敛）
- `Execute(ctx, args) *ToolResult`：去掉 `(result, error)` 双返回——错误双通道
  （result.Error vs err）是旧 API 形态残留，单通道后 hooks 签名简化
  （OnToolEnd 去 err 指针）、toolResultMessage 收敛、redact 错误脱敏作用域顺带修复
- 框架级问题（panic/工具不存在）由 runtime 处理，不进工具签名

### toolcall 规范化（中立 Parameters 模型，用户驱动的强类型化）
- **中立参数模型**（根包 schema.go）：`Parameters`/`Parameter` 即 JSON Schema 子集——
  JSON 形态可直接序列化为 OpenAI parameters / Anthropic input_schema / MCP inputSchema，
  provider 零转换（未来 Anthropic adapter 同模型直接输出）
- **SchemaOf[T]() 反射生成**：json tag 命名 + omitempty 推断 required +
  `jsonschema:"description=...,enum=..."` tag（与 invopop/jsonschema 兼容，可换库）
- **强类型闭环 ParseArgs[T]**：泛型解析 + required 校验（缺参报
  `missing required parameter "x"`）；定义与解析共用同一份 struct——schema 与解析
  永不漂移（修复了转换期退化：plan_create 的 steps 曾标 string[] 但 Execute 解析对象数组）
- 全仓 40+ 处手写 JSON Schema + 匿名 struct 解析清零：tool/plan/agent/iac-server/
  examples/wasm；MCP 进口 `ParametersFromMap`、model/openai 出口 `SchemaMap`
- 错误前缀与手工语义校验保留（validDeploymentID、len==0 等）；静默丢弃的 unmarshal
  （file.go 3 处、examples 3 处）改为正常报错返回

### skill 收尾（SkillLoader 删除，Provider 唯一化，用户驱动的双轨清理）
- P4 曾只"新增" provider/skill（意图匹配注入），旧轨原样保留：根包 `SkillLoader`
  接口、`agent.Agent.SkillLoader` 配置字段、run.go 全量 Discover 注入、load_skill
  旧后端、6 处应用层 WithSkillLoader——双轨并存（新轨仅条件覆盖）
- 收尾：`SkillLoader` 接口全删（根包只留 `SkillInfo` 数据模型）；`Provider` 补
  `Discover()`（load_skill 按名/reload_skills 重扫）；`Agent.SkillLoader` 字段与
  WithSkillLoader 删（能力统一走 `Deps.SkillProvider`，与 MemoryProvider 对齐）；
  run.go 全量注入删（context Build 意图匹配为唯一入口，budget 估算同步调整）；
  load_skill/reload_skills 改用 Provider；应用层 6 处（examples×2/cli/rest/
  iac-server）改构造 `skill.NewFSBridge(skillfs.New(dir))`

### memory 包按域拆分（用户驱动的包结构对齐）
- P2 拆接口后物理实现仍留在旧包名 memory/sqlite、memory/file（三合一：会话+压缩+知识，
  2/3 属于 session 域）；provider/memory 只有文档没有实现（空壳宣称 "SQLite/file live here"）
- 拆分：`session/sqlite`、`session/file` 新增 `MessageStore`（SessionStore+Compressor，
  同库同连接）；`provider/memory/sqlite`、`provider/memory/file` 新建（MemoryProvider，
  独立连接打开同一 .db / 同目录 knowledge.jsonl，WAL 多连接安全）
- 顺带清理：会话侧 vectors 表+向量索引是死代码（Search 删除后无读取方，embedder 仅知识侧需要）；
  file 的会话档案 Recall 分支死（全仓无 SessionID scope 构造）；DeleteKnowledge 死方法；
  重复的 cosineSimilarity/cosineSim 合并
- 引用点 6 处改造（cli/rest/iac-server/examples×3），`sessionsqlite.New(mem.DB())`
  共享连接模式保留（MessageStore.DB()）

### LLM extractor（知识提取升级，对齐 Mem0）
- 规则版（英文正则，中文不识别、原句存储、无更新语义）删除——被 LLM 版取代
- **LLMExtractor**（context/extractor_llm.go）：一次调用完成提取+分类（Mem0 两阶段合并）——
  提取候选（精炼自包含陈述）+ 对比已有知识输出 `{op: add|update|skip, kind, content, topic}`
- **存储 upsert**：`MemoryItem.Topic` 为键——同 (scope, topic) 覆盖（update），新 topic 插入（add），
  空 topic 追加；sqlite 加 topic 列（新 schema，不做旧库迁移）、file 读重写
- **AsyncExtractor 后台调度**（用户指出同步阻塞问题）：单 worker + 队列 + 按 user 合并去重
  （N 次 run → 至多一次提取，取最新消息）；`Extractor` 接口 fire-and-forget（去返回值）；
  应用层接线（cli/rest/acp 各构建一个共享实例，**绝不 per-run**）；kernel 不再默认构建
- 冒烟验证：teach 瞬时返回（不再等提取 LLM）、两次 teach 合并为 1 行（worker 合并 + upsert 更新）
- 冒烟验证：中文偏好提取 → 同主题更新（两次 teach 后 knowledge 1 行，防膨胀）→
  跨会话召回更新后的内容；修复：prompt 需含 user 角色（模型 API 400）、提取失败加 slog 日志（不静默）
- 与 OpenViking 衔接：extractor 只依赖 MemoryProvider 接口；topic 即 OpenViking 的分面键

### acp mode 语义对齐（用户定义 + "记住用户选择"闭环）
- **mode 语义矩阵**（用户定义）：plan = 只读工具+plan 工具+无需审批；
  manual = 全部工具审批+记住用户选择（Always 持久化）；auto = 全工具不审批（高风险显式选择）
- **"记住用户选择"断点修复**：acpApprover.always 写入 server 级 `s.approvalMemory`
  （持久化到 session meta），但 policy 链 Memory 层（Recall）读的是 per-runtime 的
  `rt.approvalMemory`（deps.ApprovalMemory 未传）——写入与读取不同实例，跨 turn Always 失效；
  agentForTurn 传 `deps.ApprovalMemory = s.approvalMemory` 闭环（同一持久化实例）
- **manual 全审批**（用户要求 read 也审批）：默认 policy 的 Safety 层（ToolClassifier）
  会把只读工具标 ReadOnly 自动放行——manual 模式改为**无 Safety 层的 engine**
  （所有工具进人工，含 read）；Always 记忆层仍前置短路；transfer_to_* 保留放行规则
- 核对：plan 只读无审批 ✓（上节修复）、manual 全工具+acpApprover ✓

### acp mode/plan 审查（用户指出的设计问题）
- **默认 mode auto → manual**：auto 是 "HIGH RISK, no approval"（buildModeState 自述），
  作为默认 = 默认进完全不审批的高风险模式；安全默认改 manual（审批模式），
  auto 显式选择。三处（OnNewSession/OnLoadSession fallback/buildModeState current）修正
- **plan mode 无本地只读工具**：原来只有 ACP read_file（客户端文件系统）——模型做计划
  无法读本地代码；补本地只读子集（read/ls/grep/websearch/webfetch，
  `readOnlyToolNames` 与平台 ToolClassifier 白名单一致）
- loadSessionState 一次 Get 返回 mode/prevMode/createdAt/config（会话状态恢复的统一入口）

### acp 会话创建/加载审查（持久化补齐）
- **config 不持久化**：会话级设置（model/thought_level）只存内存，重启丢失、加载回默认
  → `saveConfig` 写 `_meta["config"]`，OnLoadSession 恢复；`loadSessionState` 统一读
  mode/previousMode/createdAt/config
- **previousMode 不持久化**：plan mode 会话重启后 exit 回退目标丢失
  → `saveMode` 顺带存 `_meta["previous_mode"]`
- **createdAt 恢复用 now**：加载重建丢失原始创建时间 → 从 store 恢复
- `loadMode` 死函数删除（被 loadSessionState 取代）

### 端到端白盒分析（代码走查发现的修复）
- **P0: ModifiedArgs 不生效**——kernel/execute.go Phase 1 的
  `call.Function.Arguments = ModifiedArgs` 修改的是 range 值副本，Phase 2 用原始 calls——
  "人工改参后执行"链路失效；改为修改切片元素 `calls[i]`（rest edit 审批闭环打通）
- **rest handleChat 并发保护**——同一会话并发 chat 会并行跑两个 agent run 消息交错；
  加 `s.running` 检查返回 409
- 核对良好：executeTools（审批顺序+并发+保序）/modelcall（流式+退避）/
  job（panic 在 job 内）/restApprover（ctx 兜底+always+edit）/acpApprover
  （ACP 协议无 edit 选项——协议限制非遗漏）/agentForTurn（模型+模式工具）/commit 日志

### 第四轮清理（死代码/双实现收敛）
- `sortStrings` 死代码（bge tokenizer，"kept for potential" 无调用方）删
- Summarizer 双实现收敛：`model/openai/summarizer.go`（旧，独立 apiKey/modelID/baseURL 构造，
  仅 examples/memory 使用）删——统一 `summarizer.New(model)`（接受 Model 接口，cli 主路径）
- 遗留注释核对：legacy/former 均为历史说明（文档非包袱）；TODO/FIXME 清零；
  Session 字段（UserProfile/Temperature/MaxTokens/ProjectContext/DynamicContext）全部在用

### subagent 重构（配置化委托，用户驱动的业界对齐）
- **删除动态 subagent builtin**：模型在运行时自造 name/description/prompt 是"让模型
  设计 agent"——违反 Agent 纯配置化，不可治理不可审查（executeSubAgent/SubAgentParams/
  Builtins 注入机制/NoSpawn 全删）
- **Agent.SubAgents 配置化**：`SubAgent{Name, Description, SystemPrompt, Tools, Model,
  MaxTurns}`——身份由配置声明，模型只传 task；runtime 启动时注册为委托工具
- **双实现合并**：executeSubAgent 与 subAgentTool 统一为 `runChild`（隔离会话保留
  父 UserID scope 共享知识 + 流式转发）；子工具集在调用时从父快照解析
  （剥离其他 subagent + Tools 白名单）
- **治理继承**（用户拍板，Claude Code 模型）：子 agent 内部工具调用走继承的策略链
  （Rules→Safety→Memory→Human），审批记忆按子会话冷启动；**委托调用本身自动放行**
  （控制流无副作用——kernel policy() 为配置的 subagent 生成默认 Allow rule，
  应用可经 Deps.Policy 覆写；AsTool 程序化委托不自动放行，应用自理）
- 顺带修复：recall builtin 的 scope 从 SessionID 改为 UserID（与 extractor 对齐——
  否则 builtin recall 查不到 extractor 存的知识，知识闭环是断的）

### 后续修复轮（全量代码审查，2026-08-03）

重构交付后对全仓（76 包）逐文件真读审查（4 个并行审查 agent + 人工复核，全部发现
带 file:line 实证），修复按类别汇总如下，已合入重构提交。

**崩溃级（P0，5 条）**

| 位置 | 问题 | 修复 |
|---|---|---|
| orchestrate/executor.go | 并行批次 goroutine 无锁写 `state.Results` map → `fatal: concurrent map writes` 进程崩溃 | `executeStep` 改只读 + nil 防御；`executeBatches` 的预置保证每条目已存在 |
| skill/fs/loader.go | frontmatter 关闭符在 EOF 且无尾换行 → `text[4+idx+5:]` 切片越界 panic | `closeLen` 方案：EOF 分支按 4 字符分隔符切到 `len(text)` |
| cmd/mcp/iac-server/main.go | 日志文件在目录 `MkdirAll` 之前 `O_CREATE` 打开 → 全新机器首次运行必崩 | 打开日志前先建 iacHome 目录 |
| orchestrate/executor.go | AutoReplan 路径 `modelSyncCall(ctx, e.model, ...)` 无 nil 检查 → panic | `replanAfterFailure` 开头显式报错 |
| sandbox/native/native_linux.go | 流式 bwrap 失败回退缺 `Stdout==""` 条件 → 沙箱命令被执行两遍 | `sawReader` 跟踪输出，回退条件加 `!sawStdout` |

**skill 渐进式加载（对齐业界：Claude Code / OpenAI Agent Skills）**

- **目录全量注入**：`ContextRuntime.Build` 改 `SkillProvider.Discover()` 全量——**所有
  skill 的完整 frontmatter**（name/description 等全部键值，用户拍板保留 frontmatter
  渲染，比业界的一行 name+description 更重）每轮进 prompt；不再关键词打分 top-5——
  续接语（"继续"/"好的"）不再把目录从 prompt 抹掉；意图查询只用于知识/资源检索
- **删 `LoadedSkills` 死通道**：`AgentContext.LoadedSkills` 是 turn-0 快照、恒空；skill body 经
  `load_skill` 工具结果进上下文（业界模式），"## Loaded Skill" prompt 分节删除
- `Provider.Match` 保留（未来 skill 海量时用于检索选择），`tokenize` 补 CJK（与 knowledge 侧对齐）
- openviking `Load` 契约文档化（返回内容取决于服务端索引：全文或摘要）

**行为对齐（正确性/安全/语义）**

| 位置 | 改动 |
|---|---|
| eventbus/bus.go | 历史回放改**全量**（channel 容量 = max(256, 历史长度)），修"满缓冲丢最新"方向颠倒 |
| keyring/keyring.go | `Set("")` 拒绝（原静默删除已有密钥），显式删除走 `Delete` |
| schema.go | `ParseArgs` 空参 = 空对象，照常走必填校验（原跳过 → grep 空参全匹配） |
| guard/llm/guard.go | judge 非 JSON 输出 → 一次纠正性重试 + reasoning-only 模型兼容（原散文回答全拦） |
| orchestrate/executor.go | `isPermanentError`：DeadlineExceeded/net.Error 改瞬时可重试（原直接触发 replan） |
| acp/server.go | plan 白名单单源（引用 `governance.NewToolClassifier().ReadOnlyNames`）；StreamToolResult 失败判定用结构化 `Result.Error`（内容前缀降为兜底） |
| tool/shell.go | `ShellParams.Timeout` 可配置（默认 30s，钳制 [1,600]s），注释声称的 "configured timeout" 成真 |
| tool/grep.go | WalkDir 错误加日志；ctx 取消返回 `filepath.SkipAll` 立即停止遍历 |
| tool/file.go | 写入保留目标原 mode（新文件才 0644）；删 `isWithinWorkspace` 死代码（边界归 sandbox） |
| shell/summarizer/feishu | UTF-8 截断 4 处统一按 rune（原字节切片切裂中文，产生非法 UTF-8） |

**记忆链路**

- provider/memory/sqlite migrate：旧库缺 topic 列 → `ALTER TABLE ADD COLUMN`（原
  `CREATE TABLE IF NOT EXISTS` 不更新旧表 → 提取/召回全链路 `no such column: topic`）
- context/extractor_llm.go：Store 失败加 slog.Warn（原静默吞，提取失败不可见）
- cmd/cli/server/run.go：`run` 命令对齐 RunACP 全量接线（SessionStore/Compressor/
  MemoryProvider/Extractor/SkillProvider/summarizer；原仅 Tools）

**openviking 配置简化（方案 A）**

- `openviking.endpoint` 非空 = memory/skill/resource 三域全切（一个地址足够）；
  `context_providers` 降级为 `"builtin"` 逃生舱（显式覆盖某域回本地）
- 直接 HTTP 对接（4 端点，SDK 移除）；`account/user/apiKey` 死字段删除；`[user:]` 内容前缀 hack 删除

**跨平台 onnxruntime**

- `third_party/onnxruntime/` 补 linux-arm64 / darwin-arm64 / windows-amd64 库（与现有 1.28.0 同版本）
- `libraryPath()` 按 `runtime.GOOS-GOARCH` 精确选择（防跨平台误中架构不匹配的库）；
  Intel Mac（darwin-amd64）官方 1.28.0 无构建 → 走系统路径 / `OPENAGENT_ORT_LIB`

**其他清理**

- 死代码已删：`toolCallNames`/`chSendBlock`/mcp `CallTool`/`execution.Execute`/`ToolDefs`/
  `context.Runtime.Commit`/`LoadedSkills` 通道/`isWithinWorkspace` 等
  （iac `backupTFFiles`/`restoreTFFiles` 同样确认无调用，但暂未删除——见遗留项）
- iac http_request：SSRF 四层防御（ValidateRequestURL + ResolveAndCheck + DialContext 防
  rebinding + 禁重定向）；hostapi http_request：30s 超时 + 10MiB 响应上限（插件不可信代码）
- provider/memory/file 与 sqlite 行为对齐（scope_session 过滤、空 query 返回空、原子写、损坏行日志）
- rest 收敛：handleApprove/submitApproval 泛型共享；team 模型键统一 "/"
- 文档：ACP_CALL_CHAIN.md（ACP 模式数据流级调用链路）

## 5. 过程中的教训（决策记录）

1. **"升级型重构"不是"迁移型重构"**：第一版计划被否（"现状不一定是正确的"）。
   实施中仍滑向搬运优先（semanticMD、trimToContextWindow 原样搬入新架构），
   被用户两轮点名后系统性清理。原则：**旧机制被新机制取代就删，重复就并，无主就归位**；
   保留必须有设计理由，不是惯性。
2. **"无投机抽象" ≠ 自造轮子，但"中立模型" ≠ "依赖标准库"**：toolcall 参数规范化
   第一版自创 `desc:/req:/enum:` tag 方言被否（"定制"）；最终方案是**中立 Parameters
   模型**——`SchemaOf[T]()`/`ParseArgs[T]` 自包含实现，tag 格式与 invopop/jsonschema
   兼容（可换库），不引入运行时依赖。要点：自定义方言是问题，**自包含中立模型**是方案——
   模型中立才能 provider 中立。
3. **接口定义放在使用方**（Go 惯例）：MemoryProvider 接口放 context 包、Provider 实现放
   provider/，既避免 import cycle 又符合惯例。
4. **修复链要找根因**：非阻塞丢事件的根因是 run ctx 不随请求取消，而不是发送方本身；
   修根因后 workaround 自然消失。
5. **旧 API 形态不等于正确**：`(result, error)` 双返回是 OpenAI SDK 风格残留，
   结构化 ToolResult 后是冗余双通道——用户指出后收敛为单返回。
6. **"适配旧的"不是重构**：embedding 接入时往 `Capabilities` 加第 7 个开关被用户否掉
   （"从一坨屎变成另一坨屎"）——内嵌零依赖能力默认开启，Capabilities 机制整体
   待重构为 settings.json 配置驱动。

## 6. 测试与验证状态

- `go build ./...` + `go vet ./...` 干净
- **25 包测试全绿**，含新增：
  - result_test.go（截断策略落盘/FileRef/Truncated）
  - context/knowledge_test.go（知识闭环链路 extract→store→recall→注入）
  - execution/job_test.go（Job 保序/Cancel/重试）
  - provider/skill/fs_test.go（意图匹配排序）
  - eventbus/logger_test.go（审计事件发布/隔离）
  - session/runtime_test.go（会话生命周期/Checkpoint）
  - kernel/runtime_test.go（stream_test 迁移）、agent/team_test.go、acp/plan_concurrency_test.go
    （并发 AppendTools 契约保留）
  - embedder/bge/bge_test.go（中文语义相似度：同义 0.85 vs 无关 0.31）
  - memory/sqlite/vector_test.go（向量召回排序 + 跨语言诊断）

## 7. 遗留项

| 项 | 说明 |
|---|---|
| 审批 UI 交互 | Always/Edit-args 的 ACP/REST 前端响应（引擎层 `Decision.ModifiedArgs` + `ApprovalMemory` 已就位） |
| 配置化 subagent 端到端 | ✅ 已实测（模型→委托→子 agent 独立系统提示+工具白名单→结果回填，本地模型冒烟） |
| OpenViking 协议对接 | 按实际部署 API 调整 provider/openviking 的请求/响应字段 |
| session.Runtime 接入完成 | rest/acp 会话生命周期统一走 session.Runtime：Runtime 接口补 Get/List（元数据读取）；应用层删 session.Store 字段（rest sessionManager、acp AgentServer 只持 Runtime）；acp 会话关闭走 Runtime.Delete 联动删 meta+messages；approvalMemory 直接接受 session.Runtime；Checkpoint 在 run 完成后调用。消息列表端点保留直接访问（需完整 Message）。**Restore 修正**：去掉 500 条硬编码上限——按 checkpoint_msgs 计数全量恢复 checkpoint 之后的消息（Checkpoint/Restore 语义闭环，checkpoint 计数从只写不读变完整使用） |
| eventbus 展示流有损背压 | 订阅分发非阻塞（慢消费者丢事件）——设计取舍（生产者不阻塞）：缓冲 256 降低丢率，丢的只是 SSE 展示事件；审计走独立 durable Logger（接口已就位，长期审计 sink 待接） |
| ApprovalMemory 落库 | 当前进程内会话级，可持久化到 SessionStore 元数据 |
| LLM extractor | ✅ 已完成（见下节） |
| rest/acp 接入 session.Runtime | 当前应用层仍各自管理会话，可逐步切换到统一生命周期 API |
| **cmd/cli Capabilities 机制重构** | 7 个能力开关的字符串 flag + `*bool` 是旧设计，重构为 settings.json 配置驱动 + Provider 装配收敛（已记入项目记忆） |
