<script setup>
import { ref, computed, onMounted, onBeforeUnmount } from 'vue';
import { marked } from 'marked';
import DOMPurify from 'dompurify';
import Security from './Security.vue';
import { api, json, imageURL, csrf } from './api.js';

const user = ref(null);
const siteURL = ref('');
const publishedURL = computed(() => siteURL.value && editor.value?.slug && editor.value?.status === 'published' ? `${siteURL.value}/posts/${editor.value.slug.split('/').map(encodeURIComponent).join('/')}/` : '');
const heroURL = '/assets/hero/hero-1.jpg';
const initializing = ref(true);
const needsSetup = ref(false);
const credentials = ref({ email: '', password: '', name: '', token: '', otp: '' });
const needsOTP = ref(false);
const error = ref('');
const message = ref('');
const busy = ref(false);
const loading = ref(false);
const section = ref('posts');
const items = ref([]);
const page = ref(1);
const hasMore = ref(false);
const total = ref(0);
const query = ref('');
const editor = ref(null);
const original = ref('');
const settings = ref(null);
const tabs = [{ key: 'posts', name: '文章管理', symbol: '文' }, { key: 'notices', name: '公告管理', symbol: '信' }, { key: 'comments', name: '评论管理', symbol: '言' }, { key: 'users', name: '读者账号', symbol: '友' }, { key: 'smtp', name: '邮件服务', symbol: '邮' }, { key: 'storage', name: '图片存储', symbol: '图' }, { key: 'security', name: '账号安全', symbol: '锁' }];
const headings = { posts: ['文字，慢慢积累。', '从一个想法，到一篇值得收藏的笔记。'], notices: ['给读者的一封信。', '更新、近况，还有想告诉大家的事。'], comments: ['让交流保持友善。', '查看网站评论，及时处理不友善的内容。'], users: ['在这里，相遇。', '只有完成邮箱验证的读者才会出现在这里。'], smtp: ['让每一封，都送达。', '邮箱验证是注册的前置条件，未启用 SMTP 时关闭新用户注册。'], storage: ['给图像，一个家。', '本地目录、S3 兼容服务或 WebDAV，自由选择。'], security: ['安心地，记录日常。', '管理你自己的账号安全，不强制其他用户开启。'] };
const preview = computed(() => DOMPurify.sanitize(marked.parse(editor.value?.markdown || '')));
const dirty = computed(() => editor.value && JSON.stringify(editor.value) !== original.value);
const visibleItems = computed(() => section.value === 'posts' ? items.value : items.value.filter(item => JSON.stringify(item).toLowerCase().includes(query.value.toLowerCase())));
async function refreshUser() { user.value = (await api('/me')).user; }
async function initialize() {
  initializing.value = true; error.value = '';
  try { siteURL.value = (await api('/config')).site_url || ''; needsSetup.value = (await api('/setup')).required; if (!needsSetup.value) { try { await refreshUser(); if (user.value.role === 'admin') await load(); } catch (failure) { if (failure.status !== 401) throw failure; } } }
  catch (failure) { error.value = failure.message; }
  finally { initializing.value = false; }
}
async function signIn() {
  busy.value = true; error.value = '';
  try {
    const value = needsSetup.value ? { token: credentials.value.token, email: credentials.value.email, name: credentials.value.name, password: credentials.value.password } : { email: credentials.value.email, password: credentials.value.password, otp: credentials.value.otp, client: 'web' };
    user.value = (await api(needsSetup.value ? '/setup' : '/auth/login', json('POST', value))).user;
    credentials.value.password = ''; credentials.value.otp = ''; credentials.value.token = ''; needsSetup.value = false; needsOTP.value = false;
    if (user.value.role === 'admin') await load();
  } catch (failure) { if (failure.code === 'totp_required') needsOTP.value = true; error.value = failure.message; }
  finally { busy.value = false; }
}
async function signOut() {
  if (dirty.value && !confirm('尚有未保存的内容，确定退出？')) return;
  try { await api('/auth/logout', json('POST', {})); user.value = null; editor.value = null; settings.value = null; items.value = []; csrf.value = ''; }
  catch (failure) { error.value = failure.message; }
}
async function load() {
  loading.value = true; error.value = '';
  try {
    if (['smtp', 'storage'].includes(section.value)) settings.value = await api('/admin/settings');
    else if (section.value !== 'security') {
      const suffix = section.value === 'posts' ? `?page=${page.value}&q=${encodeURIComponent(query.value)}` : '';
      const data = await api('/admin/' + section.value + suffix);
      items.value = data[section.value]; hasMore.value = data.has_more || false; total.value = data.total || items.value.length;
    }
  } catch (failure) { error.value = failure.message; }
  finally { loading.value = false; }
}
function closeEditor() { if (dirty.value && !confirm('编辑尚未保存，确定放弃修改？')) return false; editor.value = null; return true; }
async function navigate(key) { if (!closeEditor()) return; section.value = key; query.value = ''; page.value = 1; error.value = ''; message.value = ''; await load(); }
function edit(item) {
  editor.value = item ? JSON.parse(JSON.stringify(item)) : section.value === 'posts' ? { title: '', slug: '', description: '', markdown: '', category: '随笔', tags: [], date: new Intl.DateTimeFormat('en-CA').format(new Date()), cover: '', status: 'draft', revision: 0 } : { title: '', text: '', status: 'draft', revision: 0 };
  original.value = JSON.stringify(editor.value); error.value = ''; message.value = ''; window.scrollTo({ top: 0 });
}
async function save(status) {
  busy.value = true; error.value = '';
  try {
    const value = { ...editor.value, status };
    await api('/admin/' + section.value + (value.id ? '/' + value.id : ''), json(value.id ? 'PUT' : 'POST', value));
    editor.value = null; await load(); message.value = status === 'published' ? (siteURL.value ? '已发布，网站立即生效。' : '已发布到 API，动态网站尚未启用。') : '草稿已保存，不在网站或公开 API 中展示。';
  } catch (failure) { error.value = failure.message; }
  finally { busy.value = false; }
}
async function remove(item) {
  const target = item.title || `${item.name}：${item.text}`;
  if (!confirm(`确定删除「${target.slice(0, 120)}」？\n这会影响网站内容，界面中不能撤销。`)) return;
  busy.value = true; error.value = '';
  try { await api(`/admin/${section.value}/${item.id}`, { method: 'DELETE' }); await load(); message.value = '删除已完成。'; }
  catch (failure) { error.value = failure.message; }
  finally { busy.value = false; }
}
async function saveSettings(type) {
  busy.value = true; error.value = ''; message.value = '';
  try { await api('/admin/settings/' + type, json('PUT', settings.value[type])); await load(); message.value = '配置已加密保存。建议再做一次连接测试。'; }
  catch (failure) { error.value = failure.message; }
  finally { busy.value = false; }
}
async function testSettings(type) {
  busy.value = true; error.value = ''; message.value = '';
  try { message.value = (await api('/admin/settings/' + type + '/test', json('POST', {}))).message; }
  catch (failure) { error.value = failure.message; }
  finally { busy.value = false; }
}
async function upload(event) {
  const file = event.target.files[0]; if (!file) return;
  busy.value = true; error.value = '';
  try { const body = new FormData(); body.append('file', file); editor.value.cover = (await api('/admin/media', { method: 'POST', body })).url; message.value = '封面已上传，保存文章后生效。'; }
  catch (failure) { error.value = failure.message; }
  finally { busy.value = false; event.target.value = ''; }
}
function beforeUnload(event) { if (dirty.value) { event.preventDefault(); event.returnValue = ''; } }
onMounted(() => { initialize(); window.addEventListener('beforeunload', beforeUnload); });
onBeforeUnmount(() => window.removeEventListener('beforeunload', beforeUnload));
</script>

<template>
  <a class="skip-link" href="#main">跳到工作区</a>
  <main v-if="initializing" class="loading-screen">正在打开 Kukie 工作台…</main>
  <main v-else-if="!user" class="auth-layout" id="main">
    <div class="auth-art"><img :src="heroURL" alt="Kukie 暖色插画"><div><span>KUKIE / CREATOR STUDIO</span><h1>把值得的，<br>写下来。</h1><p>一个写作的角落，也是一座小站的日常。</p></div></div>
    <section class="auth-content"><div class="brand"><span class="brand-icon">k·</span><strong>Kukie</strong><small>内容工作台</small></div><span class="eyebrow">FOR THE PERSON BEHIND THE WORDS</span><h2>{{ needsSetup ? '建立你的小站。' : '你好，创作者。' }}</h2><p class="help-text">{{ needsSetup ? '首次初始化需要服务器上的 setup-token，不存在默认账号。' : '使用管理员账号登录，开始今天的记录。' }}</p>
      <form @submit.prevent="signIn"><label v-if="needsSetup">初始化密钥<input v-model="credentials.token" required type="password" autocomplete="off" placeholder="读取 .local/kukie/setup-token"></label><label v-if="needsSetup">管理员昵称<input v-model="credentials.name" required minlength="2" maxlength="24" autocomplete="nickname"></label><label>邮箱<input v-model="credentials.email" type="email" required autocomplete="username"></label><label>密码<input v-model="credentials.password" type="password" required minlength="12" maxlength="128" :autocomplete="needsSetup ? 'new-password' : 'current-password'" placeholder="至少 12 位"></label><label v-if="needsOTP">两步验证码或恢复码<input v-model="credentials.otp" required autocomplete="one-time-code" autofocus></label><p v-if="error" class="form-error" role="alert">{{ error }}</p><button class="solid-button" :disabled="busy">{{ busy ? '正在验证' : needsSetup ? '创建管理员' : '登录工作台' }} ↗</button></form><small class="auth-footnote">Go API · SQLite 持久化 · 管理员权限校验</small>
    </section>
  </main>
  <main v-else-if="user.role !== 'admin'" class="loading-screen" id="main"><h1>这是创作者的工作台。</h1><p>当前账号是读者，不具备管理权限，请返回网站阅读。</p><button class="solid-button" @click="signOut">退出当前账号</button></main>
  <div v-else class="admin-shell">
    <aside class="sidebar"><div class="brand"><span class="brand-icon">k·</span><div><strong>Kukie</strong><small>内容工作台</small></div></div><span class="sidebar-label">小站的日常</span><nav aria-label="控制台导航"><button v-for="tab in tabs" :key="tab.key" :class="{ active: section === tab.key }" :aria-current="section === tab.key ? 'page' : undefined" @click="navigate(tab.key)"><span>{{ tab.symbol }}</span>{{ tab.name }}</button></nav><div class="sidebar-bottom"><span class="status-dot"></span> 真实数据工作区<p>API :8084<br>Console :8085</p><button @click="signOut">退出登录 ↗</button></div></aside>
    <div class="main-area"><header class="topbar"><div><span>创作者工作台</span><span>/</span><strong>{{ tabs.find(tab => tab.key === section)?.name }}</strong></div><button class="identity-button" @click="navigate('security')"><img v-if="user.avatar" :src="imageURL(user.avatar)" alt=""><span v-else class="avatar-letter">{{ user.name.slice(0, 1) }}</span><span>{{ user.name }}</span></button></header>
      <main id="main" class="workspace"><div class="workspace-heading"><div><span class="eyebrow">{{ editor ? 'A WORK IN PROGRESS' : 'KUKIE / CREATOR STUDIO' }}</span><h1>{{ editor ? editor.id ? '继续写好这篇记录。' : '新的一篇记录。' : headings[section][0] }}</h1><p>{{ editor ? '保存草稿仅自己可见，发布后网站立即更新。' : headings[section][1] }}</p></div><button v-if="!editor && ['posts', 'notices'].includes(section)" class="solid-button" @click="edit()">＋ {{ section === 'posts' ? '写篇文章' : '新建公告' }}</button><button v-if="editor" class="outline-button" @click="closeEditor">返回列表</button></div>
        <p v-if="error" class="form-error" role="alert">{{ error }}</p><p v-if="message" class="success-message" role="status">{{ message }}</p>
        <div v-if="loading" class="empty-state" role="status">正在读取数据…</div>
        <form v-else-if="editor" class="content-editor" @submit.prevent="save('published')"><div class="editor-main"><label>标题<input class="title-input" v-model="editor.title" required maxlength="160" placeholder="给这次记录起个名字"></label><template v-if="section === 'posts'"><label>内容摘要<textarea v-model="editor.description" maxlength="300" rows="2"></textarea></label><label>正文 · Markdown<textarea class="markdown-input" v-model="editor.markdown" required rows="18"></textarea></label><details class="preview"><summary>查看正文预览</summary><div class="article-body" v-html="preview"></div></details></template><label v-else>公告正文<textarea v-model="editor.text" rows="15" required maxlength="5000"></textarea></label></div><aside class="editor-meta"><h2>发布设置</h2><template v-if="section === 'posts'"><label>文章地址<input v-model="editor.slug" :readonly="Boolean(editor.id)" maxlength="200" placeholder="留空自动生成，如 notes/hello"></label><small>创建后地址固定，支持分层路径。</small><a v-if="publishedURL" class="outline-button" :href="publishedURL" target="_blank" rel="noopener noreferrer">查看网站中的已发布版本 ↗</a><label>分类<input v-model="editor.category" maxlength="30" required></label><label>标签<input :value="editor.tags.join('，')" @input="editor.tags = $event.target.value.split(/[,，]/).map(item => item.trim()).filter(Boolean)" placeholder="逗号分隔"></label><label>日期<input v-model="editor.date" type="date" required></label><label>封面地址<input v-model="editor.cover" placeholder="可通过下方上传"></label><img v-if="editor.cover" class="cover-preview" :src="imageURL(editor.cover)" alt="封面预览"><label class="upload-button outline-button">上传封面<input type="file" accept="image/jpeg,image/png,image/webp" :disabled="busy" @change="upload"></label></template><div class="publish-note"><strong>发布到网站</strong><p v-if="siteURL">网站立即更新，无需重新构建。仓库中的 Markdown 不会被覆盖。</p><p v-else>当前服务尚未启用动态网站，启用后即可公开阅读。</p><p>存为草稿会下架现有公开版本。</p></div><button class="solid-button" :disabled="busy">发布文章 / 公告 ↗</button><button class="outline-button" type="button" :disabled="busy" @click="save('draft')">保存草稿</button></aside></form>
        <template v-else-if="['posts', 'notices', 'comments', 'users'].includes(section)">
          <form class="list-toolbar" @submit.prevent="page = 1; load()"><input v-model="query" type="search" placeholder="搜索当前内容" aria-label="搜索当前内容"><button v-if="section === 'posts'" class="outline-button">搜索</button><span>{{ total }} 条{{ section === 'posts' ? '' : ' · 最多显示 100 条' }}</span></form>
          <div v-if="!visibleItems.length" class="empty-state"><h2>这里，等一份新的记录。</h2><p>暂时没有匹配的内容。</p></div>
          <section v-else class="content-list">
            <article v-for="item in visibleItems" :key="item.id" class="content-row"><div class="row-main"><span class="category-label">{{ item.category || (section === 'users' ? item.email : section === 'comments' ? item.post_title : '小站公告') }}</span><button v-if="['posts', 'notices'].includes(section)" class="row-title" @click="edit(item)">{{ item.title }}</button><h2 v-else>{{ item.name }}</h2><p>{{ item.description || item.text || item.bio }}</p><div class="row-meta"><span v-if="item.status" class="status-tag" :class="item.status">{{ item.status === 'published' ? '已发布' : '草稿' }}</span><span v-if="section === 'users'" class="status-tag">{{ item.role === 'admin' ? '管理员' : '读者' }} · {{ item.two_factor ? '2FA 开启' : '2FA 未开启' }}</span><time>{{ (item.date || item.created_at || item.updated_at || '').slice(0, 10) }}</time></div></div><div v-if="section !== 'users'" class="row-actions"><button v-if="section !== 'comments'" @click="edit(item)">编辑</button><button class="danger-button" :disabled="busy" @click="remove(item)">删除</button></div></article>
          </section><div v-if="section === 'posts'" class="pagination"><button class="outline-button" :disabled="page === 1" @click="page--; load()">上一页</button><span>第 {{ page }} 页</span><button class="outline-button" :disabled="!hasMore" @click="page++; load()">下一页</button></div>
        </template>
        <form v-else-if="section === 'smtp' && settings" class="settings-panel" @submit.prevent="saveSettings('smtp')"><div class="panel-heading"><div><span class="eyebrow">TRANSACTIONAL EMAIL</span><h2>SMTP 邮件服务</h2></div><label class="switch-label"><input type="checkbox" v-model="settings.smtp.enabled">启用邮件注册</label></div><p class="help-text">验证码有效期 10 分钟，单次使用，带发送频率和错误次数限制。配置密码加密存储，留空表示保留已有密码。</p><div class="form-grid"><label>SMTP 主机<input v-model="settings.smtp.host" placeholder="smtp.example.com" :required="settings.smtp.enabled"></label><label>端口<input v-model.number="settings.smtp.port" type="number" min="1" max="65535" required></label><label>传输加密<select v-model="settings.smtp.mode"><option value="starttls">STARTTLS（通常为 587）</option><option value="tls">隐式 TLS（通常为 465）</option></select></label><label>登录用户名<input v-model="settings.smtp.username" autocomplete="off"></label><label>密码 / 授权码<input v-model="settings.smtp.password" type="password" autocomplete="new-password" :placeholder="settings.smtp.has_password ? '已配置，留空保留' : '填写邮件服务授权码'"></label><label>发件人邮箱<input v-model="settings.smtp.from" type="email" :required="settings.smtp.enabled"></label><label>发件人名称<input v-model="settings.smtp.from_name" maxlength="60"></label></div><div class="form-actions"><button class="solid-button" :disabled="busy">保存邮件配置</button><button class="outline-button" type="button" :disabled="busy" @click="testSettings('smtp')">按已保存配置发送测试邮件</button></div><p class="help-text">测试邮件只发送到当前管理员邮箱：{{ user.email }}。服务接受邮件不代表最终送达，请检查收件箱与垃圾箱。</p></form>
        <form v-else-if="section === 'storage' && settings" class="settings-panel" @submit.prevent="saveSettings('storage')"><div class="panel-heading"><div><span class="eyebrow">IMAGE STORAGE</span><h2>让图片各得其所</h2></div></div><div class="storage-options"><label v-for="option in [{ id: 'local', name: '本地目录', detail: '默认可用，适合起步' }, { id: 's3', name: 'S3 兼容', detail: '自定义端点与路径风格' }, { id: 'webdav', name: 'WebDAV', detail: '连接云盘或文件服务器' }]" :key="option.id" :class="{ selected: settings.storage.active === option.id }"><input type="radio" v-model="settings.storage.active" :value="option.id"><strong>{{ option.name }}</strong><small>{{ option.detail }}</small></label></div><p class="help-text">只影响新上传图片。已有图片保存独立配置快照，切换后不会改写原位置；撤销旧服务凭据前需要先迁移旧图片。密钥不会下发给浏览器。</p>
          <div v-if="settings.storage.active === 's3'" class="form-grid"><label>Endpoint<input v-model="settings.storage.s3.endpoint" type="url" placeholder="https://s3.example.com" required></label><label>Region<input v-model="settings.storage.s3.region" placeholder="us-east-1"></label><label>Bucket<input v-model="settings.storage.s3.bucket" required></label><label>Access Key<input v-model="settings.storage.s3.access_key" required autocomplete="off"></label><label>Secret Key<input v-model="settings.storage.s3.secret_key" type="password" autocomplete="new-password" :required="!settings.storage.s3.has_secret" :placeholder="settings.storage.s3.has_secret ? '已配置，留空保留' : ''"></label><label class="switch-label"><input type="checkbox" v-model="settings.storage.s3.path_style">使用路径风格（多数 S3 兼容服务）</label></div>
          <div v-else-if="settings.storage.active === 'webdav'" class="form-grid"><label class="full-width">WebDAV 目标目录 URL<input v-model="settings.storage.webdav.url" type="url" required placeholder="https://dav.example.com/remote.php/dav/files/user/kukie/"><small>目录需提前创建，账号需有读取和写入权限。</small></label><label>用户名<input v-model="settings.storage.webdav.username" required autocomplete="off"></label><label>密码 / 应用密码<input v-model="settings.storage.webdav.password" type="password" autocomplete="new-password" :required="!settings.storage.webdav.has_password" :placeholder="settings.storage.webdav.has_password ? '已配置，留空保留' : ''"></label></div>
          <div v-else class="local-storage-note"><strong>本地持久目录</strong><code>.local/kukie/uploads/</code><p>已开箱即用。备份时同时备份数据库、master.key 和此目录。</p></div><p class="help-text">默认要求 HTTPS 并阻止内网地址。{{ settings.private_storage_allowed ? '服务器已允许内网存储。' : '如需 NAS 内网服务，请由部署者设置 KUKIE_ALLOW_PRIVATE_STORAGE=1 后重启。' }}图片统一通过 API 读取，不要求桶公开。</p><div class="form-actions"><button class="solid-button" :disabled="busy">保存存储配置</button><button class="outline-button" type="button" :disabled="busy" @click="testSettings('storage')">测试已保存连接</button></div>
        </form>
        <div v-else-if="section === 'security'"><Security :user="user" @updated="refreshUser" /><button class="outline-button account-exit" @click="signOut">退出控制台</button></div>
        <footer class="workspace-footer"><span>Kukie · 为好内容留一个位置</span><span>CONTROL PANEL · 0.1.0</span></footer>
      </main>
    </div>
  </div>
</template>
