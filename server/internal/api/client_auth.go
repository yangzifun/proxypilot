package api

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"time"
)

// 客户端「网页登录」授权码流程：
//   1. 客户端 POST /api/client-auth/start    → 拿到 code 与授权页地址
//   2. 浏览器打开 /client-auth?code=...       → 邮箱验证码登录后点「授权」
//   3. 客户端轮询 /api/client-auth/poll        → 拿到门户令牌
// 授权码单次使用、10 分钟过期；令牌只在授权完成的那一次返回。

const clientAuthTTL = 10 * time.Minute

func newClientAuthCode() string {
	b := make([]byte, 24)
	_, _ = rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}

func (h *handlers) clientAuthPublicBase(r *http.Request) string {
	if base := strings.TrimSpace(h.cfg.PublicBaseURL); base != "" {
		return strings.TrimRight(base, "/")
	}
	scheme := "http"
	if r.TLS != nil || strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https") {
		scheme = "https"
	}
	return scheme + "://" + r.Host
}

// POST /api/client-auth/start {client_name}
func (h *handlers) clientAuthStart(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	var body struct {
		ClientName string `json:"client_name"`
	}
	_ = json.NewDecoder(io.LimitReader(r.Body, 4096)).Decode(&body)
	name := strings.TrimSpace(body.ClientName)
	if len(name) > 64 {
		name = name[:64]
	}
	code := newClientAuthCode()
	now := time.Now().UnixMilli()
	if _, err := h.db.ExecContext(r.Context(),
		`INSERT INTO client_auth_codes (code, status, token, email, client_name, created_at, expires_at)
		 VALUES (?, 'pending', '', '', ?, ?, ?)`,
		code, name, now, now+clientAuthTTL.Milliseconds()); err != nil {
		writeError(w, http.StatusInternalServerError, "创建授权码失败")
		return
	}
	authURL := h.clientAuthPublicBase(r) + "/client-auth?code=" + code
	writeJSON(w, http.StatusOK, map[string]any{
		"code":       code,
		"auth_url":   authURL,
		"expires_in": int(clientAuthTTL.Seconds()),
	})
}

// GET /api/client-auth/poll?code=
func (h *handlers) clientAuthPoll(w http.ResponseWriter, r *http.Request) {
	code := strings.TrimSpace(r.URL.Query().Get("code"))
	if code == "" {
		writeError(w, http.StatusBadRequest, "缺少 code")
		return
	}
	var status, token, email string
	var expires int64
	err := h.db.QueryRowContext(r.Context(),
		`SELECT status, token, email, expires_at FROM client_auth_codes WHERE code = ?`, code).
		Scan(&status, &token, &email, &expires)
	if err != nil {
		writeError(w, http.StatusNotFound, "授权码不存在")
		return
	}
	if status == "pending" && time.Now().UnixMilli() > expires {
		status = "expired"
	}
	out := map[string]any{"status": status, "expires_at": expires}
	if status == "approved" {
		out["token"] = token
		out["email"] = email
	}
	writeJSON(w, http.StatusOK, out)
}

// POST /api/client-auth/approve {code}  —— 需要门户令牌（邮箱验证码登录后拿到）
func (h *handlers) clientAuthApprove(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	email, ok := h.portalSession(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "请先完成邮箱登录")
		return
	}
	var body struct {
		Code string `json:"code"`
	}
	_ = json.NewDecoder(io.LimitReader(r.Body, 4096)).Decode(&body)
	code := strings.TrimSpace(body.Code)
	if code == "" {
		writeError(w, http.StatusBadRequest, "缺少 code")
		return
	}
	raw := strings.TrimSpace(r.Header.Get("Authorization"))
	token := strings.TrimSpace(strings.TrimPrefix(raw, "Bearer "))
	now := time.Now().UnixMilli()
	res, err := h.db.ExecContext(r.Context(),
		`UPDATE client_auth_codes SET status = 'approved', token = ?, email = ?, approved_at = ?
		  WHERE code = ? AND status = 'pending' AND expires_at > ?`,
		token, email, now, code, now)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "授权失败")
		return
	}
	if n, _ := res.RowsAffected(); n == 0 {
		writeError(w, http.StatusBadRequest, "授权码无效或已过期")
		return
	}
	h.log.Info("客户端网页登录授权", "email", email, "code", code[:8]+"…")
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "email": email})
}

// GET /client-auth?code=...  —— 独立授权页（不依赖管理端 SPA）
func (h *handlers) clientAuthPage(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	code := strings.TrimSpace(r.URL.Query().Get("code"))
	page := strings.ReplaceAll(clientAuthHTML, "__CODE__", code)
	_, _ = w.Write([]byte(page))
}

const clientAuthHTML = `<!doctype html><html lang="zh-CN"><head><meta charset="utf-8">
<meta name="viewport" content="width=device-width,initial-scale=1">
<title>授权登录 · ProxyPilot</title>
<style>
*{box-sizing:border-box}
body{margin:0;min-height:100vh;display:flex;align-items:center;justify-content:center;
 background:radial-gradient(120% 70% at 50% -10%,rgba(56,189,248,.14),transparent 60%),#0a0c10;
 color:#e9eef5;font-family:system-ui,-apple-system,"Segoe UI",sans-serif}
.card{width:min(420px,92vw);background:#12161c;border:1px solid #1f252e;border-radius:18px;padding:26px}
.brand{display:flex;align-items:center;gap:10px;margin-bottom:20px}
.logo{width:30px;height:30px;border-radius:9px;display:grid;place-items:center;background:rgba(56,189,248,.1);
 border:1px solid rgba(56,189,248,.3);color:#38bdf8;font-size:15px}
h1{font-size:15px;margin:0}
p{font-size:12.5px;color:#8b98a9;line-height:1.6;margin:0 0 16px}
label{display:block;font-size:11px;color:#5c6878;margin:12px 0 6px}
input{width:100%;padding:11px 13px;border-radius:10px;border:1px solid #1f252e;background:#0e1218;color:#e9eef5;
 font-family:ui-monospace,Consolas,monospace;font-size:13px}
input:focus{outline:none;border-color:rgba(56,189,248,.55)}
button{width:100%;margin-top:16px;padding:12px;border-radius:10px;border:1px solid #38bdf8;background:#38bdf8;
 color:#04121c;font-size:13px;font-weight:700;cursor:pointer}
button.ghost{background:#12161c;border-color:#1f252e;color:#e9eef5;font-weight:600}
button:disabled{opacity:.55;cursor:not-allowed}
.msg{margin-top:14px;font-size:12.5px;line-height:1.6;min-height:18px}
.ok{color:#34d399}.err{color:#fb7185}
.code{font-family:ui-monospace,Consolas,monospace;color:#38bdf8}
.hide{display:none}
</style></head><body><div class="card">
<div class="brand"><div class="logo">◈</div><h1>ProxyPilot 客户端授权</h1></div>
<p>用你的订阅邮箱完成验证，即可授权这台设备登录客户端。授权码 <span class="code" id="code">__CODE__</span></p>

<div id="step1">
  <label>邮箱</label>
  <input id="email" placeholder="订阅所属邮箱" autocomplete="email">
  <button id="btnSend">发送验证码</button>
</div>

<div id="step2" class="hide">
  <label>6 位验证码</label>
  <input id="otp" placeholder="邮件里的 6 位数字" inputmode="numeric" maxlength="6">
  <button id="btnLogin">登录并授权</button>
</div>

<div class="msg" id="msg"></div>
</div>
<script>
const CODE = document.getElementById('code').textContent.trim();
const $ = id => document.getElementById(id);
function say(t, cls){ const m=$('msg'); m.className='msg '+(cls||''); m.textContent=t; }
async function post(url, body, token){
  const r = await fetch(url, {method:'POST', headers: Object.assign({'Content-Type':'application/json'}, token?{Authorization:'Bearer '+token}:{}), body: JSON.stringify(body||{})});
  const d = await r.json().catch(()=>({}));
  if(!r.ok) throw new Error(d.error || ('HTTP '+r.status));
  return d;
}
$('btnSend').onclick = async () => {
  const email = $('email').value.trim();
  if(!email) return say('请填写邮箱','err');
  $('btnSend').disabled = true;
  try{
    await post('/api/portal/send-code', {email});
    say('验证码已发送，请查收邮件','ok');
    $('step2').classList.remove('hide');
  }catch(e){ say('发送失败：'+e.message,'err'); }
  $('btnSend').disabled = false;
};
$('btnLogin').onclick = async () => {
  const email = $('email').value.trim();
  const code = $('otp').value.trim();
  if(code.length !== 6) return say('请输入 6 位验证码','err');
  $('btnLogin').disabled = true;
  try{
    const login = await post('/api/portal/login', {email, code});
    await post('/api/client-auth/approve', {code: CODE}, login.token);
    document.querySelector('.card').innerHTML = '<div class="brand"><div class="logo">✓</div><h1>授权成功</h1></div>'
      + '<p>客户端已获得登录凭证，可以关闭本页面回到客户端了。</p>';
  }catch(e){ say('授权失败：'+e.message,'err'); $('btnLogin').disabled = false; }
};
</script></body></html>`
