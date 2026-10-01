<script setup>
import { ref, onBeforeUnmount } from 'vue';
import QRCode from 'qrcode';
import { api, json } from './api.js';

const props = defineProps({ user: Object });
const emit = defineEmits(['updated']);
const password = ref('');
const code = ref('');
const setup = ref(null);
const qr = ref('');
const recovery = ref([]);
const error = ref('');
const message = ref('');
const busy = ref(false);
async function action(kind) {
  busy.value = true; error.value = ''; message.value = '';
  try {
    if (kind === 'setup') { setup.value = await api('/me/2fa/setup', json('POST', { password: password.value })); qr.value = await QRCode.toDataURL(setup.value.uri, { width: 220, margin: 1 }); }
    if (kind === 'enable') { const result = await api('/me/2fa/enable', json('POST', { code: code.value })); recovery.value = result.recovery_codes; setup.value = null; qr.value = ''; message.value = '两步验证已开启，其他设备会话已退出。请立即保存恢复码。'; emit('updated'); }
    if (kind === 'disable') { await api('/me/2fa/disable', json('POST', { password: password.value, code: code.value })); message.value = '两步验证已关闭，旧恢复码失效。'; recovery.value = []; emit('updated'); }
    password.value = ''; code.value = '';
  } catch (failure) { error.value = failure.message; }
  finally { busy.value = false; }
}
onBeforeUnmount(() => { password.value = ''; setup.value = null; recovery.value = []; });
</script>

<template>
  <section class="settings-panel security-panel">
    <div class="panel-heading"><div><span class="eyebrow">ACCOUNT SECURITY</span><h2>多一道保护，由你决定。</h2></div><span class="status-tag" :class="{ draft: !user.two_factor }">{{ user.two_factor ? '已开启' : '未开启' }}</span></div>
    <p class="help-text">使用任意兼容 TOTP 的验证器。开启后，登录需要密码和验证码，也可使用一次性恢复码。管理员和读者都能自行选择。</p>
    <form v-if="!setup && !recovery.length" @submit.prevent="action(user.two_factor ? 'disable' : 'setup')">
      <label>确认当前密码<input v-model="password" type="password" autocomplete="current-password" required minlength="12" maxlength="128"></label>
      <label v-if="user.two_factor">验证码或恢复码<input v-model="code" required autocomplete="one-time-code" placeholder="验证器代码 / 一次性恢复码"></label>
      <button class="solid-button" :disabled="busy">{{ busy ? '处理中' : user.two_factor ? '关闭两步验证' : '开始设置两步验证' }}</button>
    </form>
    <form v-if="setup" @submit.prevent="action('enable')">
      <div class="qr-wrap"><img :src="qr" alt="验证器绑定二维码"><div><strong>先扫描，再验证</strong><p class="help-text">二维码在当前浏览器内生成，不发送给第三方。若无法扫码，可手动输入密钥：</p><code class="secret-key">{{ setup.secret }}</code><small>10 分钟内完成绑定，确认前不会启用。</small></div></div>
      <label>验证器的 6 位代码<input v-model="code" inputmode="numeric" pattern="[0-9]{6}" maxlength="6" required autocomplete="one-time-code"></label>
      <button class="solid-button" :disabled="busy">验证并开启</button>
    </form>
    <div v-if="recovery.length" class="recovery-panel"><h3>这些恢复码只显示这一次</h3><p>请抄下或存入密码管理器，每个代码仅可使用一次。关闭页面后无法重新查看。</p><div class="recovery-grid"><code v-for="item in recovery" :key="item">{{ item }}</code></div><button class="outline-button" @click="recovery = []">我已妥善保存</button></div>
    <p v-if="error" class="form-error" role="alert">{{ error }}</p><p v-if="message" class="success-message" role="status">{{ message }}</p>
  </section>
</template>
