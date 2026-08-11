# OCR 原生 P4 实现记录

## 2026-08-11：建立新基线

### 执行流

1. 从官方 OCR 提交 `200424c88ae45d2a51eec7388cd29344f54d4fbd` 建立分支 `codex/native-p4`。
2. 固化基线 tree hash：`071debe624aa2740dfa84e1c1e3c8f89f49b5e7e`。
3. 新实现只面向 submitted CL 的 `add/edit`，不复用或迁移旧 Eval、94-case、188-run、镜像和证据。
4. 先建立 source-aware 合同和 Git adapter 回归，再接 P4 source socket；通过关键路径后才允许删除迁移期 Git/MCP 对照链。

### 取舍

- “原生 P4”只表示 OCR 不依赖 Git 源码副本，并统一通过 `ReviewSource` / `SourceView` 消费输入；P4 凭据、执行预算和 canonical ledger 仍归 Claw node。
- v1 保持 shadow-only；P4 模式支持 preview，禁止 from/to/commit/resume。
- P4 模式不自动发现 repo-local rules，只接受内置 rules 和显式 hash-bound rule artifact。
- 不 cherry-pick `F:\X6Agent\open-code-review` 的旧 P4 原型，只参考其分层边界。

### Deviations

- 暂无。

## 2026-08-11：non-Git preview 与关键回归

### 执行流

1. 增加真实 Unix socket fake source 集成测试：从无 `.git` 的 runtime root 执行 P4 preview，覆盖 resolve、receipt、source identity，并确认运行后仍无 `.git`。
2. source flags 在 Git 模式下不再静默忽略；Git source 携带 `--source-manifest/--source-socket` 直接失败。
3. `internal/reviewsource`、command 关键路径、vet 与 Windows 本机构建通过。

### 取舍

- 本轮不启动旧 Formal Eval，也不把 preview 测试冒充真实模型或真实 P4 canary。

### Deviations

- 本机 Go 是 32 位 `GOARCH=386`；既有 `internal/session` 测试在 `history.go` 的 64 位 atomic 上触发 `unaligned 64-bit atomic operation`。保守跳过该既有整包阻塞，保留新增包、command 关键路径和实际 binary build 验证；Linux amd64 合并门禁仍必须重跑整包。
- 追加 `GOARCH=amd64` 复核后，新增 P4 包通过，但 Windows 下既有 session/config 测试仍受目录权限、绝对路径和 chmod 语义影响失败；这进一步确认本地 Windows 不能替代计划中的 Linux 合并门禁。
- 为满足“Git 输出不变”，本轮没有强迫现有 Git diff/FileReader/search/find 全部改走新的 `ReviewSource` 接口；Git 仍沿原执行链，P4 走同一 Agent 入口下的 native `ReviewSource`。直接包装 Git 会扩大回归面且不决定 P4 去 Git 化，保守留到独立重构；这不影响 P4 diff/read/find/search 共用同一个 source instance。

## 旧资产只读锚点

- 旧 Formal Eval 仓：`H:\aiDOC\ocr-claw-cr-eval`。
- 只读 commit：`0f0dab53e78fce5ed23e7f19677edafe763d7905`。
- 只读 tree hash：`f2741b02636d6d31e10fcfb6bf3531e6976683f8`。
- 该仓当前存在用户未提交改动；本分支不读取其运行产物、不修改、不清理。

## 2026-08-11：收紧 native P4 动态上下文

### 执行流

1. `file_find` 只接受带固定目录前缀的相对查询，拒绝 depot 根、裸 `...`、无目录前缀和路径通配符。
2. 严格解析 source service 返回的最多 100 个路径、逐项 exact `#rev` 和显式 `truncated`。
3. 对 changed path 的 read/search 同时核对 manifest head spec 与 SHA-256，不再只比较 depot/path 前缀。
4. 工具输出固定包含 `TRUNCATED: true|false`，避免模型把不完整枚举当完整结果。

### 取舍

- unchanged path 没有预先登记的 manifest 内容 hash，OCR 只能校验 exact depot/path#rev 和合法 hash；其字节 hash 由 node 在 exact print 后生成并写入 canonical ledger。

### Deviations

- 暂无。
