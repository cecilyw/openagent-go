# ACP 模式代码调用链路(数据流级)

> 从启动 ACP 服务、客户端连接、会话创建,到连续对话的每一步:每个函数接收什么、输出什么、输出又被谁消费。
> 按主路径与分支路径组织,标注关键代码位置。

## 目录

1. [启动 ACP 服务](#1-启动-acp-服务)
2. [客户端连接(OnInitialize)](#2-客户端连接oninitialize)
3. [会话创建(OnNewSession)](#3-会话创建onnewsession)
4. [一次 prompt 请求主路径](#4-一次-prompt-请求主路径sessionprompt)
5. [kernel RunStream 8 节点主循环](#5-kernel-runstream-8-节点主循环)
6. [节点④ 模型调用层展开](#6-节点模型调用层展开)
7. [节点① prepareMemory 压缩展开](#7-节点-preparememory-压缩展开)
8. [节点⑥⑦ executeTools 与 job 生命周期展开](#8-节点-executetools-与-job-生命周期展开)
9. [节点 commit:消息落库展开](#9-节点-commit消息落库展开)
10. [分支:审批 RPC 往返(manual 模式)](#10-分支审批-rpc-往返manual-模式)
11. [分支:buildDynamicContext(plan/模式状态注入)](#11-分支builddynamiccontextplan模式状态注入)
12. [分支:模式切换 setSessionMode](#12-分支模式切换-setsessionmode)
13. [分支:plan 工具回调](#13-分支plan-工具回调)
14. [分支:取消 / 关闭 / 删除 / 加载](#14-分支取消--关闭--删除--加载)
15. [分支:OpenViking provider 数据流(切换后)](#15-分支openviking-provider-数据流切换后)

---

## 1. 启动 ACP 服务

入口:[cmd/cli/server/acp.go](cmd/cli/server/acp.go) `RunACP(ctx, cfg, caps) error`

```
1. buildMemory(profilesDir, embedding, embedderOn) → (ms, knowledge, sessionStore, cleanup, err)
     ms        = session/sqlite 消息库(会话)
     knowledge = provider/memory/sqlite 知识库(独立连接,同 .db)
     sessionStore = session meta 库
2. buildModels(cfg.Provider) → (models []Model, modelInfos []modelReg)
     modelInfos = {ID, Provider, Model, APIKey, BaseURL}
     modelMap["provider/modelID"] = Model
3. agent.New("openagent",
     WithSystemPrompts(resolveProfiles(profilesDir, "")...),   ← SOUL/SYSTEM/AGENTS
     WithMaxTurns(100))
4. buildRuntimeDeps(caps, sensitive) → kernel.Deps
     SkillProvider = skill.NewFSBridge(...)(或 openviking,见 §15)
     MemoryProvider = knowledge; SessionStore = ms; Compressor = ms
     Extractor = NewAsyncExtractor(NewLLMExtractor(firstM, knowledge))   ← 后台提取,绝不 per-run
     ms.WithSummarizer(summarizer.New(firstM))
5. wasm 插件 Discover(agent:tools / agent:observers),observer 并入 deps.Observer
6. applyContextProviders(cfg, &deps)                              ← openviking 替换点
7. acp.NewAgentServer(agentCfg, deps, sessionStore, modelMap) → *AgentServer
     AgentName/AgentVersion/MCPEnabled/DefaultMode/PluginMgr/ProfileResolver
     RegisterModel(key, provider, modelID, apiKey, baseURL)       ← 存 modelConfigs(runtime_set_model_config 用)
     ToolFactory = func(cwd) → native sandbox + buildTools(...)   ← 每会话 cwd 懒建
8. openacpsdk.NewServer(name, version, srv) → *Server
9. server.Run(ctx) → RunTransport(ctx, os.Stdout, os.Stdin)
```

存了啥:无(仅进程级状态:models/modelConfigs/approvalMemory/runtime)。

## 2. 客户端连接(OnInitialize)

入口:[acp/sdk/server.go](acp/sdk/server.go) `serve` → `route` → [acp/server.go:633](acp/server.go#L633) `OnInitialize`

```
serve(ctx, r):
  scanner 逐行读 stdin(JSON-RPC 2.0,10MB 行上限)→ lines chan(缓冲 8)
  msg.Method != "" → route(msg)
  msg.ID 非空且无 Method → deliverResponse(msg)        ← Agent→Client RPC 的响应

route: "initialize" → handleInit → dispatch → OnInitialize(ctx, req InitializeRequest)
  → (*InitializeResponse, error)

OnInitialize:
  存: s.clientCaps = req.ClientCapabilities             ← 决定注册哪些 Agent→Client RPC 工具
  返回: {ProtocolVersion: 1,
        AgentCapabilities{LoadSession, Prompt{Image, EmbeddedContext},
                          Mcp{HTTP, SSE}, Session{Close/Delete/List/Resume}, Auth{Logout}},
        AgentInfo{Name, Version}}
```

存了啥:`clientCaps`(内存)。后续 `clientCanReadFile/clientCanWriteFile/clientCanTerminal` 按它给 LLM 注册工具(能力不足的工具不注册,避免 LLM 拿到会被客户端 -32601 拒绝的工具)。

## 3. 会话创建(OnNewSession)

入口:[acp/server.go:689](acp/server.go#L689) `OnNewSession(ctx, req NewSessionRequest) → (*NewSessionResponse, error)`

```
1. id := s.newSessionID()                                ← "acp_<nano>_<seq>"
2. mcpSessions, mcpTools := s.connectMCP(ctx, req.McpServers)   ← MCP 客户端连接
3. ss := &agentSession{
     id, cwd(规范化), createdAt: now,
     mode: s.defaultMode(),                              ← 默认 manual
     config: {"thought_level": "medium", "model": defaultModelID},
     firstPrompt: true,
     additionalDirectories, mcpServers, mcpSessions, mcpTools}
4. process.NewManager(/tmp/openagent/sess-<id>)          ← 会话级 shell 长进程
5. ss.rt = s.buildRuntimeForSession(id, ss)              ← 核心,每会话一次(见下)
6. putSession(id, ss)                                    ← 内存 map
7. saveMeta(ctx, id, cwd, "acp", req.Meta)               ← Runtime.Save → _meta{kind, cwd}
8. updateSender.SendSessionUpdate(available_commands_update)
9. 返回 {SessionID, ConfigOptions(mode/thought_level/model), Modes}
```

### buildRuntimeForSession 内部([server.go:1512](acp/server.go#L1512))

```
输入: sid, ss *agentSession;输出: *kernel.Runtime(会话级,跨轮复用)

cfg := s.Cfg.Clone()                                     ← 每会话独立配置副本
deps := s.Deps
deps.Tools = nil                                         ← 工具按模式注入
deps.HumanApprover = nil
deps.SubAgentExcludeTools = [plan_create, plan_update, enter/exit_plan_mode]  ← 子 agent 不继承模式工具
deps.ApprovalMemory = s.approvalMemory                   ← server 级持久化审批记忆(跨轮/跨重启)

modelID := ss.config["model"] → s.Models[modelID] → cfg.Model
ss.config["thought_level"] → cfg.ReasoningEffort
ProfileResolver(cwd) → cfg.SystemPrompts(项目目录 SOUL/SYSTEM/AGENTS)

rt := kernel.New(cfg, deps)
  → rt.tools = deps.Tools + 每个 SubAgent 的委托工具(newSubAgentTool)
  → rt.context = ctxpkg.NewContextRuntime({SessionStore, Compressor, MemoryProvider,
                                           SkillProvider, ResourceProvider, Observer})
  → rt.execution = execution.New({ToolSnapshot: rt.SnapshotTools, ...,
                                  ResultPolicy: 默认 DefaultResultPolicy})

cfg.SubAgents 非空 → 缓存 subAgentTools(plan 模式移除后恢复用)
applyModeTools(sid, ss, rt)                              ← 初始模式工具集(见下)
```

### applyModeTools 内部([server.go:1580](acp/server.go#L1580))

```
drop := toolNames(ss.modeTools) + subAgentToolNames(cfg)
rt.RemoveTools(drop...)
switch ss.Mode():
  "plan":   只读白名单(read/ls/grep/websearch/webfetch)+ read_client_file
            rt.SetHumanApprover(nil)                     ← 无副作用,无需审批
  default:  read_client_file + executionTools(sid, ss) + subAgentTools
            executionTools = MCP 工具 + ToolFactory(cwd) + 客户端 RPC 工具(按 clientCaps)
            "manual" → rt.SetHumanApprover(acpApprover{client, sid, memory: approvalMemory})
            "auto"   → rt.SetHumanApprover(nil)
ss.modeTools = add; rt.AppendTools(add...)
```

存了啥:`_meta{kind, cwd}`;内存:`sessions[id] = ss`、`ss.rt`、`ss.modeTools`、`subAgentTools`。

## 4. 一次 prompt 请求主路径(session/prompt)

### sdk 层([sdk/server.go:475](acp/sdk/server.go#L475) handlePrompt)

```
route: "session/prompt" 且 isReq →
  promptWG.Add(1) → go handlePrompt(msg)                 ← 独立 goroutine(防 Agent→Client RPC 死锁)
    json.Unmarshal(msg.Params, &req PromptRequest)
    mu := sessionLocks.LoadOrStore(req.SessionID) → Lock  ← 同会话 prompt 串行
    ctx, cancel := context.WithCancel(Background)
    cancelPending[idString(msg.ID)] = cancel             ← $/cancel_request 用
    sender := &promptSender{m, req.SessionID}
    resp, err := handler.OnPrompt(ctx, req, sender)
    wasCancelled := ctx.Err() != nil; cancel()
    delete(cancelPending, id)
    err != nil: wasCancelled → writeResult(PromptResponse{StopReason: Cancelled})
                否则 → writeError(Internal, err)
    成功 → writeResult(msg.ID, resp)
```

### AgentServer.OnPrompt([server.go:1212](acp/server.go#L1212))

```
OnPrompt(ctx, req PromptRequest, sender SessionEventSender) → (*PromptResponse, error)

├─ ss := getSession(req.SessionID)                        ← nil → 报错返回
├─ input, err := contentBlocksToMessage(req.Prompt)
│    输入 []ContentBlock{Type: text|image|resource|resource_link}
│    按 Type 分发 → textParts[] / contentParts[](image_url)
│    输出 Message{Role: user, Content: strings.Join(textParts, "\n"), ContentParts}
│    校验: Content=="" 且非多模态 → "empty prompt"
├─ ss.ResetPlanToolsInjected()                            ← 每轮重置 enter_plan_mode 注入门
├─ ctx, cancel := WithCancel(ctx); ss.cancel = cancel
│  defer: ss.cancel = nil; cancel()
│
├─【分支 A】slash: resp, handled := cmdRegistry.Handle(buildSlashContext(...), input.Content)
│   handled → sender.SendAgentMessage(resp) → return {StopReason: EndTurn}
│
├─【分支 B】首轮 auto-title:
│   title := firstLine(input.Content, 80)
│   updateTitle → Runtime.Get → Title=... → Runtime.Save → SendSessionUpdate(session_info_update)
│   sender.SendSessionInfo(title, nil)
│   sender.SendAvailableCommands(availableCommands())
│
├─ agent := ss.rt;nil → buildRuntimeForSession;ss.rt = agent
├─ providerID, modelID := resolveModelConfig(ss)          ← config["model"] → modelConfigs 拆
├─ oaSession := Session{
│     ID, ModelID, Provider,
│     Model: agent.Model(),                               ← 运行时解析的模型实例
│     CreatedAt: ss.createdAt,
│     Metadata: {cwd, additionalDirectories, mcpServers},
│     DynamicContext: buildDynamicContext(ss)}            ← §11
├─【分支 C】wasmhost.WithAgentRuntime / process.WithManager 注入 ctx
├─ reconcilePlanTools(ctx, sid, ss, agent, sender)        ← §13(每轮 rebind plan 工具)
│
├─ ch := agent.RunStream(ctx, oaSession, input)           ← §5,输出 <-chan StreamEvent
│
├─ for evt := range ch:                                   ← 事件消费与转发
│   StreamThought      → sender.SendAgentThought(evt.Text)
│   StreamTextDelta    → sender.SendAgentMessage(evt.Text)
│   StreamToolCall     → 每个 tc: SendToolCall({ToolCallID, Status: "pending", RawInput})
│   StreamToolProgress → SendToolCall({Status: "in_progress", RawOutput: {chunk}})
│   StreamToolResult   → 内容前缀 "error: " → "failed",否则 "completed"
│                        SendToolCall({ToolCallID, Status, RawOutput: {result}})
│   StreamRetrying     → SendAgentThought("[retrying: ...]")
│   StreamDone         → usage = evt.Result.Usage; stopReason = finishReasonToACP(...)
│   StreamError        → return nil, evt.Error
│   StreamAborted      → return {StopReason: Cancelled, Meta: {mode}}
│
├─ Runtime.Checkpoint(cpCtx, sid)                         ← 写 _meta.checkpoint_msgs
├─ ss.totalTokens += usage.TotalTokens
├─ usage.PromptTokens > 0 → SendUsageUpdate(usage.PromptTokens, cw, nil)
├─ ctx.Err() != nil → return {Cancelled}
└─ return {StopReason, Meta: {mode}}
```

### sender 输出去向(promptSender → writerLoop)

```
promptSender.send(SessionUpdate):
  notif := jsonrpcMessage{Method: "session/update", Params}
  mux.enqueue(notif) → writeQueue.push(帧)               ← 无界 FIFO,永不阻塞
  → writerLoop()(唯一写 stdout 的 goroutine)→ os.Stdout.Write(帧 + "\n")
  写失败 → queue.close + cancelWrite(serve ctx 取消)+ drainAndDrop
```

## 5. kernel RunStream 8 节点主循环

入口:[kernel/run.go:26](kernel/run.go#L26) `run(ctx, session, prefix, input, ch) → (*RunResult, error)`

```
RunStream → RunStreamWithPrefix → ch(缓冲 16)→ go run(...)

run:
├─ maxTurns = cfg.MaxTurns(默认 20)
├─ rt.runModel = cfg.Model;session.Model != nil → runModel = session.Model
├─ SkillProvider != nil → builtinTools = [load_skill, reload_skills]
├─ MemoryProvider != nil → builtinTools += [recall]
├─ commit(session, input) → SessionStore.Append          ← 用户消息落库
├─ logEvent(user.input)
├─ Hooks.OnAgentStart → agentHookState
│
├─ for turn := 0; turn < maxTurns; turn++:
│  ├─ ctx.Err() → cancelCompensation() → return nil, ctx.Err()
│  │
│  ├─【turn 0 只一次】:
│  │  ① messages, ci, err := prepareMemory(ctx, session)   ← §7
│  │     workingMessages += messages
│  │     workingMessages = ExcludeInput(去 input)+ TrimOrphanToolCalls
│  │     rt.compressed = ci.compressed
│  │  ③ guardInput(input) → !ok → return nil
│  │  ② workingMessages += prefix + input
│  │  ① ac, err := context.Build({Session, Goal: input.Content, WorkingSet})
│  │        → AgentContext{Messages, Memories(Recall,5), Skills(Discover 全量), Resources(Search,5)}
│  │
│  ├─ ac.Messages = workingMessages                       ← 每轮同步
│  ├─ ② prompt, err := buildPrompt(ctx, session, ac) → []Message
│  │     静态(system prompts+project context)+ 动态(OS/日期/skills/knowledge/resources/summary)
│  │     token > ContextWindow → 硬检查报错
│  │     PromptBuilder(BuildPrompt 或 cfg.Prompt)→ messages
│  ├─ req := buildModelRequest(session, prompt)
│  │     {Model: session.ModelID, Messages, Tools: snapshot+builtin, ReasoningEffort}
│  │
│  ├─ ④ resp, err := callModel(ctx, req, ch)              ← §6
│  ├─ choice := resp.Choices[0].Message
│  ├─ ⑤ guardOutput(choice) → blocked → choice.Content = "[blocked: ...]"
│  ├─ result.FinalOutput = choice.Content
│  ├─ StreamToolCall 事件(每个 tc)
│  ├─ result.Messages += choice;workingMessages += choice
│  ├─ commit(choice) → 落库
│  │
│  ├─ len(choice.ToolCalls) == 0 → break
│  │
│  ├─ ⑥⑦ results := executeTools(ctx, session, choice.ToolCalls, ch)   ← §8
│  ├─ 对每个 result: guardOutput → Messages/workingMessages/commit/StreamToolResult
│  ├─ checkHandoff(calls, results) → EndTurn → StopReason=handoff, break
│
├─ Extractor.Extract(scope{UserID}, workingMessages)      ← fire-and-forget
├─ chSend(StreamDone{Result})
└─ return result, nil
```

## 6. 节点④ 模型调用层展开

### callModel([kernel/modelcall.go:14](kernel/modelcall.go#L14))

```
for attempt := 0; attempt <= 3; attempt++:
  attempt > 0:
    backoff = 1<<(attempt-1) 秒;RetryableError.RetryAfter > 0 → RetryAfter
    chSend(StreamRetrying)
    select time.After(backoff) / ctx.Done
  resp, err := callModelOnce(ctx, req, ch)
  err == nil → return resp
  !errors.As(err, *RetryableError) → return nil, err       ← 非瞬时不重试
return "max retries exceeded"
```

### callModelOnce([modelcall.go:47](kernel/modelcall.go#L47))

```
reader, err := runModel.ChatCompletionStream(ctx, req)
  err → return
  reader != nil → accumulateStream(ctx, reader, ch)
  reader == nil(不支持流式)→ ChatCompletion(ctx, req)
     → chSendBlock(StreamThought/StreamTextDelta 整段)
```

### openai 侧([model/openai/openai.go:105](model/openai/openai.go#L105))

```
params := ChatCompletionNewParams{
  Model/Messages/Tools/Temperature/MaxTokens/TopP/Stop,
  ReasoningEffort(非空才传),
  StreamOptions{IncludeUsage: true},                       ← 流式带 usage
}
stream := client.Chat.Completions.NewStreaming(ctx, params)
Err() → toRetryableError(429 + 5xx + Retry-After 解析)
```

### streamReader / toStreamChunk([openai.go:325](model/openai/openai.go#L325))

```
Next(): SDK SSE 解析 → toStreamChunk(Current) → StreamChunk
Err(): 429/503 → RetryableError(流中断可重试)

toStreamChunk:
  Usage.TotalTokens > 0 → sc.Usage
  for choice:
    sd := StreamDelta{
      Content: choice.Delta.Content,
      ReasoningContent: extractReasoning(choice.Delta.RawJSON()),   ← SDK 无类型化字段,从 RawJSON 抠
      FinishReason,
      ToolCalls: [{Index, ID, Type, Function{Name, Arguments}}]     ← 增量片段
    }
```

### accumulateStream([modelcall.go:76](kernel/modelcall.go#L76))

```
本地: content/reasoning/finishReason;usage;toolAcc map[int]*ToolCall
for reader.Next():
  chunk.Usage != nil → usage = *chunk.Usage
  for delta in chunk.Choices:
    content += delta.Content;reasoning += delta.ReasoningContent
    FinishReason 非空 → 记录
    ch != nil: ReasoningContent → StreamThought;Content → StreamTextDelta   ← 实时事件
    for tcd in delta.ToolCalls:
      tc := toolAcc[tcd.Index];nil → 建
      ID/Type/Name 非空 → 覆盖;Arguments += 拼接                          ← 按 index 聚合
reader.Err() → return nil, err
toolCalls := 按 index 0..n 顺序收集
输出 ChatCompletionResponse{Choices: [{Message{Role: assistant, Content, ReasoningContent,
                                                ToolCalls}, FinishReason}], Usage}
```

输出去向:`choice := resp.Choices[0].Message` → guard.out → commit → executeTools。

## 7. 节点 prepareMemory 压缩展开

入口:[kernel/prepare.go:37](kernel/prepare.go#L37)

```
budget := workingTokenBudget()
  MaxWorkingTokens > 0 → 它;否则 ContextWindow × 70%;兜底 20000
budget -= estimatePromptOverhead(modelID)                  ← 静态+动态上下文+摘要; < 500 → 500

totalCount := SessionStore.Count(session.ID)               ← sqlite COUNT(*)
totalCount == 0 → return nil
msgs := SessionStore.Recent(session.ID, min(totalCount, 5000), 0)   ← 最近,旧→新
globalOffset := totalCount - len(msgs)

【压缩判定】从最新往最旧累加 token:
  for i := len(msgs)-1; i >= 0; i--:
    tokens += CountMessageTokens(msgs[i])
    tokens > budget → overflow = i+1; break
overflow < len(msgs):
  overflow = SafeCompressionBoundary(msgs, overflow)
      ← 最后被压的是 assistant+tool_calls → 把其 RoleTool 结果一起纳入压缩(摘要完整)
  oldTI := Compressor.Compressed(session.ID).ThroughIndex
  ci.err = Compressor.Compact(session.ID, globalOffset+overflow, msgs)
  Compressed 再读 → ci.compressed;ThroughIndex > oldTI → ci.count/from/to(观测)

【工作集】overflow >= len(msgs) → 全保留;否则 return msgs[overflow:]
```

输出去向:工作集 → ExcludeInput → TrimOrphanToolCalls → Build → prompt;摘要进 `rt.compressed` → prompt "## Conversation Summary"。

## 8. 节点 executeTools 与 job 生命周期展开

### Phase 1 审批([kernel/execute.go:27](kernel/execute.go#L27))

```
policy := rt.policy()                                     ← 默认引擎(Rules→Safety→Memory→Human)
for i, call := range calls:
  rc := execution.Resolve(name);nil → "tool not found" 结果
  decision, err := policy.Evaluate(ctx, call, rc.Def, session)
    err → "policy error"
    Deny → "this call rejected, reason: ..."
    ModifiedArgs != nil → calls[i].Arguments = string(ModifiedArgs)   ← 改参后执行
  approved[i] = true
```

### Phase 2 job 执行([handle.go:43](execution/handle.go#L43))

```
handles[i] = execution.Start(ctx, session, call, ch) → ExecutionHandle
  startJob:
    jobCtx, cancel := WithCancel(ctx)
    go func():
      defer close(done);defer recover → jobPanic(panic 吞在 job 内)
      j.output = e.execute(jobCtx, session, call, ch)     ← 内含重试(≤2,Retryable)
      j.err = j.output.Result.AsError()

for i, h := range handles:
  h.Wait(ctx): <-done → err;<-ctx.Done → ctx.Err
    取消 → Cancel 剩余 handles
  results[i] = h.Output()
```

### executeOnce 单次管道([execution/runtime.go:158](execution/runtime.go#L158))

```
rc := Resolve(name) → nil → "tool not found"
args := json.RawMessage(call.Function.Arguments)
toolCtx := withSession(ctx, session)
【内置】rc.Builtin → fireToolHooks(OnToolStart) → handler → fireToolHooksEnd(OnToolEnd) → toolResultMessage
【流式】Tool 实现 StreamExecutor 且 ch != nil:
  toolCh := se.ExecuteStream(toolCtx, args)
  select: chunk ← toolCh → buf/pending;ticker 1s → flush → StreamToolProgress(节流);
          toolCtx.Done → flush, done
  result = ToolResult{Content: buf}
【阻塞(默认)】result = rc.Tool.Execute(toolCtx, args) → *ToolResult
OnToolEnd(toolCtx, def, args, result, hookState)          ← hooks 可改写 result(redact 等)
ResultPolicy != nil → result = Apply(...)                 ← 超窗截断落盘 + FileRef
return toolResultMessage(call, result)                    ← Message{RoleTool, ToolCallID, Content, Result}
```

## 9. 节点 commit:消息落库展开

入口:[kernel/run.go:339](kernel/run.go#L339)

```
commit(ctx, session, msg):
  msg.Transient || SessionStore == nil → 跳过(handoff 内部消息不入库)
  SessionStore.Append(ctx, session.ID, msg)
```

sqlite Append([session/sqlite/message_store.go:107](session/sqlite/message_store.go#L107)):

```
toolCallsJSON, _ := json.Marshal(msg.ToolCalls)           ← 结构化字段序列化进 TEXT 列
contentPartsJSON, _ := json.Marshal(msg.ContentParts)
BeginTx:
  INSERT INTO messages (session_id, role, name, content, content_parts,
                        tool_calls, tool_call_id, reasoning_content)
  id := LastInsertId
  msg.Content != "" → INSERT INTO messages_fts (rowid, content)    ← FTS5 trigram 索引
  Commit
```

输出去向:messages 表 → 下次 prepareMemory Recent 读回;FTS5 → recall 工具检索。

## 10. 分支:审批 RPC 往返(manual 模式)

入口:[acp/server.go:1980](acp/server.go#L1980) `acpApprover.Ask`

```
Ask(ctx, call, def, session) → (Decision, error):
  a.client == nil → Deny("no approval client configured")           ← fail-closed
  resp, err := a.client.RequestPermission(ctx, RequestPermissionRequest{
    SessionID: a.sessionID,
    ToolCall: ToolCallUpdate{ToolCallID: call.ID,
                             Title: toolTitle(name, args),          ← 从 args 抠 path/command 做标题
                             Kind: "execute", Status: "pending",
                             RawInput: json.RawMessage(args)},
    Options: [allow/Always once, always/Always, reject/Reject once]})
  err → Deny("permission request failed")
  Outcome.Cancelled → Deny("cancelled")
  OptionID == nil → Deny("no option selected")
  switch *OptionID:
    "allow"  → Allow
    "always" → Allow + ApprovalMemory.Remember(session.ID, name, Allow)  ← 持久化,下次 Memory 层短路
    "reject" → Deny("rejected by user" 或 feedback)
    default  → Deny("unknown option")
```

RPC 帧往返([sdk/server.go:656](acp/sdk/server.go#L656) request):

```
mux.request(ctx, "session/request_permission", req) → rpcResponse:
  ctx 无 deadline → 包 5 分钟超时(防客户端不响应挂死)
  id := nextID() → "ac-N"
  clientCalls["ac-N"] = call{done}
  enqueue(req) → writerLoop → stdout
  select: <-call.done / <-ctx.Done
客户端响应 → stdin → deliverResponse → clientCalls[id].resp + close(done)
doCall: Error → error;json.Unmarshal(Result, &out)
```

Decision 输出去向:executeTools Phase 1(Deny → 拒绝消息;Allow → Phase 2 Start;ModifiedArgs → 替换参数)。

## 11. 分支:buildDynamicContext(plan/模式状态注入)

入口:[acp/server.go:1842](acp/server.go#L1842),输出注入 `oaSession.DynamicContext`

```
entries := ss.PlanEntries()                               ← modeMu 锁内深拷贝快照
mode := ss.Mode()
【有 plan】"## Current Plan" + 每条 "- [优先级] [状态] 内容" + plan_update 提示
【plan 模式】"## Plan Mode" + 无执行工具声明 + 工作流(读文件→分析→plan_create→exit_plan_mode)
【auto/manual 无 plan】"## Task Planning" + enter_plan_mode 提示
```

输出去向:→ buildPrompt `dynamicParts += session.DynamicContext` → 模型每轮看到 plan 状态 + 模式指令;与 §13 plan 工具回调形成闭环(prompt 反映状态 → 模型改状态 → 回调写回 → 下轮 prompt 更新)。

## 12. 分支:模式切换 setSessionMode

入口:[acp/server.go:1077](acp/server.go#L1077)

```
setSessionMode(ctx, sid, mode):
  ss.modeMu.Lock()
  mode=="plan" && ss.mode=="plan" → unlock, return(幂等,保 previousMode)
  ss.transitionModeLocked(mode)     ← 进 plan 时 previousMode = 旧 mode
  ss.modeMu.Unlock()
  persistAndNotifyMode: saveMode(_meta.mode + _meta.previous_mode)
                        + current_mode_update + config_option_update
  applyModeTools(sid, ss, ss.rt)    ← 换工具集 + approver(§3)
```

触发点:OnSetSessionMode / slash /mode / set_config_option("mode")。`set_config_option` 的 model/thought_level 走 `SetModel/SetReasoningEffort` 热更新 runtime(不重建)。

## 13. 分支:plan 工具回调

每轮 `reconcilePlanTools`([server.go:1659](acp/server.go#L1659)):

```
RemoveTools(plan_create, plan_update, enter_plan_mode, exit_plan_mode)   ← 闭包 over sender,必须每轮重挂
AppendTools(plan_update{Execute → ss.ApplyPlanUpdates(updates, underLock)
    → 锁内校验 ID → 应用状态 → underLock: mode=="plan" → sender.SendPlanUpdate(snap)
    → savePlan(_meta.plan)})
switch ss.Mode():
  "plan"   → AppendTools(plan_create{makeCreateCallback} + exit_plan_mode{makeExitCallback})
  default  → AppendTools(enter_plan_mode{setSessionMode("plan") + 首次注入 plan_create/exit_plan_mode})
```

回调细节:

```
makeCreateCallback: modeMu 锁内 SetPlanEntries → savePlan → SendPlanUpdate(mode=="plan" 才发)
makeExitCallback:
  target := PreviousMode()(默认 "auto")
  modeMu 锁内: mode!="plan" → return(并发幂等);transitionModeLocked(target)
              SendPlanUpdate(nil)清面板 → unlock
  persistAndNotifyMode → applyModeTools(同轮恢复执行工具)
```

## 14. 分支:取消 / 关闭 / 删除 / 加载

```
取消: stdin "$/cancel_request" → handleCancelRequest → cancelPending[reqID]()
  → OnPrompt ctx 取消 → RunStream ctx.Err → cancelCompensation(未完成工具写 "cancelled by user" 落库)
  → StreamAborted → PromptResponse{Cancelled} → handlePrompt: wasCancelled → writeResult(Cancelled)

session/close: disconnectMCP + removeSession(内存清 + cancel);meta/messages 保留
session/delete: disconnectMCP + processMgr.Cleanup + removeSession + Runtime.Delete(meta+messages 一起删)

session/load: 内存无 → loadSessionState(_meta: mode/previous_mode/createdAt/config)
  → resolveSessionCwd(请求 cwd 或持久化 cwd)→ 重建 agentSession + connectMCP + buildRuntimeForSession
  → replayHistory(Mem.Recent ≤2000 → user/agent/thought/tool_call(pending)/tool_result(completed))
  → loadPlan → replayPlan(plan 面板)
session/resume: 同构但不回放历史(ACP 规范)

服务关闭: serve defer 链: cancelInFlightPrompts → promptWG.Wait → queue.close → <-writeDone
```

## 15. 分支:OpenViking provider 数据流(切换后)

接线([cmd/cli/server/shared.go:116](cmd/cli/server/shared.go#L116)):

```
cp.Memory == "openviking"   → deps.MemoryProvider = openviking.NewMemory(client)
cp.Skill == "openviking"    → deps.SkillProvider = openviking.NewSkill(client, nil)
cp.Resource == "openviking" → deps.ResourceProvider = openviking.NewResource(client)
client := NewClient(cfg.OpenViking.Endpoint)               ← 直接 HTTP,无 SDK
```

### 知识闭环(记忆)

```
提取(写): run 结束 → Extractor.Extract(scope{UserID}, workingMessages)
  → AsyncExtractor 入队(单 worker,按 user 合并去重)
  → LLMExtractor: transcript(截 4000 token)→ Recall 已有知识对照
      → 模型一次调用输出 [{op: add|update|skip, kind, content, topic}]
      → MemoryProvider.Store(scope, MemoryItem{Topic})
  → openviking.Memory.Store → client.Remember(content)     [HTTP]
      → POST /api/v1/sessions(创建) → /messages/batch(追加) → /commit(服务端异步提取)

召回(读): context.Build → MemoryProvider.Recall(scope{UserID}, query, 5)
  → openviking.Memory.Recall → client.Search(query, 5, "memory")   [HTTP]
      → POST /api/v1/search/search → {memories: [{uri, abstract, score}]}
  → MemoryEntry{Kind, Content: abstract, Score} → ac.Memories → "## Recalled Knowledge"
```

### skill

```
目录: context.Build → SkillProvider.Discover() 全量
  → client.Search("", 100, "skill") → SkillInfo{Name, Description: abstract} → "## Available Skills"
加载: 模型 load_skill → executeLoadSkill → Discover 按名找 → Load(info)
  → loader==nil → return skill.Description(摘要;全文取决于服务端索引)
  → 缓存 → 工具结果进上下文
```

### resource

```
context.Build → ResourceProvider.Search(query, 5) → client.Search(query, 5, "resource")
  → Resource{URI, MIMEType} → ac.Resources → "## Reference Resources"(仅 URI 列表)
  → 模型按需 Read: client.Read(uri) → GET /api/v1/content/read?uri=
```

### HTTP 层([provider/openviking/client.go:82](provider/openviking/client.go#L82) doJSON)

```
json.Marshal(payload) → http.NewRequestWithContext → c.http.Do(120s 超时)
→ io.ReadAll → json.Unmarshal(responseEnvelope{status, result, error})
→ env.Error != nil → apiError;非 2xx → apiError
→ json.Unmarshal(env.Result, out)
```
