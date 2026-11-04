/** 仅允许 http(s) 链接，阻止 javascript:/data: 等危险协议。 */
export function safeHttpUrl(value) {
  const raw = String(value || '').trim();
  if (!raw) return '';
  try {
    const u = new URL(raw, window.location.origin);
    if (u.protocol === 'http:' || u.protocol === 'https:') return u.toString();
  } catch (e) {
    /* ignore */
  }
  return '';
}