# 自建直投邮件

不依赖任何第三方发信服务：面板直接解析收件方 MX，自己连 25 端口投递，并用面板内生成的
DKIM 私钥签名。管理员只需要填「发信域名」与「发件人邮箱」。

## 1. 工作方式

```
sendMail(to, subject, html)
   └─ provider=builtin
        ├─ buildMIMEMessage    组装 RFC 5322（CRLF、Q 编码中文主题）
        ├─ dkimSign            rsa-sha256，relaxed/relaxed，私钥存 app_settings（加密）
        ├─ resolveMX(to域)     LookupMX，无 MX 时按 RFC 5321 回落 A 记录
        └─ deliverSMTP(每个MX)  :25 → EHLO → 可选 STARTTLS → MAIL/RCPT/DATA
```

关键实现：

| 文件 | 职责 |
|---|---|
| `mail_dkim.go` | 生成 RSA-2048 密钥、DKIM 签名与规范化、DNS 指引与实时校验 |
| `mail_builtin.go` | MX 解析、MIME 组装、SMTP 直投 |
| `mail_builtin_api.go` | `GET /api/admin/mail/guide`、`POST /admin/mail/test`、`POST /admin/mail/dkim` |
| `mail_dns_apply.go` | `POST /api/admin/mail/dns/apply` —— 用 Cloudflare API 一键写记录 |

## 2. 设置项

| 键 | 说明 |
|---|---|
| `mail_provider` | 设为 `builtin` |
| `mail_domain` | 发信域名，如 `proxypilot.yangzifun.org` |
| `mail_from` | 发件人，必须属于该域名 |
| `mail_dkim_selector` | 默认 `pp`，对应 `pp._domainkey.<域名>` |
| `mail_dkim_private` | RSA 私钥 PEM（Secret，AES-GCM 存库） |
| `mail_egress_ip` | 可选，SPF 里的 `ip4:`；留空自动探测 |
| `mail_dns_token` | 可选，仅用于写 DNS 的 Cloudflare Token（Zone:Read + DNS:Edit） |

## 3. 必须配置的 DNS

| 类型 | 名称 | 值 |
|---|---|---|
| TXT | 域名本身 | `v=spf1 a mx ip4:<出口IP> ~all` |
| TXT | `pp._domainkey.<域名>` | `v=DKIM1; k=rsa; p=<公钥>` |
| TXT | `_dmarc.<域名>` | `v=DMARC1; p=none; rua=mailto:postmaster@<域名>` |
| PTR | 在 VPS 控制台设置 | 公网 IP → 域名 |

**填写注意**：网页表单里粘贴的值**不要带引号**。带内部分段引号是 BIND 文件格式，
Cloudflare 会报「记录内容的引用方式不正确」。需要 BIND 片段时用向导里的
「复制为 BIND 片段」（会自动按 255 字节分段加引号）。

## 4. 邮件送达要点

| 项 | 说明 |
|---|---|
| 出站 25 端口 | 部分云厂商默认封禁，需工单解封 |
| PTR | 反向解析必须是自己的域名（服务商默认值 `racknerd-xxxx` 不算） |
| SPF | 精确到出口 IP，写错是 softfail，DMARC 会升级为拒收 |
| DMARC | 从 `p=none` 起步，观察 1-2 周再收紧 |
| 预热 | 新 IP 前两周容易被判垃圾，控制发送量 |
| 退信 | 当前不解析 DSN，退信进 `postmaster@<域名>` 的邮箱需人工看 |

## 5. DKIM 正确性

签名写错时对端不会报错，只会静默判垃圾，所以必须有本地校验。
`mail_dkim_test.go` 里 `TestDKIMSignVerifiable` 会：

1. 生成密钥、签名一封样例邮件
2. 重新按 RFC 6376 计算正文摘要 `bh` 并比对
3. 用公钥 `rsa.VerifyPKCS1v15` 验签

任何一步失败都会让 CI 红，避免「签了但算错」。

## 6. Cloudflare 一键写入

`POST /api/admin/mail/dns/apply`：

```
找 zone（proxypilot.a.com → a.com → com 逐级试）
  → 查已有 TXT（同 name）
     ├─ 内容一致 → unchanged
     ├─ 存在但不同 → PUT 覆盖 → updated
     └─ 不存在 → POST 新建 → created
```

需要 Token 具备 `Zone:Read` + `DNS:Edit`。若统一 Token 只有 R2/Email 权限，
可在设置里单独填 `mail_dns_token`，不必放大原 Token 的权限范围。