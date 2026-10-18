<template>
  <div class="pub">
    <PublicNav />
    <div class="doc">
      <h1>帮助</h1>
      <nav class="toc"><a href="#bind">绑定公众号</a> · <a href="#ip">IP 白名单</a> · <a href="#flow">写作流程</a> · <a href="#faq">常见问题</a></nav>

      <h2 id="bind">一、绑定公众号：找到 AppID 和 AppSecret</h2>
      <ol>
        <li>用电脑浏览器登录 <b>微信公众平台</b>（mp.weixin.qq.com），用管理员微信扫码。</li>
        <li>左侧菜单进入 <b>设置与开发 → 开发接口管理</b>（旧版界面叫 <b>开发 → 基本配置</b>）。</li>
        <li>在「开发者ID」里能看到 <b>AppID（开发者ID）</b>：以 <code>wx</code> 开头，共 18 位，例如 <code>wx0123456789abcdef</code>。直接复制。</li>
        <li><b>AppSecret（开发者密码）</b>：点「重置」或「启用」，用管理员微信扫码确认后会显示一次，是 32 位字母和数字。<b>微信只显示这一次</b>，请马上复制。
          <p class="hint">重置 AppSecret 后，原来用旧密码的其他工具会失效，请确认没有别的工具在用。</p></li>
        <li>回到本站「公众号」页面，点「绑定公众号」，填入名称、AppID、AppSecret，保存。系统会自动检查能不能连上。</li>
      </ol>
      <p>要求：公众号需要是<b>已认证</b>或有草稿箱接口权限的订阅号 / 服务号。个人未认证订阅号可能没有接口权限，检查时会提示。</p>

      <h2 id="ip">二、设置 IP 白名单</h2>
      <p>微信要求调用接口的服务器 IP 在白名单里，否则会提示「IP 不在白名单」（错误码 40164）。</p>
      <ol>
        <li>同样在 <b>开发接口管理</b>（或 <b>基本配置</b>）页面，找到 <b>IP白名单</b>，点「查看」或「修改」。</li>
        <li>把本站服务器 IP <code>{{ ip || '（登录后在绑定页面可以看到）' }}</code> 加进去，一行一个，管理员扫码确认。</li>
        <li>保存后回到本站，在公众号详情里点「重新检查」。</li>
      </ol>

      <h2 id="flow">三、写作流程</h2>
      <ol>
        <li>在公众号详情里填写<b>写作设置</b>：账号定位、读者、人设、语气等，越具体越好。</li>
        <li>建立<b>栏目</b>，每个栏目选一种文章类型。</li>
        <li>在<b>写作计划</b>里选每周哪几天、几点写；或者随时「现在写一篇」。</li>
        <li>写好的文章在<b>文章</b>页预览，确认后推送到公众号<b>草稿箱</b>。本站不会替你发表。</li>
      </ol>

      <h2 id="faq">四、常见问题</h2>
      <p><b>每天能写几篇？</b>看你的套餐，<router-link to="/pricing">价格页</router-link>有说明。邀请好友也能得到额外篇数（如果平台开启了邀请奖励）。</p>
      <p><b>AppSecret 安全吗？</b>密钥加密保存，页面上只显示前后几位，任何人（包括管理员）都看不到完整内容。</p>
      <p><b>不想用了怎么办？</b>在「我的」页面可以导出全部数据，也可以注销账号。注销后有 7 天反悔期，期间登录就会取消注销。</p>
      <p><b>发现文章侵权或有问题？</b>请到<router-link to="/complaint">投诉与举报</router-link>页面留言。</p>
    </div>
    <PublicFoot />
  </div>
</template>
<script setup>
import { ref, onMounted } from 'vue'
import { get } from '../api.js'
import PublicNav from './PublicNav.vue'
import PublicFoot from './PublicFoot.vue'
const ip = ref('')
onMounted(async () => { try { ip.value = (await get('/api/site')).server_ip } catch { /* needs login */ } })
</script>
