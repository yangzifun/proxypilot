# Agent 1.3 发布说明（节点侧 · PilotLink（私有协议）UDP 下行修复）

- 版本：Agent `1.3`（`agent/internal/agent/agent.go` → `const Version = "1.3"`）
- 日期：2026-11-16
- 范围：仅 Agent（节点）侧；客户端 1.3 由客户端任务并行发布；面板/服务端不由本说明覆盖
- 状态：**已构建、未部署、未上传 R2**（由 Lead 统一集成发布）

## 一、修复的问题

线上节点用户出现「`ping 1.1.1.1` 通，但 DNS / 域名全部超时」：

- 根因（详见 `myworkspace/reports/vpn-dns-diagnosis.md`）：Agent 二进制用
  `agent/vendor` 里的**陈旧 vendored `tunnel` 模块**构建。旧 tunnel 节点：
  1. `GET /` 无条件注册 DATAGRAM 会话，不做 `H3-Datagram` 能力协商；
  2. 客户端未协商 DATAGRAM 时，节点仍把 **UDP（含 DNS）下行**写进客户端不读的
     QUIC DATAGRAM 通道，且发送失败无回退；
  3. 没有 `单连接双通道` 与 DNS 强制走可靠流的逻辑。
- 结果：UDP/DNS 下行被静默丢弃；TCP/ICMP 走可靠流，所以 ping 1.1.1.1 正常。

## 二、Agent 1.3 变更

1. **同步 `agent/vendor` 到工作区当前模块**（`cd agent && go mod vendor`）：
   - `tunnel/node` 更新为包含 `单连接双通道`、`H3-Datagram` 能力协商、
     `X-VPN-Lane` 多路通道、`已停用 DATAGRAM`（客户端未声明能力时走可靠流回退）、
     DNS 强制可靠流、FEC 等修复的版本。
   - 保留 vendor 供离线 / Windows 交叉构建；**不使用 `-mod=mod`**。
2. **新增 `agent/check-vendor.sh` 防复发守卫**：
   - 用 `go mod vendor -o` 复算 vendor 并与仓库 `agent/vendor/` 全量 diff
     （忽略行尾 CR，兼容 Windows `core.autocrlf`）；
   - 额外硬校验 vendor 的 `tunnel/node` 含「单连接双通道 / H3-Datagram /
     X-VPN-Lane / 已停用 DATAGRAM」修复符号；
   - 已接入 `agent/build.sh`：构建前自动执行，不一致直接中止并提示
     `cd agent && go mod vendor`。
3. **版本号 1.2 → 1.3**：`servers.agent_version` 随 poll 上报更新；
   节点 VPN 心跳（`POST /api/agent/vpn/heartbeat`，body `version`）会更新
   `vpn_nodes.version`（`UPDATE vpn_nodes SET last_seen=?, version=?`）。
4. 顺带修复既有 `agent/internal/runner/runner_test.go`：deploy 测试未注入
   `safeConfigPathFn` 临时目录白名单，导致 `go test ./...` 有 8 个用例必然失败；
   现已在 `newRunner` 中调用既有 `allowTempDeploy`，测试全绿（仅测试代码，无生产逻辑变更）。

## 三、构建

```bash
cd agent
./build.sh
# 产物：agent/dist/proxypilot-agent-linux-amd64 / -linux-arm64
#       agent/dist/SHA256SUMS / manifest.json（version v1.3）
```

构建脚本固定 `GOTOOLCHAIN=local`、`CGO_ENABLED=0`、`-trimpath -ldflags "-s -w"`。
构建前 vendored tunnel 必须与 `tunnel/` 一致（`check-vendor.sh` 自动把关）。

## 四、发布前验证清单

```bash
cd agent
bash check-vendor.sh                                  # OK
go build ./... && go vet ./... && go test ./...       # 全绿
grep -a -c '单连接双通道' dist/proxypilot-agent-linux-amd64
grep -a -c 'H3-Datagram' dist/proxypilot-agent-linux-amd64
grep -a -c 'X-VPN-Lane' dist/proxypilot-agent-linux-amd64
grep -a -c '已停用 DATAGRAM' dist/proxypilot-agent-linux-amd64
```

四组字符串都应 >= 1；旧 1.2 二进制为 0（本说明的区分依据）。

## 五、升级与回滚

- 升级：按既有 Agent 升级流程替换 `/usr/local/bin/proxypilot-agent` 并重启
  `proxypilot-agent`（sing-box 不动）；升级后面板 `servers.agent_version` 应为 1.3，
  VPN 节点心跳后 `vpn_nodes.version` 应为 1.3。
- 回滚：保留 1.2 旧二进制（sha256 `d36c9390…` / arm64 `c16c6c92…`），
  覆盖回 `/usr/local/bin/proxypilot-agent` 并重启即可；面板/协议无需迁移。
- 客户端 1.3 需同步发布（客户端任务负责），两端版本建议同时为 1.3。