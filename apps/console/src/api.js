import { ref } from 'vue';

export const csrf = ref('');
export async function api(path, options = {}) {
  const headers = { ...options.headers };
  if (options.body && !(options.body instanceof FormData)) headers['Content-Type'] = 'application/json';
  if (csrf.value) headers['X-CSRF-Token'] = csrf.value;
  const response = await fetch('/api/v1' + path, { ...options, credentials: 'same-origin', headers });
  let data;
  try { data = await response.json(); } catch { throw new Error('服务响应异常，请确认 API 正在运行'); }
  if (!response.ok) { const failure = new Error(data.error || '请求失败'); failure.code = data.code; failure.status = response.status; throw failure; }
  if (data.csrf) csrf.value = data.csrf;
  return data;
}
export function json(method, value) { return { method, body: JSON.stringify(value) }; }
export const imageURL = path => path?.startsWith('/media/') || path?.startsWith('/assets/') || !path?.startsWith('/') ? path : '/assets' + path;
