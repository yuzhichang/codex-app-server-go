# 计划书：对齐 Codex 最新 app-server 协议并补齐能力缺口

| 项 | 值 |
|---|---|
| 日期 | 2026-10-08（含两轮复审修订） |
| 目标仓库 | `github.com/Zealbase/codex-app-server-go` |
| 当前基线 | SDK `HEAD=c319518`（2026-06-30）；`VERSION=v0.1.2`（README 写 v0.2.0，不一致） |
| 上游参考 | `/home/zhichyu/github.com/openai/codex` @ **`14c8b777`** |
| 上游"版本" | `codex-rs/Cargo.toml:163` `version = "0.0.0"`（**无有效版本号**，见 §2.1） |
| **实现范围** | **仅 stable 面**（= 源码 `experimental=false`）：experimental 不实现（R2）；v1 deprecated 与 `attestation/generate` 不实现（R4） |
| 兼容策略 | **严格对齐上游，不保留旧名/兼容分支**（R3） |
| **Scope 模型** | `declared_stable` = **源码 non-experimental**；导出用于**对账**（R10） |

---

## 0. 决策记录（Decision Log）

| ID | 决策 | 影响 |
|---|---|---|
| **R2** | experimental 全部不予实现 | 89 项（65 ClientRequest + 1 ServerRequest + 23 通知）**不在 scope 内**（非 stable） |
| **R3** | 与上游不一致者一律迁移 + 不兼容旧名 | ① `item/mcp/requestApproval` → `mcpServer/elicitation/request`，删除旧名 ② 删除 `jsonrpc:"2.0"` 字段，无开关 ③ D2/D4/D5/D6/D7 全修 |
| **T1.4** | 同意 | 删除 `jsonrpc` 字段（已按 R3 收紧，取消 `WithJSONRPCVersionField` 开关）；新增 `trace`（W3C Trace Context） |
| **T2.6** | 事件投递：不静默丢、关闭有界、可观测；超时默认 5s | 见 §5.1 T2.6（`publish` 永不阻塞；等待在订阅者侧；终止槽位按 A8 用 `len(out)` 真正预留） |
| **R4** | v1 deprecated 5 项 + `attestation/generate` 不实现 | 入白名单（§5.7）；server-initiated 的入站按 §5.8 静默 decline |
| **R9** | 未实现/未配置的 server-initiated request：静默 decline + 本地记录，不终止会话 | 见 §5.8 T2.13 |
| **R10** | **`declared_stable` 由「源码 `#[experimental]` 分类」定义；导出实测仅用于对账** | 修正上一版"以导出为 Scope 权威"的错误（导出会**排除**方法、且**不过滤**通知） |
| **R11** | 导出与源码的差集必须等于**显式登记集**（5 项），且以**集合**（非数量）比较 | 见 T0.4 |
| **R12** | 终止槽位预留通过 `len(out)` 约束普通事件占用 ≤ `outCap-1` 实现 | 见 T2.6 |
| **R13** | 终止广播用**一次性 `stop` 通道**；`Close()` 只置 terminal + `close(stop)`，`close(out)`/`close(done)` 只由转发 goroutine 执行 | 见 T2.6（修 A9；保证空闲订阅者关闭后必定退出） |
| **R14** | 实现登记的 `kind` 覆盖四个 face（含 `client_notification_sender`），且 face↔kind 满射 | 见 T1.1（修 A10；否则 `initialized` 无法登记） |

> ✅ **remote control 已确认放弃**：`remoteControl/*` 7 个方法全部 `#[experimental]`（`common.rs:1155-1193`），按 R2 不在 scope 内，不开例外。

### 0.1 第一轮复审修订（A1–A5）

| # | 发现 | 修订 |
|---|---|---|
| **A1** | 覆盖率门禁假阳性：用生成的 `Method*` 常量判覆盖 → 生成后缺口自动归零 | T1.1 拆分「声明集 / 实现集」，门禁只查实现集 + 三条防造假校验 |
| **A2** | 独立转发 goroutine 仍不消除共享路径阻塞 | T2.6：`publish` 永不阻塞，等待迁移至订阅者执行路径 |
| **A3** | 终止事件缺可行交付机制 | T2.6：带外 `Err()`/`Done()` 必达 + 带内预留槽位 |
| **A4** | "stable 导出即纯 stable 集"前提不成立（通知未被过滤） | §2.5 改写；通知范围改由源码分类决定 |
| **A5** | 阶段验收与最终 DoD 冲突 | 统一为**全量交付**；里程碑仅为交付顺序 |

### 0.2 第二轮复审修订（A6–A8）

| # | 发现 | 修订 |
|---|---|---|
| **A6** | T0.4 断言在**当前基线上即失败**：源码 ClientRequest 173 / 通知 86 与导出 170 / 84 差 3 / 2 项，且**源码数字并非计数错误** | **T0.4 重设计**：登记 5 项「导出排除」；断言改为**集合相等**（明确差集），不再断言数量相等。**撤回上一版"手工枚举偏 +3/+2"的错误结论**（R11） |
| **A7** | 缺口 59 重复扣除了 legacy：stable 导出的 105 项已不含 3 个 legacy 请求 | **修订为集合差计算**（R10）：缺口 = **62**；新增「核实 43 项已实现归属」任务（T1.1b） |
| **A8** | 转发伪代码未落实终止槽位预留：`select { case out <- head }` 会填满全部 128 槽，终止事件仍无法保证写入 | **T2.6 修正**（R12）：用 `len(out)` 约束普通事件占用 ≤ `outCap-1`，明确容量耗尽后的消费进展等待；补"完全不消费 + 投递 ≥ outCap 个事件后终止"测试 |
| **A9** | 空闲订阅者 `Close()` 可能永不退出：backlog 为空时阻塞在 `waitFor(notify)`，无人唤醒；而 `done` 由该 goroutine 关闭 → `C()`/`Done()` 永不关闭 | **T2.6 修正**（R13）：新增一次性 `stop` 广播通道，**两个等待点都必须 select 到 `stop`**；明确 `Close()` 只置 terminal + `close(stop)`，`close(out)`/`close(done)` 只由转发 goroutine 执行；补"订阅后不发布任何事件直接 Close"测试 |
| **A10** | 实现登记遗漏 ClientNotification：`declared_stable` 含 `initialized`，但 `kind` 无发送路径 → 无法合法登记，门禁不能归零 | **T1.1 修正**（R14）：`kind` 增加 **`client_notification_sender`**，且断言 face↔kind **满射**；接线证据 = `transport.Notify(ctx, <Method*>, …)` 调用点；测试证据 = `TestInitializeUsesProtocolMethods` |

> 两轮复审均未修改文件，由计划书作者落实。

### 0.3 实施期修订（I1–I3，2026-10-08 M0 实施中发现）

| # | 发现（均有实测证据） | 修订 |
|---|---|---|
| **I1** | **锚点必须是 codex 仓库 commit，不能是已安装的 CLI。** 实测本机 `codex-cli 0.160.0` 落后于 `14c8b777`：ClientRequest 少 1（`thread/attachmentOwner/list`，104 vs 105）、ServerNotification 少 1（`thread/prediction/updated`，83 vs 84）。若按原 T0.1「跑 CLI 生成」实施，会**静默丢掉一个方法** | T0.1 同步输入改为 **`--codex-src <repo>`（主路径）**；CLI 只用于新命令 **`diff-cli` 漂移守卫**（集合比较，不一致即失败） |
| **I2** | **生成的 `version.go` 不得含时间戳。** 原 T0.2 要求记录 `GeneratedAt`，但时间戳会让每次 `sync` 都产生 diff，**直接违反 T0.1/T0.4 的"幂等、`git diff` 为空"验收** | T0.2 移除 `GeneratedAt`；`version.go` 只含确定性值（commit、schema sha256、各面计数）。实测已确认 `sync` 幂等 |
| **I3** | 三处实现细节澄清：① 仓库已提交的 `schema/json/` 就是 **stable** 面（`ClientRequest.json` = 105，与 precomputed stable 一致），可直接作为汇总 schema 的 vendor 源（622KB）；② 两个 internal-only 通知的排除理由**有上游显式注释**（`common.rs:1977`/`:1979` *"This event is internal-only"*），理由由推断升级为有据；③ precomputed 解压后约 **4.9MB**，不宜 vendor | T0.1 改为 vendor **聚合 stable schema（622KB）+ 派生的 method-set 小文件**；`verify` 因此不需要 `zstandard`、不需要 repo、不需要网络（CI 安全） |

| **I4** | **D4 的原任务描述有误**：`emittedAtMs` 不是 `params` 的成员。上游 `#[serde(flatten)]` 使线上形状为 `{"method":…,"params":{…},"emittedAtMs":123}`，该字段与 `method`/`params` **同级**，而现有 transport 只取 `raw["params"]`，同级字段在读取阶段即被丢弃 | D4 改为**在 transport 层捕获**（`Notification` 增加 `EmittedAtMs`），并同步修正 §2.3 的 D4 行与 T1.3 |
| **I5** | **T1.1b 审计结果远超计划预期：SDK 有 5 个（不是 1 个）方法被上游删除/改名**，均由 `scripts/coverage_gate.py` 的"wires-up-but-not-upstream"检查机械发现（见 `gen/unknown-methods.txt`）：<br>`config/update`（SetModel/SetApprovalPolicy/SetSandbox 在用）→ 上游改为 `config/value/write`（带 `expectedVersion` 乐观并发）<br>`thread/rollback` → 上游为 `thread/revert`<br>`turn/diff` → 上游**无对应请求方法**（只有 `turn/diff/updated` 通知），需另寻替代或删除<br>`item/mcp/requestApproval` → `mcpServer/elicitation/request`（= 已知 D3）<br>`item/updated` → 上游**已无此通知** | 按 R3 全部迁移/删除；新增任务 **T1.5**。注意 `config/update`、`thread/rollback`、`turn/diff` 三项是**计划书原先未识别的破坏性变更** |
| **I6** | **计划书的"43 已实现"高估了完成度**：它统计的是"有接线"的方法，而 T1.1 的判据要求"接线 **且** 有测试证据"。机械统计（`make coverage`）的真实分布为：**已实现 59 + 已接线但缺测试 44 + 完全未开始 72 + 待迁移 5**（declared_stable=182，白名单 7） | 计划书中 M2–M5 的工作量按"72 未开始 + 44 补测试"重新理解；补测试是低成本项，应优先清掉 |

| **I7** | **门禁是"方法级"的，看不到"类型级"漂移。** 全量审计（含所有 118 个 `Method*` 常量、内联/动态方法名、`Call` 站点）确认：SDK 真实 RPC 面 = **118 个方法，其中恰好 3 个上游不存在**（= `gen/unknown-methods.txt`），**不存在额外的幻影包装**。但对照 `codex_app_server_protocol.v2.schemas.json` 的 660 个 `definitions` 后发现：`client_types_gen.go` 的 55 个结构体里 **27 个在 schema 中无同名定义**。原因分三类：**(a) 命名约定差异（良性）** —— 上游用 `*Params`/`*Response`，SDK 用 `*Request`/`*Result`（如 `InitializeParams`↔`InitializeRequest`、`ThreadStartParams`↔`ThreadStartRequest`）；**(b) SDK 自造类型** —— `RPCRequest`/`RPCResponse`/`RPCNotification`/`RPCError` 在聚合 schema 中**根本不存在**（上游不定义 JSON-RPC 信封），`Capabilities` 对应上游 `InitializeCapabilities`；**(c) 真实孤儿** —— `ThreadRollbackRequest` 无对应（上游是 `ThreadRevertParams`），与方法级审计交叉印证 `thread/rollback` 确已消失。另：`initialize` 的 `Capabilities` 命名与上游不一致，说明 T1.2 若做 codegen 会产出**不同名字**，需先定命名映射 | T1.2 增加"类型级对账"：即使暂不做全量 codegen，也要比对 SDK 结构体名与 schema 定义名，并对**有意改名**维护显式白名单，否则类型级漂移永远不可见 |
| **I8** | **`TurnRead` 未使用幻影 RPC，但用的是上游已弃用路径**：`readTurn`（`wait.go:121-141`）走 `thread/read{includeTurns:true}`。上游明确标注全量 hydrate 对分页 thread 已弃用，应改用 `thread/turns/list` + `thread/items/list`（`thread.rs:1703-1706`） | 不改方法归属（`thread/read` 真实存在），但记录为"弃用用法"，纳入 M5 的读路径改造 |

> 附带结论 1：M0 的机械提取（`scripts/codex_schema_surface.py`）**独立复现了计划书的全部计数**（173/65/108、11/1/10、86/23/63、1/0/1），且 6 条集合断言全绿 —— 此前手工推导的数字现已变为**机器验证**。
>
> 附带结论 2：D1 的移除已在本机真实 `codex app-server --listen ws://…` 上验证通过（无 `jsonrpc` 字段的握手与只读 RPC 均成功）。同时发现 **stdio 路径无法对齐**：它由 `jrpc2` 驱动，该库自身会发出并要求 `jsonrpc` 字段，故 `versionFixerReader` 入站修补必须保留 —— 这是"library 行为"而非我们可删的兼容 shim。

---

## 1. 结论摘要（TL;DR）

1. **没有"schema 版本"可以对齐。** 唯一锚点 = codex commit（**`14c8b777`**）+ 产物 sha256。
2. **Scope 权威是源码的 `#[experimental]` 分类，不是导出模式**（R10）。导出**会排除** 5 个方法、且**不按 experimental 过滤通知**。
3. **`declared_stable`**：ClientRequest **108**、ServerRequest **10**、ServerNotification **63**、ClientNotification **1**。
4. **本期需实现缺口**（集合差，见 §2.2）：ClientRequest **62**、ServerRequest **2**、通知侧达成 **61** 目标。
5. **不实现基线（声明为 stable 但本期不实现）= 8 项**（R4 6 项 + 内部通知 2 项）；experimental 89 项**因非 stable 而不在 scope**，二者须分开登记。
6. **`fs/*`(9)、`plugin/*`(12) + `marketplace/*`(3)、`mcpServer/*`(5) 全部为 0**，是主要补齐对象。
7. **reconnect 上游无协议级支持**，须 SDK 侧实现会话监督器（§5.1）。

---

## 2. 调研结论（Ground Truth）

### 2.1 上游没有协议版本号

- `codex-rs/Cargo.toml:163` → `version = "0.0.0"`（`codex-app-server-protocol` 继承 workspace）。
- `codex-rs/app-server-protocol/src/rpc.rs:11` → `JSONRPC_VERSION = "2.0"`，**全仓无引用（死代码）**。
- 结论：**锚点 = git commit + vendored 产物 sha256**。

### 2.2 Scope 与缺口（集合模型，实测于 `14c8b777`）

| 面 | 源码总数 | 源码 experimental | **declared_stable**（源码 non-exp） | 导出 stable | 导出 experimental | 导出排除 | 白名单 | 已实现 | **待实现** |
|---|---:|---:|---:|---:|---:|---:|---:|---:|---:|
| ClientRequest | **173** | 65 | **108** | 105 | 170 | **3** | 3（R4） | 43 | **62** |
| ServerRequest | **11** | 1 | **10** | 10 | 11 | 0 | 3（R4） | 5 | **2** |
| ServerNotification | **86** | 23 | **63** | 84 | 84 | **2** | 2（内部通知） | 待重基线 | 达成 **61** 目标 |
| ClientNotification | **1** | 0 | **1** | 1 | 1 | 0 | 0 | 1 | 0 |

**集合关系（必须全部成立，由 T0.4 断言）**：

1. `源码全集 − 导出_experimental == 导出排除集`（**恰好** 5 项，按名登记，见下）
2. `导出_experimental − 源码全集 == ∅`
3. `导出_experimental − 导出_stable == 源码 experimental 集`（ClientRequest 65 ✅、ServerRequest 1 ✅）
4. `导出_stable(ServerNotification) == 导出_experimental(ServerNotification)`（**成立且为已知上游行为**：通知不被过滤）
5. `导出_stable(ClientRequest) == declared_stable(ClientRequest) − 3 legacy`
6. `导出_stable(ServerNotification) == declared_stable(63) + 23(泄漏) − 2(内部)  == 84` ✅

**导出排除集（5 项，R11 登记）**：

| 方法 | 面 | 排除原因 |
|---|---|---|
| `getAuthStatus` | ClientRequest | v1 deprecated 顶层方法；导出不产出 |
| `getConversationSummary` | ClientRequest | 同上 |
| `gitDiffToRemote` | ClientRequest | 同上 |
| `rawResponse/completed` | ServerNotification | internal-only；不对客户端暴露 |
| `rawResponseItem/completed` | ServerNotification | 同上 |

**缺口推导（集合差，不重复扣除）**：

- ClientRequest：`declared_stable(108) − 白名单(3) − 已实现(43) = 62`
  - 注：其中 3 个 legacy **既不在导出、也已按 R4 入白名单**；不得再对"导出 stable 105"重复减 3（这是上一版 59 的错误来源）。
- ServerRequest：`10 − 3 − 5 = 2`
- ServerNotification：`declared_stable(63) − 内部通知(2) = 61`（实现目标）

> 口径与证据：
> - **源码计数**：`common.rs` 四个宏调用块的变体数 → ClientRequest **173**、ServerRequest **11**（9 显式 wire + 2 由变体名转 camelCase：`applyPatchApproval`/`execCommandApproval`）、ServerNotification **86**、ClientNotification 1。
> - **导出计数**：解压 `schema/precomputed/app-server-exports-{stable,experimental}.json.zst`，取 `json_schema[*]["method"].enum` 并集。
> - **两份数据均经独立复核**；差集恰好为上述 5 项（已在两个导出中均不存在）。
> - ⚠️ 不要用 `grep '"method"'` 统计聚合 JSON（嵌套对象会偏高：实测 210/20/170）。

### 2.3 已确认的不兼容 / 缺陷（R3 下全部需修正）

| # | 问题 | 证据 | 处置 |
|---|---|---|---|
| D1 | SDK 发送 `"jsonrpc":"2.0"`；上游既不发送也不期望 | 上游 `rpc.rs:1-2`；SDK `internal/transport/transport.go` | **删除，不提供开关** |
| D2 | `Capabilities.OptOutNotificationMethods` 类型为 `bool`，上游是 `Option<Vec<String>>` | 上游 `protocol/v1.rs:65`；SDK `client_types_gen.go` | 改 `[]string`（静默失效 bug） |
| D3 | SDK 用 `item/mcp/requestApproval`；上游已无此名 | SDK `envelope.go:46`、`interaction.go:218`；上游 ServerRequest 面 | 迁移到 `mcpServer/elicitation/request`，**删旧名** |
| D4 | 通知信封新增 `ServerNotificationEnvelope.emittedAtMs`，SDK 未建模 | 上游 `common.rs:2067-2080` | **建模并在 transport 层捕获**（`emittedAtMs` 与 `method`/`params` **同级**，见 T1.3 的 D4 说明；放在解码器里无效） |
| D5 | `InitializeCapabilities` 缺 `explicitGatewayOauth`/`requestAttestation`/`extensions`；`ClientInfo` 缺 `title`；`client.go` 硬编码 `codex-go-sdk/0.1.0` | 上游 `protocol/v1.rs:29-70`；SDK `client.go:90-109` | 补字段；`ClientInfo` 可配置 |
| D5b | 默认宣告 `experimentalApi: true`，与 R2 矛盾 | SDK `client.go:95-97` | **默认 false** |
| D6 | 版本元数据矛盾（`0.141.0` vs `0.142.0`；`v0.1.2` vs `v0.2.0`；同步脚本路径失效且不写 pinned 版本） | `version.go:3-4`、`VERSION`、`README.md:9,40,106`、`scripts/update-codex-go-schema.sh:6-7` | 由生成器/单一来源产出 |
| D7 | `llms.txt` 模块路径写成 `github.com/nharness/sdk/codex-go` | `llms.txt:2-5` | 修正 |

### 2.4 reconnect 的上游语义

- **无协议级 ping/keepalive/session-resume**。唯一 ping 是 WS 帧级（`app-server-transport/src/transport/websocket.rs:363-373`）。
- **订阅按连接**：`thread_state.rs:344-348`；断连 `remove_connection()`（`thread_processor.rs:3605`）。
- **无客户端事件重放**：`app-server/src/transport.rs:204-243`。
- **官方替代 = `thread/resume`**（`thread_state.rs:59-64`）。
- 传输面仅 `stdio://`/`unix://`/`ws://`/`off`（`mod.rs:80-166`）；**无 HTTP/SSE**。

### 2.5 导出与源码的关系（修订 A4/A6）

- CLI：`codex app-server generate-json-schema --out <DIR> [--experimental]`（`cli/src/main.rs:727-735`，分发 `1421-1425`）。
- **结论 1**：`--experimental` 对 ClientRequest/ServerRequest **有效**（导出 exp-only = 65 / 1，与源码 experimental 计数**精确一致**）。
- **结论 2**：`--experimental` 对 **ServerNotification 无效** —— stable 与 experimental **同为 84**（集合相等）；源码 23 个 experimental 通知**全部泄漏**进 stable 导出。
- **结论 3**：导出**排除** 5 个源码方法（3 v1 deprecated client + 2 internal-only 通知）。
- **结论 4**：stable 导出是 experimental 导出的严格子集（各面 stable-only = 0）。
- **推论**：Scope 必须由**源码 non-experimental** 定义（R10）；导出用于**对账**，且对账必须是**集合比较 + 显式登记差集**（R11）。

### 2.6 schema 生成与分发

- 运行时不推导 schema：`precomputed_exports.rs:15-18` 解压内嵌 `.zst`。
- fixtures：`schema_fixtures.rs:95-153`。
- 上游已提交可 vendored 产物：`schema/json/{codex_app_server_protocol.schemas.json, codex_app_server_protocol.v2.schemas.json, ClientRequest.json, ServerRequest.json, ServerNotification.json, ClientNotification.json}` + `schema/json/v2/*.json`。
- **实测澄清（I3）**：仓库里已提交的 `schema/json/` 就是 **stable** 面 —— `schema/json/ClientRequest.json` 的方法集大小 = 105，与 precomputed stable 一致。因此 `schema/json/codex_app_server_protocol.v2.schemas.json`（622KB）可直接作为 stable 汇总 schema 的 vendor 源。
- **实测澄清（I3）**：precomputed `.zst` 解压后约 **4.9MB**（含 typescript + json_schema + internal），**不宜 vendor**；改为 vendor 聚合 schema + **派生的方法集小文件**（`.codex-schema/exports/{stable,experimental}-methods.json`），使 `verify` 无需 `zstandard`。

---

## 3. WS0 — schema 同步基础设施（最高优先级）

### T0.1 重写同步脚本

- 替换 `scripts/update-codex-go-schema.sh`（路径失效，D6）。
- ✅ **已实施**：`scripts/codex_schema_surface.py`（子命令 `sync` / `verify` / `diff-cli`），`make sync` 驱动。
- **输入是 codex 仓库（不是 CLI，见 I1）**：`--codex-src <path>`；从仓库同时取得
  1. 源码 `.../protocol/common.rs` → `declared_stable` + experimental 标注；
  2. `schema/precomputed/*.zst` → stable/experimental 导出方法集（对账用）；
  3. `schema/json/codex_app_server_protocol.v2.schemas.json`（stable 汇总，622KB）→ vendor 到 `internal/protocol/schema/`。
- 记录 `git -C <repo> rev-parse HEAD`；计算全部产物 sha256；**不写时间戳**（见 I2）。
- 新增 `diff-cli`：把 CLI 生成的 bundle 与锚点做集合比较，不一致即失败（防"用落后/超前的 CLI 重新生成"）。

### T0.2 `version.go` 由生成器书写

- ✅ **已实施**：`version.go` 由 `sync` 生成并 `gofmt`；只含**确定性**值 —— `SourceCodexCommit`、`SchemaRevision`（聚合 schema 的 sha256）、`SchemaTitle`、四个 `DeclaredStable*`、四个 `SourceExperimental*`、`NotInScopeTotal`。
- **不写 `GeneratedAt`**（I2：时间戳破坏幂等）；**已删除 `PinnedCodexVersion`** 与 `SchemaDefinitionCount`（后者源于已退役的 core-subset 文件）。

### T0.3 退役 "core subset" 模型

✅ **已实施**：删除 `internal/protocol/schema/v2.schema.json`（56 `$defs` 裁剪件）及其哈希逻辑；改为 vendor 上游产物（`internal/protocol/schema/codex_app_server_protocol.v2.schemas.json`，622KB stable 汇总）。
同时退役被取代的脚本：`scripts/update-codex-go-schema.sh`（路径失效 D6）、`scripts/generate_schema.go` 与 `scripts/compare_schema.py`（原 `make generate` / `make check-schema-drift` 走 CLI，存在 I1 的版本错位风险）。`Makefile` 目标改为 `sync` / `verify` / `diff-cli` / `conformance`。

### T0.4 源码 ↔ 导出 对账（**按 A6 重设计**）

> ⚠️ **原设计缺陷**：断言"源码规模 == 导出规模"，但当前基线即相差 3 / 2 项，断言必然失败；且**源码数字本身是正确的**（不是计数错误）。必须改为**集合比较 + 显式登记差集**。

**产出 `gen/method-surface.json`**（Scope 唯一机器可读来源）：
```json
{ "method": "fs/readFile",
  "face": "client_request",
  "experimental": false,
  "experimental_reason": null,
  "in_export_stable": true,
  "in_export_experimental": true,
  "export_excluded": false }
```

**产出 `gen/export-exclusions.json`**（5 项，人工评审 + 理由）：
```json
[ { "method": "getAuthStatus", "face": "client_request", "reason": "v1 deprecated; not emitted by export" },
  { "method": "getConversationSummary", "face": "client_request", "reason": "v1 deprecated" },
  { "method": "gitDiffToRemote", "face": "client_request", "reason": "v1 deprecated" },
  { "method": "rawResponse/completed", "face": "server_notification", "reason": "internal-only" },
  { "method": "rawResponseItem/completed", "face": "server_notification", "reason": "internal-only" } ]
```

**断言（全部为集合比较，任一失败即 CI 失败并要求人工复核）**：

| # | 断言 | 当前基线期望 |
|---|---|---|
| 1 | `源码全集 − 导出_experimental == export-exclusions.json` | 恰好 5 项 |
| 2 | `导出_experimental − 源码全集 == ∅` | ∅ |
| 3 | `导出_experimental − 导出_stable == 源码 experimental 集`（按面分别比较） | ClientRequest 65、ServerRequest 1 |
| 4 | `导出_stable(ServerNotification) == 导出_experimental(ServerNotification)` | 相等（**登记为已知上游行为**；若变为不等 → 说明上游开始过滤通知，**必须复核并更新 Scope**） |
| 5 | `导出_stable(ClientRequest) == declared_stable(ClientRequest) − 3` | 105 == 108 − 3 |
| 6 | `export-exclusions.json` 内容相对上次无变化 | 否则需人工确认 |

**验收**：脚本幂等（`git diff` 为空）；六条断言全绿；manifest 记录 commit 与 sha256。

---

## 4. WS1 — JSON Schema 对齐 codex 最新代码

### T1.1 覆盖判定与差分工具（**按 A1 重设计**）

> ⚠️ **原设计缺陷**：用 `protocol.Method*` 常量是否存在判断"是否实现"。而 T1.2 会从 schema **生成全部常量**，导致生成后缺口自动归零，即使没有任何 `Client` 方法、dispatcher 分支或通知解码。**必须把「协议声明」与「实际实现」分开登记，门禁只查后者。**

- **两个集**：
  - **声明集 declared**：由 T1.2 生成的常量与类型。**仅表示协议中存在，不构成任何覆盖信号。** 生成物头部须加醒目注释。
  - **实现集 implemented**：显式登记 `gen/implemented-methods.json`（人工评审、随 PR 更新）：
    ```json
    { "method": "fs/readFile", "face": "client_request", "kind": "client_method",
      "call_site": "(*Client).FSReadFile", "test": "TestFSReadFile" }
    ```
    `kind` ∈ `client_method` | `server_request_handler` | `notification_decoder` | `client_notification_sender`。
- **face ↔ kind 必须一一覆盖（修 A10）**：`client_request` → `client_method`；`server_request` → `server_request_handler`；`server_notification` → `notification_decoder`；`client_notification` → **`client_notification_sender`**（client → server 的**发送**路径，不是解码路径）。四者构成满射，校验器需断言「任一 face 都有对应 kind 可用于登记」，否则 `declared_stable` 中的 `initialized`（ClientNotification）将无法合法登记，门禁永远无法归零。
- **门禁**：`declared_stable − whitelist(§5.7, 8 项) − implemented == ∅`。
- **三条防造假校验（必须同时满足，否则该条视为未实现）**：
  1. **接线证据（按 kind 分派）**：
     - `client_method` → 某 `Client`/`SessionThread` 方法体内出现该 `Method*` 调用点（**排除**常量定义文件与生成物）；
     - `server_request_handler` → `Dispatcher.HandleServerRequest` 有对应 `case`；
     - `notification_decoder` → `decodeEvent` / `extraEventTarget` 有对应 `case`；
     - `client_notification_sender` → 存在 **`transport.Notify(ctx, <Method*>, …)`** 的调用点（**排除**常量定义文件与生成物）。当前基线：`initialized` 的证据为 `(*Client).Initialize` 内的 `Notify(ctx, protocol.MethodInitialized, nil)`。
  2. **测试证据**：存在具名测试且确实引用该方法常量或走通该分支。当前基线：`initialized` 由 `client_test.go:62` `TestInitializeUsesProtocolMethods` 覆盖，该测试同时断言 `callMethod == "initialize"` **与** `notifyMethod == "initialized"`（`client_test.go:73-78`），可直接作为 `client_notification_sender` 的测试证据；`client_extended_test.go:31` `TestInitializeNotifyFails` 覆盖 Notify 失败路径。
  3. **双向一致**：无孤立登记，也无未登记接线。
- **T1.1b 核实 43 项已实现归属（按 A7 新增）**：逐一审计当前 43 个已实现方法，分类为
  (a) 属于 `declared_stable` 且仍有效 → 计入实现；
  (b) 上游已移除/改名（如 `item/mcp/requestApproval`）→ 按 R3 删除；
  (c) 属于 experimental → 按 R2 需移除或明确保留理由。
  结果写入 `gen/implemented-methods.json` 并附对账说明。**在 (b) 清理完成前，不得把 43 当作有效基数**。
- ✅ **已实施**：`scripts/coverage_gate.py`（`report` / `write` / `check [--strict]`）+ `gen/whitelist.json`（7 项）+ `gen/implemented-methods.json` + `gen/unknown-methods.txt`；`make coverage` / `make conformance` / `make conformance-strict`。
  - **判据按 face 绑定 kind**（不用"扫描全部 role 再取优先级"——`client.go` 同时承载 client_request 与唯一 client notification，那种写法会把请求误判为 `client_notification_sender` 后被 Notify 检查丢弃，实测造成 44 个方法的假缺口）。
  - **测试证据放宽**为"引用常量 **或** 引用 wire 方法字符串"：SDK 的测试普遍用 wire 字符串驱动 mock server，只认常量会严重低估。
  - 新增 4 条自检：白名单项必须属于 `declared_stable`；白名单项不得已被实现（矛盾）；已接线但上游已删除的方法必须为空；双向一致。
- **实测结果（`make coverage`）**：`declared_stable=182 / 白名单=7 / 已实现=59 / 缺口=116`，其中 **未开始 72 + 已接线但缺测试 44**；另有 **5 个待迁移方法**（I5）。
- **输出**：`gen/method-surface.json`、`gen/export-exclusions.json`、`gen/not-in-scope.txt`、`gen/whitelist.json`、`gen/implemented-methods.json`、`gen/unknown-methods.txt`。

### T1.5 迁移上游已删除/改名的方法（**I5 新增**）

`scripts/coverage_gate.py` 的 `wires-up-but-not-upstream` 检查机械发现 5 项，比计划原以为的多 4 项。按 R3 一律迁移、**不留旧名**：

| SDK 现有方法 | 上游现状与**权威参数形状**（取自 codex 源码） | 处置 |
|---|---|---|
| `config/update` | 不存在。替代为 `config/value/write`，其 `ConfigValueWriteParams { key_path, value, merge_strategy, file_path?: Option, expected_version?: Option }`（`protocol/v2/config.rs:1101-1110`）；版本需先经 `config/read`（`ConfigReadParams { include_layers, cwd? }` → `ConfigReadResponse { config, origins, layers? }`）取 `origins[key].version` | 迁移 `SetModel`/`SetApprovalPolicy`/`SetSandbox`。**非改名**：需补 `keyPath`+`mergeStrategy`，并新增乐观并发读版本流程；还要确定 `model`/`approval_policy`/`sandbox_mode` 的真实配置键名 |
| `thread/rollback` | 不存在。替代为 `thread/revert`，`ThreadRevertParams { thread_id, before_turn_id }`（`protocol/v2/thread.rs:1288-1292`），语义是"**排除该 turn 及其之后的所有 turn**"；响应 `ThreadRevertResponse { thread, cursor }`，`turns` 恒为空，须用 `thread/turns/list` 回填 | **非改名 → 语义变更**：现有 `ThreadRollback(ctx, ThreadRollbackRequest{TurnIDs []string})` 是"按 id 列表回滚"，无法一一对应。需重新设计 API（单 `beforeTurnID` + 游标回填），并同步 `SessionThread.Rollback` |
| `turn/diff` | **不存在对应的请求方法**（`turn/*` 仅有 interrupt/settings-update/start/steer 四个请求；`Turn` 结构体也**无 diff 字段**，`thread_data.rs:386-409`）。diff 数据仍可取（聚合 diff 只经 `turn/diff/updated` 通知推送；逐文件 diff 在 `FileUpdateChange.diff`，`item.rs:1146-1150`），但**JSON-RPC 层面没有 `turn/diff` 这个方法** | ✅ **已完成（用户决策）**：JSON-RPC 没有 `turn/diff`，**SDK 侧就不实现它**。删除 `MethodTurnDiff`、`TurnDiffRequest`/`TurnDiffResult`、`Client.TurnDiff`、`SessionThread.GitDiff` 及 `types.go` 别名；同步清理 `docs/api-reference.md`、`docs/index.md`、`llms.txt`、`llms-full.txt` 的悬空引用；`TestSessionThreadGitDiff` 一并删除。**保留** `TurnDiffUpdatedEvent`（它是真实存在的上游通知） |
| `item/mcp/requestApproval` | → `mcpServer/elicitation/request` | = 原 D3，见 §5.3 T2.8 |
| `item/updated` | 上游已无此通知 | ✅ **已完成**：删除 `MethodItemUpdated`、`ItemUpdatedEvent` 结构体/别名/解码分支/deref 分支；`wait.go` 的 `eventMatchesTurn` 改由既有的 `RawNotificationEvent` 分支覆盖（`wait.go:197`）。原测试改写为 `TestRemovedNotificationFallsBackToRaw`，断言回退为 `RawNotificationEvent` |

> ⚠️ **重要更正**：I5 初版把 `thread/rollback → thread/revert` 与 `config/update → config/value/write` 视为"改名迁移"。读源码后确认二者都是**语义变更**，迁移成本远高于改名，必须按新 API 设计而非机械替换。`turn/diff` 更须产品决策。

**验收**：`make conformance-strict` 的 `wires-up-but-not-upstream` 检查为空。当前剩余 4 项（`config/update`、`thread/rollback`、`turn/diff`、`item/mcp/requestApproval`）。

### T1.2 生成 Go 协议类型

- **A. codegen（建议）**：从 vendored schema 生成 params/response 结构体 + 方法名常量（`atombender/go-jsonschema` 或自写模板）；文件头 `// Code generated; DO NOT EDIT.` + 覆盖判定说明注释。
- **B. 手写 + T1.1 卡漏项。**
- ⚠️ **无论 A/B：生成常量绝不作为覆盖依据。**

### T1.3 修复类型/建模缺陷（D2/D4/D5/D5b）

- ✅ **D2 已修**：`Capabilities.OptOutNotificationMethods` → `[]string`；回归测试同时断言"旧 `bool` 形状必须解码失败"。
- ✅ **D5 已修**：`Capabilities` 补 `ExplicitGatewayOauth`/`RequestAttestation`/`Extensions`；`ClientInfo` 补 `Title`；新增 `WithClientInfo(name, title, version)`；新增 `options.go` 的 `Version` 变量（可由 `-ldflags -X` 注入，取代硬编码 `0.1.0`）。
- ✅ **D5b 已修**：默认不宣告任何 capabilities —— `InitializeRequest.Capabilities` 改为**指针**（`nil` → 字段整体从线上省略，对齐上游 `Option`）；`New()` 不再硬编码 `ExperimentalAPI: true`；新增 `WithInitializeCapabilities` 供显式覆盖。
- ✅ **D1 配套**：`RPCRequest`/`RPCResponse`/`RPCNotification` 移除 `Version`（实测聚合 schema 中根本不存在 JSONRPC 信封类型，故这三个 `Version` 字段本就是 SDK 自造）。
- ⏳ **D4 待办（本轮未做，发现需改设计）**：`ServerNotificationEnvelope.emittedAtMs` **不是 `params` 的成员** —— 上游 `#[serde(flatten)] notification` + `emitted_at_ms`（`common.rs:2067-2080`）意味着线上形状是 `{"method":…,"params":{…},"emittedAtMs":123}`，`emittedAtMs` 与 `method`/`params` **同级**。而现有 transport 只把 `raw["params"]` 取出（`websocket.go` / `transport.go` 的 readLoop），**同级字段在这一步就被丢弃**。因此 D4 必须在 **transport 层**（`Notification` 增加 `EmittedAtMs`）而不是解码器里实现 —— 原任务描述有误，已修正。

### T1.4 线格式严格对齐（D1 + trace）✅ 已完成

- ✅ **删除 `"jsonrpc":"2.0"`**：`requestEnvelope`/`notificationEnvelope`/`replyEnvelope` 移除 `Version`；删除 `JSONRPCVersion` 常量；`transport.go` / `http.go` / `websocket.go` 中全部 `Version: "2.0"` 字面量清除。**未提供兼容开关**（R3）。
  - **唯一保留的例外（必要且有意）**：stdio 传输由 `jrpc2`（严格 JSON-RPC 2.0 实现）驱动，它既发出也要求该字段；其入站修补 `versionFixerReader`（`stdio.go:153-196`）必须保留，除非替换 jrpc2。已在 `transport.go` 与 `stdio.go` 就地注释说明。
- ✅ **新增 `trace`**：`TraceContext{Traceparent, Tracestate}`（形状取自 `codex-rs/protocol/src/protocol.rs:168-175`；该类型不在 app-server 聚合 schema 中，故本地建模）+ `WithTraceContext(ctx, *TraceContext)` / `TraceContextFromContext(ctx)`；`JSONRPCTransport.Call`、`WebSocketTransport.Call`、`HTTPTransport.Call` 自动注入。**仅请求携带**（上游 `JSONRPCNotification` 无 `trace`）。
- ✅ **回归测试**：`internal/transport/wire_shape_test.go`（出站无 `jsonrpc`、trace 仅在请求上、helper nil 安全）、`internal/protocol/schema/client_types_wire_test.go`。
- ✅ **真实 server 验证**（T1.4 明确要求）：`tests/real/wire_alignment_real_test.go` 对本地 `codex app-server --listen ws://…` 执行**无 `jsonrpc` 字段**的 `initialize`+`initialized` 握手与只读 RPC，**实测通过**。

**验收**：出站不含 `jsonrpc` ✅；`trace` 可注入 ✅；D2/D5 有回归单测 ✅；真实 server 接受无字段信封 ✅。

---

## 5. WS2 — 协议能力缺口补齐（仅 stable）

统一规范：①`Method*` 常量 ②请求/响应类型 ③实际接线 ④单测 ⑤`docs/api-reference.md` ⑥`llms.txt` ⑦登记 `gen/implemented-methods.json`。**不得引入 experimental 门控语义**（R2）。

### 5.1 reconnect（会话监督器）

- **T2.1 transport**：WS keepalive（`conn.Ping` + 超时）；`ReconnectingWS` 修复重拨窗口期内 `Call` 打到死连接。
- **T2.2 会话重建**：新增 `SessionSupervisor` —— 监听 `Done()` → 重拨 → **重跑 `initialize`+`initialized`** → 对活跃 thread 调 `thread/resume`；维护"期望订阅集"；事件回填用 `thread/read` + `thread/turns/list` + `thread/items/list`；提供 `WithAutoReconnect`。
- **T2.3 在途操作**：修正 `retry.go:125-127`（`ErrClosed` 不可重试导致掉线瞬间全硬失败）；区分只读可重试 / 写不重试；`Notify` 与 server-request 回包不重试。
- **T2.4 审批跨重连**：新增 `PendingApprovalLostEvent{ThreadID, TurnID, Method}`；approval 可配置超时。
- **T2.5 可观测性事件**：`ReconnectStarted/Succeeded/Failed`、`SessionRecovered{ThreadsResumed}`、`EventsLost{GapFrom, GapTo}`、`PendingApprovalLost`、`UnhandledServerRequest`。

- **T2.6 事件投递：不静默丢、关闭有界、可观测（超时默认 5s）— 按 A2/A3/A8 重设计**

  **不变目标**：不静默丢弃、关闭有界、丢失可观测。
  **两条硬约束**：
  - **C1：`publish` 在任何情况下都不等待订阅者。** 若在共享发布路径上为慢订阅者等待超时，路径会被 O(慢订阅者数 × 5s) 拖住；加转发 goroutine、释放锁都**不能**解决 —— 等待必须只发生在**订阅者自身执行路径**上。
  - **C2：终止通知必须有不依赖消费者读取的交付通道**；且**带内终止事件必须真正预留槽位**（仅声明"预留"而用 `select` 直接写会填满通道，预留形同虚设）。

  **结构（每订阅者一份）**：
  ```
  backlog   有界双端队列, maxBacklogEvents (默认 4096)
  out       chan Event, cap = outCap (默认 128, 要求 >= 2)
  terminal  atomic { reason: "stall"|"overflow"|"closed", since, lostCount }
  stop      chan struct{}   // 终止广播: 首次置 terminal 时由胜者关闭一次(sync.Once)
  done      chan struct{}   // 带外终止信号: 由转发 goroutine 退出流程中关闭
  notify    chan struct{}, cap 1   // 合并唤醒信号(仅表示"有新事件")
  ```

  **`publish(ev)`（永不阻塞）**：对每个订阅者 —— 取短锁；已终止则跳过；`len(backlog) >= maxBacklogEvents` → 置 `terminal(overflow)`（计入 `lostCount`，并由胜者 `close(stop)`）后跳过；否则 append；解锁；非阻塞发 `notify`。耗时 O(#订阅者)，仅 append + atomic，**无任何超时等待**。

  **唤醒与退出（修 A9，关键）**：

  - **`terminal` 的置位者有三类**：`Close()`（`"closed"`）、`publish` 溢出（`"overflow"`）、转发 goroutine 自身停滞（`"stall"`）。**任一置位者都必须由 `sync.Once` 保护、只关闭一次的 `stop` 通道做广播。**
  - **转发 goroutine 的两个等待点都必须 `select` 到 `stop`**：空闲等待 `select { case <-notify: case <-stop: }`（**不能只等 `notify`**）；投递等待 `select { case out <- head: …; case <-time.After(remaining): …; case <-stop: … }`。
  - **为什么不能只依赖 `notify`**：`notify` 是 cap=1 的**合并**信号，可能已有待处理 token（此时非阻塞发送失败，且没有人会被唤醒）。因此"订阅后从未收到任何事件即 `Close()`"的场景下，若无 `stop`，转发 goroutine 会**永久阻塞**在 `notify` 上 → `out` 永不关闭、`done` 永不关闭 → 消费方的 `for range sub.C()` 与 `<-sub.Done()` **永久挂起**（goroutine 泄漏）。`close(stop)` 对阻塞在该通道上的 goroutine 是**可靠唤醒**，与合并信号状态无关。
  - **退出责任划分**：`Close()` 只负责「CAS 置 `terminal("closed")` + `close(stop)`」并**立即返回**（不等待、不阻塞、幂等）；**`close(out)` 与 `close(done)` 一律只由转发 goroutine 在其退出流程中执行**，以保证 `close(out)` 之后不再有写入（避免 send-on-closed panic）。`stop` 与 `done` 各自由 `sync.Once` 保护；`Close()` 在 stall/overflow 已触发后调用必须幂等且不 panic。
  - **顺序**：置 terminal → `close(stop)` → goroutine 观察到 `stop`/`terminal` → 写预留槽终止事件 → `close(out)` → `close(done)`。消费方经 `C()`（关闭或终止事件）**或** `Done()`/`Err()` 感知终止，二者都不依赖消费方是否读取 `C()`。

  **转发 goroutine（每订阅者 1 条；等待只在此处）—— 含槽位预留（修 A8）+ 可靠退出（修 A9）**：

  ```
  stallDeadline: time.Time  // 零值=未进入停滞
  for {
      if terminal != nil { break }
      if len(backlog) == 0 {
          select { case <-notify: case <-stop: }   // 必须可被 stop 唤醒
          continue
      }
      head := backlog[0]

      // 关键：普通事件占用严格 <= outCap-1，最末槽位专供终止事件
      if len(out) >= outCap-1 {
          if stallDeadline.IsZero() { stallDeadline = now() }
          if since(stallDeadline) > timeout { setTerminalOnce("stall"); break }
          select {                                  // 等待消费进展(有界)，且可被 stop 唤醒
          case <-time.After(pollInterval /* 50ms */):
          case <-stop:
          }
          continue
      }
      select {
      case out <- head:
          pop(backlog); stallDeadline = zero      // 消费进展 → 重置
      case <-time.After(remaining(stallDeadline, timeout)):
          setTerminalOnce("stall"); break
      case <-stop:
          break
      }
  }
  // 终止交付(仅此 goroutine 执行；观察到 stop 即蕴含 terminal 已置位)
  if terminal != nil {
      out <- terminalEvent   // 保证立即成功：普通事件最多占 outCap-1，最末槽位必空
  }
  close(out)
  close(done)                // 带外（权威）
  ```

  ⚠️ **实现注意（易踩）**：Go 中 `select` 内的 `break` **只跳出 `select`，不跳出 `for`**。上面对 `<-stop` 的分支必须用**标签化 break**（`break loop`）或退出标志位实现，否则会在 `terminal` 已置位后空转。`setTerminalOnce` 亦须为幂等（`sync.Once` 或 CAS）。

  **预留正确性论证**：`out` 的**唯一写者**是本转发 goroutine；守卫 `len(out) >= outCap-1` 保证普通发送后占用 ≤ `outCap-1`；读者只会减少 `len`。因此第 `outCap` 个槽位**恒为空**，终止事件的发送**必定立即成功**（非阻塞语义）。⚠️ `outCap` 必须 ≥ 2。

  **容量耗尽后如何等待消费进展**：走 `len(out) >= outCap-1` 分支，以 `pollInterval`（50ms）轮询 + `stallDeadline` 计总超时；消费恢复（`len` 下降）即回到正常路径并重置 `stallDeadline`；持续无进展 → `terminal("stall")`。

  **终止交付双通道（修 A3）**：
  1. **带外（权威、必达）**：`EventSubscription.Err() error` + `Done() <-chan struct{}`。即使消费方**从不读** `C()`，终止状态也必定可见、`done` 必定关闭 → 这是"关闭有界 + 丢失可观测"的保证。
  2. **带内（由预留保证）**：终止事件写入预留槽位后 `close(out)`；消费方始终不读时事件静置缓冲内，**不阻塞任何 goroutine**。

  **语义小结**：正常路径无丢失且 `publish` 不阻塞；异常路径（stall / backlog 溢出 / 显式 Close）经 `Err()`/`Done()` + 预留槽位收敛，`lostCount` 体现丢弃量。**`Close()` 返回后，转发 goroutine 必定在有限时间内退出并关闭 `out`/`done`**（依赖 `stop` 广播，**与是否曾有事件无关**）。**不提供**可选策略开关。`Close()` 幂等、可并发、不阻塞。

  **必测**：
  - **空闲订阅者直接 `Close()`（按 A9 要求）**：订阅后**不发布任何事件**，直接 `Close()` → 断言 ① `C()` 在限定时间内关闭（`for range` 能退出，不挂起）② `Done()` 关闭 ③ `Err()` 非 nil 且 `reason == "closed"` ④ 无 goroutine 泄漏（关闭前后 goroutine 计数对比，或以超时断言兜底）。
  - **`Close()` 与终止并发**：在 stall / overflow 已触发（`stop` 已关闭）后再调用 `Close()` → 幂等、不 panic、不重复 close；且 `reason` 保持首次置位值。
  - **完全不消费 + 投递 ≥ `outCap` 个普通事件**（按 A8 要求）：断言 ① 普通事件占用 ≤ `outCap-1` ② 终止事件**可立即写入**（用 `select`+`default` 断言可即时入队）③ `Err()` 非 nil 且 `Done()` 已关闭 ④ `publish` 全程未阻塞（在 `maxBacklogEvents` 内）。
  - **消费恢复**：消费者恢复读取后普通事件继续投递，且不误触发 stall，`stallDeadline` 被正确重置。
  - **多订阅者中仅 1 个慢**：其他订阅者投递延迟不受影响。
  - **`publish` 延迟不随饱和时长增长**：饱和前/后 p99 差值在阈值内。
  - **backlog 溢出**：`reason == "overflow"` 且 `lostCount > 0`，且溢出后 goroutine 亦能及时退出。
  - `-race` 下并发 `Close`/`publish`/转发 goroutine 无竞态。

### 5.2 filesystem（9 方法 + 1 通知，当前 0）

- `fs/readFile`、`fs/writeFile`、`fs/createDirectory`、`fs/getMetadata`、`fs/readDirectory`、`fs/remove`、`fs/copy`、`fs/watch`、`fs/unwatch`；通知 `fs/changed`。
- `fs/readFile` 返回 base64 → 提供 `ReadFileBytes()`。
- `fs/watch` 有状态：`Client.Close()`/thread 结束时自动 `fs/unwatch`；`fs/changed` 按 watch id 路由。
- 上游：`protocol/v2/fs.rs`。

### 5.3 MCP lifecycle（5 方法 + 1 ServerRequest + 2 通知，当前 0）

- Client 方法：`mcpServer/oauth/login`、`config/mcpServer/reload`、`mcpServerStatus/list`、`mcpServer/resource/read`、`mcpServer/tool/call`
- ServerRequest：`mcpServer/elicitation/request`
- 通知：`mcpServer/oauthLogin/completed`、`mcpServer/startupStatus/updated`
- T2.8：新增 `McpElicitationHandler`；**删除** `item/mcp/requestApproval`（`envelope.go:46`、`interaction.go:164,218`、`decode.go:34`、`sdk_v2_test.go:2200`）——按 R3 不留旧名。
- 上游：`protocol/v2/mcp.rs`。

### 5.4 remote control — **按 R2 不在 scope（已确认放弃）** ❌

`remoteControl/*` 7 个方法全部 experimental（`common.rs:1155-1193`）。保留 `RemoteControlStatusChangedEvent` 通知类型，文档标注 RPC 面不提供；不保留恢复例外。

### 5.5 plugins / marketplace（15 方法，当前 0）

- `marketplace/`(3)：`add`、`remove`、`upgrade`
- `plugin/`(12)：`list`、`installed`、`reconcile`、`read`、`skill/read`、`share/save`、`share/updateTargets`、`share/list`、`share/checkout`、`share/delete`、`install`、`uninstall`
- ⚠️ 标注 `serialization: global("config")` 的变体（`plugin/install|uninstall`、`marketplace/*`、`plugin/share/*`）是**全局配置级变更**，服务端串行化（`ClientRequestSerializationScope`，`common.rs:129-206`）；文档需说明并发调用会被排队。
- 先只读、后写路径。

### 5.6 其它 stable 缺口（按交付顺序，**全量交付**）

> ⚠️ 修订 A5：本节方法**全部**须在最终交付完成。P1/P2/P3 只表示**交付顺序**。

| 顺序 | 组 | 数量 | 说明 |
|---|---|---:|---|
| P1 | `thread/turns/list`、`thread/items/list` | 2 | reconnect 回填必需 |
| P1 | `account/chatgptAuthTokens/refresh` | 1 | ServerRequest；不实现则长会话认证过期无法恢复 |
| P1 | `mcpServer/elicitation/request` | 1 | 见 §5.3 |
| P1 | `account/*` stable 补齐 | 8 | 12 stable − 4 已实现 |
| P2 | `thread/attachment/*` | 4 | + 通知 `thread/attachment/updated` |
| P2 | `threadSection/*` | 4 | `list/create/update/delete` |
| P2 | `app/*` | 3 | + 通知 `app/list/updated` |
| P2 | `thread/revert` | 1 | |
| P3 | `permissionProfile/list`、`configRequirements/read` | 2 | |
| P3 | `feedback/upload` | 1 | |
| P3 | `externalAgentConfig/*` | 4 | `detect/import/import/recordHistory/import/readHistories` |
| P3 | `windowsSandbox/*` | 2 | `setupStart`、`readiness` |
| P3 | `fuzzyFileSearch` | 1 | 仅基础方法 |
| — | 通知侧 | 达成 **61** | 按 §5.7 分类实现到 61 项（现有 65 typed 基于旧 schema，须重新基线化） |

**验收**：上表全部实现、接线、测试并登记；通知达成 61 项。**唯一允许未实现者为 §5.7 白名单（8 项）。**

### 5.7 不实现基线（**8 项**，CI 白名单）

> **范围澄清**：白名单只收录**「`declared_stable` 内但本期不实现」**的方法（8 项）。experimental 89 项**因不是 stable 而不在 scope**，属另一类，**不计入白名单**（否则会在门禁中对非 scope 项做无意义扣除）。

**白名单 A — v1 deprecated（R4，ClientRequest 3 项）**：`getAuthStatus`、`getConversationSummary`、`gitDiffToRemote`
（注：这 3 项**也被导出排除**，见 §2.2；它们仍属 `declared_stable`，故需登记。）

**白名单 B — R4 server-initiated（3 项）**：

| 方法 | 处置 |
|---|---|
| `applyPatchApproval` | 跳过审批流程；**入站按 §5.8 回 `{"decision":"denied"}`**；需常量 + `ReviewDecision` 响应类型 |
| `execCommandApproval` | 同上 |
| `attestation/generate` | D5b 默认不宣告 `requestAttestation`；**入站按 §5.8 回 `-32601` + 记录** |

**白名单 C — internal-only 通知（2 项）**：`rawResponse/completed`、`rawResponseItem/completed`
（源码存在但**导出排除**，不对客户端暴露；不实现解码器。）

**非 scope 集（另一类，登记于 `gen/not-in-scope.txt`）**：experimental 89 项 = 65 ClientRequest + 1 ServerRequest（`currentTime/read`）+ 23 通知。依据：源码 `#[experimental("...")]` 标注。

### 5.8 未实现 / 未配置的 server-initiated request 策略（T2.13）

**目标**：不实现某个 server→client 请求**不得终止会话/轮次**。对外是正常协议应答，对内留下可观测记录。

**现状问题**：`Dispatcher.HandleServerRequest` default 返回 `protocol.ErrUnsupportedServerRequest`（`interaction.go:232-237`）→ `requestLoop` 回 JSON-RPC error `-32603`（`client.go:574-576`）→ 服务端可能终止轮次。

| 情形 | 对外应答 | 对内 |
|---|---|---|
| 已知审批类但未配置 handler（含白名单 B 的两个 legacy，及 `d.Exec/File/MCP == nil`） | **协议有效的 deny/decline**，不发 JSON-RPC error | 记录 + 事件 |
| 未知方法（无法构造有效响应体） | `-32601 Method not found` | 记录 + 事件 |
| 已实现且 handler 正常 | 正常应答 | — |

- **legacy 两方法**：响应为 `{ decision: ReviewDecision }`（上游 `protocol/v1.rs:156-180`）。取 **`"denied"`**，其语义即 *"…should not execute it, **but it should continue the session and try something else**"*。
  - ⚠️ v1 `ReviewDecision` 是 **snake_case**（`approved`/`approved_for_session`/`denied`/`timed_out`/`abort` + 两个带载荷对象），**不可**复用 v2 的 `accept`/`decline`/`cancel`。
  - ⚠️ **不可**用 `"abort"`（会中止轮次）。
- **可观测性**：`UnhandledServerRequestEvent{Method, ThreadID, TurnID, Action, Reason}` 经 `client.Events()` 下发 + 写日志。
- **不提供**策略开关。
- **回归测试**：未配置 handler 的 `applyPatchApproval` 入站 → 断言 ① 回包 `{"decision":"denied"}` ② 无 JSON-RPC error ③ 轮次/会话继续 ④ 发出事件。

---

## 6. WS3 — 更新 README.md、样例与文档

- **T3.1 README**：覆盖率表**自动生成**（`implemented-methods.json` 对 `method-surface.json`），表头注明"对齐 codex `14c8b777`，scope=stable"；修正 D6 版本不一致；声明三条策略（R2/R3/R4）并链接 `gen/not-implemented.txt` 与 `gen/not-in-scope.txt`；补"上游不使用 `jsonrpc`"；新增 Reliability 小节（reconnect 边界 + 事件投递语义与 `Err()`/`Done()` 用法）。
- **T3.2 样例**：`reconnect-supervisor/`、`approval-over-websocket/`、`fs-and-mcp/`、`streaming-to-sse/`（展示 `Err()`/`Done()` 与背压下的正确消费）。
- **T3.3 文档**：`docs/index.md`（reconnect + 投递语义 + transport 表）、`docs/api-reference.md`（新方法/类型）、`llms.txt`+`llms-full.txt`（修 D7、补清单、标注基线）、新增 `docs/reconnect.md`；清理过时的 `PHASE3_REVIEW.md`。

**验收**：样例 `go build` 通过并被 CI 编译；README 数字与 conformance report 一致。

---

## 7. WS4 — 自动化验收与 CI 防回归

- **T4.1** `make sync`：T0.1 + T0.4 六条断言。
- **T4.2** `make conformance`：T1.1 产出全部 `gen/*` 文件。
- **T4.3** CI 门禁：
  - `declared_stable − whitelist − implemented ≠ ∅` → 失败（**只查实现集，不查生成常量**）。
  - T1.1 三条防造假校验任一失败 → 失败。
  - T0.4 六条对账断言任一失败 → 失败并要求人工复核（防上游导出语义变化被静默吸收）。
  - `gen/export-exclusions.json` 或白名单变化 → 提示复核。
  - `gen/conformance-report.md` 不新鲜 → 失败。
- **T4.4** 单测：D2/D4/D5 回归；reconnect 断连-恢复；**背压与退出专项**（含"完全不消费 + ≥ outCap 普通事件"、"**订阅后不发布任何事件直接 Close**"、"Close 与 stall/overflow 并发幂等"、`publish` 延迟不随饱和增长、goroutine 泄漏检查、`-race`）；§5.8 decline 回归。
- **T4.5** 真实环境：`tests/real/` 补 reconnect、fs、mcp，以及"移除 `jsonrpc` 后仍可用"。

---

## 8. 里程碑与交付顺序

> ⚠️ **里程碑是交付顺序，不是部分范围。** 每个里程碑的退出条件是**对应方法集 100% 完成并登记**；不存在"P2 只做 60%"。

| 里程碑 | 交付集（全部完成才退出） | 依赖 | 预估 |
|---|---|---|---|
| M0 | WS0：同步基础设施 + T0.4 六条断言 + T1.1/T1.1b 工具与 43 项审计 | — | 中 |
| M1 | WS1：T1.2/T1.3/T1.4 全部修复与对齐 | M0 | 中 |
| M2 | §5.2 fs（9+1） + §5.3 mcp（5+1+2） | M1 | 中 |
| M3 | §5.1 reconnect（T2.1–T2.6） + §5.6 P1（12） | M1 | **大** |
| M4 | §5.5 plugins/marketplace（15） | M1 | 中 |
| M5 | §5.6 P2+P3（15） + 通知侧 61 目标 | M2/M4 | 中 |
| M6 | WS3 文档/样例 + WS4 CI | 与 M2–M5 并行 | 小中 |

顺序：**M0 → M1 → M2（fs/mcp）→ M4（plugins）→ M3（reconnect）→ M5 → M6**。

---

## 9. 风险与取舍

| 风险 | 说明 | 缓解 |
|---|---|---|
| R1 无版本锚点 | 上游 `0.0.0` | 以 commit(`14c8b777`)+sha256 为准 |
| R2 experimental 不在 scope | ✅已决策。副作用：remote control 整组不可用 | `gen/not-in-scope.txt` 留档；变化时提示 |
| R3 不兼容旧名 | ✅已决策。breaking change | CHANGELOG + README 标注；发 major |
| R4 白名单 8 项 | ✅已决策 | 白名单显式登记 + 理由 |
| R9 未实现请求降级 | ✅已决策。未知方法只能回 `-32601` | decline 路径 + 事件留痕 |
| **R10 Scope 权威错位**（已修） | 上一版误以导出为权威 → 会把"导出排除的 3 项"当成缺口、并把通知范围算错 | Scope 改由源码 non-experimental 定义；导出仅对账 |
| **R11 差集漂移** | 导出排除集或通知过滤行为可能变化 | T0.4 断言 1/4/6 把该事实显式化，变化即失败并要求人工复核 |
| **R12 预留槽位失真**（已修） | 仅声明"预留"而用直接 `select` 写会填满通道 | 用 `len(out)` 守卫把普通事件限到 `outCap-1`；专项测试覆盖"完全不消费" |
| R5 codegen 成本 | 270+ 类型文件 | 先只生成方法名常量 + 缺口组类型 |
| R6 reconnect 不透明 | 上游无重放，审批必丢 | 文档定位"至少一次 + 应用层幂等" |

---

## 10. 验收标准（Definition of Done）

1. `bash scripts/sync-codex-schema.sh <codex>` 幂等；`.codex-schema/manifest.json` 记录 `14c8b777` 与 sha256；`version.go` 由生成器书写。
2. 不存在人工裁剪的 `v2.schema.json`。
3. `gen/method-surface.json`、`gen/export-exclusions.json`（5 项）、`gen/not-in-scope.txt`（89 项）、`gen/not-implemented.txt`（8 项）齐备；**T0.4 六条集合断言全绿**。
4. `declared_stable − whitelist(8) − implemented == ∅`，且 T1.1 三条防造假校验通过（**覆盖判定基于实际接线，不基于生成常量**）；**kind 覆盖四个 face 且 face↔kind 满射**（`initialized` 以 `client_notification_sender` 合法登记）；T1.1b 的 43 项审计完成且遗留项已按 R2/R3 清理。
5. **D1–D7 全部修复**，按 R3 不保留旧名/兼容分支（含 `item/mcp/requestApproval`、`jsonrpc` 字段）。
6. reconnect：断开后自动完成 重拨 → initialize → thread/resume → 断连期结果回填；有自动化测试。
7. **事件投递语义达标**：`publish` 永不阻塞（饱和专项测试 + 量化断言）；普通事件占用 ≤ `outCap-1`，终止事件靠预留槽位**立即写入**；`Err()`/`Done()` 必达；**`Close()` 后转发 goroutine 必定退出**（含"空闲订阅者直接 Close"与"Close 与终止并发"测试，无 goroutine 泄漏）；无静默丢弃路径。
8. **缺口全量交付**：ClientRequest 62、ServerRequest 2、§5.6 P1/P2/P3 全部、通知侧 61 目标 —— 均实现、接线、测试并登记。**唯一允许未实现者为白名单 8 项。**
9. 未实现的 server-initiated request 不终止会话（§5.8 回归测试通过）。
10. README/docs/llms/examples 更新完毕，样例可编译，覆盖率数字自动生成。
11. CI 全绿，且门禁能在协议漂移或导出语义变化时失败。

---

## 11. 决议记录（全部关闭）

| # | 议题 | 决议 |
|---|---|---|
| 1–6 | （第一轮）超时行为 / remote control 放弃 / v1 deprecated 跳过 / attestation 不实现 / 超时 5s / 静默 decline | 见 §0 决策记录 |
| 7 | 复审 A1：覆盖门禁假阳性 | 采纳：声明集/实现集分离 + 三条防造假校验 |
| 8 | 复审 A2：转发 goroutine 仍阻塞共享路径 | 采纳：`publish` 永不阻塞 |
| 9 | 复审 A3：终止事件无交付机制 | 采纳：带外 `Err()`/`Done()` + 预留槽位 |
| 10 | 复审 A4：stable 导出非纯 stable 集 | 采纳：Scope 改由源码分类定义 |
| 11 | 复审 A5：阶段验收与 DoD 冲突 | 采纳：统一全量交付 |
| 12 | 复审 A6：T0.4 断言在当前基线即失败；5 项导出排除 | 采纳：登记 5 项；断言改**集合相等**；**撤回"计数错误"结论**（R11） |
| 13 | 复审 A7：缺口 59 重复扣除 legacy | 采纳：集合差计算 → 缺口 **62**；新增 T1.1b 审计 43 项（R10） |
| 14 | 复审 A8：转发伪代码未落实槽位预留 | 采纳：`len(out)` 守卫 + 消费进展等待 + "完全不消费"测试（R12） |
| 15 | 复审 A9：空闲订阅者 `Close()` 可能永不退出 | 采纳：一次性 `stop` 广播 + 两个等待点 select `stop` + 退出责任划分；补"空闲直接 Close"测试（R13） |
| 16 | 复审 A10：登记遗漏 ClientNotification 发送路径 | 采纳：新增 `client_notification_sender` + face↔kind 满射校验（R14） |
| 17 | 实施 I1：锚点被误设为 CLI | 采纳：锚点 = **codex 仓库 commit**；CLI 仅作 `diff-cli` 漂移守卫（实测 0.160.0 落后 1 个方法 + 1 个通知） |
| 18 | 实施 I2：`GeneratedAt` 破坏幂等 | 采纳：`version.go` 不含时间戳，仅确定性值 |
| 19 | 实施 I3：vendor 对象与理由证据 | 采纳：vendor 聚合 stable schema + 派生方法集；internal-only 排除理由已有上游注释背书 |
| 20 | 实施 I5 的 `item/updated` | 采纳：删除该通知类型与解码分支，测试改为断言回退 `RawNotificationEvent` |
| 21 | **`turn/diff` 的处置（用户决策）** | JSON-RPC 无 `turn/diff` 方法 → **SDK 侧不实现**。删除 `Client.TurnDiff`/`SessionThread.GitDiff`/`TurnDiffRequest`/`TurnDiffResult`/`MethodTurnDiff` 及相关文档引用；保留 `TurnDiffUpdatedEvent` 通知 |

> 无遗留开放项。

---

## 附录 A：Scope 摘要（唯一机器可读来源为 `gen/*.json`）

> 本附录不再手工列举方法清单 —— 手工清单已多次引入偏差（A4/A6）。**权威为 T0.4 生成的 `gen/method-surface.json` + `gen/export-exclusions.json`。**

| 面 | 源码总数 | experimental（非 scope） | declared_stable | 白名单 | 已实现 | **待实现** |
|---|---:|---:|---:|---:|---:|---:|
| ClientRequest | 173 | 65 | 108 | 3 | 43 | **62** |
| ServerRequest | 11 | 1 | 10 | 3 | 5 | **2** |
| ServerNotification | 86 | 23 | 63 | 2 | 待重基线 | 达成 **61** 目标 |
| ClientNotification | 1 | 0 | 1 | 0 | 1 | 0 |
| **合计** | **271** | **89** | **182** | **8** | — | — |

一致性：`173−65=108`；`108−3−43=62`；`11−1=10`；`10−3−5=2`；`86−23=63`；`63−2=61`；`84(导出 stable 通知)=63+23−2`；`105(导出 stable client)=108−3`；`170−105=65`（= 源码 experimental）。

---

## 附录 B：证据索引

| 结论 | 证据路径 / 命令 |
|---|---|
| 无协议版本号 | `codex-rs/Cargo.toml:163`、`codex-rs/app-server-protocol/src/rpc.rs:11` |
| 不使用 `jsonrpc` 字段 | `codex-rs/app-server-protocol/src/rpc.rs:1-2,45-79` |
| **源码方法面**（173 / 11 / 86 / 1） | `codex-rs/app-server-protocol/src/protocol/common.rs:499-1510,1780-1851,1935-2061,2082-2084` |
| **导出方法面**（105/170、10/11、84/84、1/1） | 解压 `schema/precomputed/app-server-exports-{stable,experimental}.json.zst` → `json_schema[*]["method"].enum` |
| **导出排除 5 项** | 上述源码集 − 导出（experimental）集；5 项在两份导出中均不存在（已复核） |
| 通知未被 experimental 过滤 | `导出_stable(ServerNotification) == 导出_experimental(ServerNotification)`（集合相等） |
| experimental 89 项 | `grep -o 'experimental("\([^"]*\)")' common.rs \| sort`（89 = 65 + 1 + 23） |
| `remoteControl/*` 全 exp | `common.rs:1155-1193` |
| `mcpServer/event/stream/*` exp | `common.rs:1257-1266`、`2001-2002` |
| `plugin/search` exp | `common.rs:916-917` |
| `InitializeCapabilities` 字段 | `codex-rs/app-server-protocol/src/protocol/v1.rs:29-70` |
| `ReviewDecision`（v1，snake_case） | `v1.rs:156-180` + `.codex-schema/ApplyPatchApprovalResponse.json:100-119` |
| 通知信封 `emittedAtMs` | `common.rs:2063-2080` |
| 无 HTTP/SSE、仅 stdio/unix/ws | `codex-rs/app-server-transport/src/transport/mod.rs:80-166` |
| WS 帧级 ping | `codex-rs/app-server-transport/src/transport/websocket.rs:363-373` |
| 订阅按连接 | `codex-rs/app-server/src/thread_state.rs:344-348,433`；`thread_processor.rs:3605` |
| 无客户端事件重放 | `codex-rs/app-server/src/transport.rs:204-243` |
| `thread/resume` = 重放替代 | `codex-rs/app-server/src/thread_state.rs:59-64` |
| precomputed 解压分发 / fixtures | `precomputed_exports.rs:15-18`；`schema_fixtures.rs:95-153` |
| schema 生成 CLI / `--experimental` | `codex-rs/cli/src/main.rs:727-735,1421-1425` |
| SDK 发送 `jsonrpc` | `internal/transport/transport.go`（`requestEnvelope`/`notificationEnvelope`） |
| SDK pinned 子集 schema | `internal/protocol/schema/v2.schema.json`、`version.go:3-10` |
| SDK 方法常量（**不可作覆盖依据**） | `internal/protocol/envelope.go:12-168` |
| SDK 旧 MCP 审批方法 | `internal/protocol/envelope.go:46`、`interaction.go:164,218`、`decode.go:34` |
| SDK dispatcher 默认错误分支 | `interaction.go:232-237`、`client.go:574-576` |
| SDK 背压静默丢事件（且在持锁遍历中） | `events.go:370-385` |
| SDK `ErrClosed` 不可重试 | `internal/transport/retry.go:125-127` |
| SDK 同步脚本路径失效 | `scripts/update-codex-go-schema.sh:6-7` |
| 文档不一致 | `llms.txt:2-5`、`README.md:9,40,106`、`VERSION` |
