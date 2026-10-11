<template>
  <div class="stack">
    <div>
      <h1 style="font-size: 36px">公众号绑定教程</h1>
      <p class="lede">绑定用的是公众号后台的 AppID 和 AppSecret。密钥只加密保存在服务器上，你随时可以解绑，解绑后凭证会删除。</p>
    </div>
    <ol class="card stack">
      <li>登录微信公众平台（mp.weixin.qq.com），进入「设置与开发 → 基本配置」。</li>
      <li>复制 AppID。AppSecret 只在重置或查看时出现，请妥善保管，不要发到聊天工具里。</li>
      <li>把本服务的固定出口 IP 加入 IP 白名单。当前配置的出口 IP：<strong>{{ egress || "部署后由管理员填写" }}</strong>。</li>
      <li>确认草稿箱可用。个人号和未认证号没有发布接口，文章进入草稿后，请用手机「公众号助手」审阅并手动发布。</li>
      <li>回到本站「公众号」页，选择主体类型（个人 / 企业）和是否认证，勾选同意保存凭证后再提交。</li>
      <li>订阅号每天可群发 1 次，服务号每月 4 次。本产品不调用群发接口。</li>
    </ol>
    <section class="card" id="auth">
      <h2>授权说明</h2>
      <p>系统会使用凭证获取调用凭据、上传一张封面素材，并写入再删除一条检测草稿，用来判断能不能使用草稿箱。检测不通过时，文章改为站内导出 HTML，由你复制到公众号编辑器。</p>
      <p>企业认证号的自动发布默认关闭。在写作计划里勾选内容责任并开启后，系统才会在审阅窗口结束后提交低风险稿。个人号和未认证号只写草稿箱。</p>
      <p v-if="mode === 'mock'" class="banner info">当前环境是模拟模式。可以用任意 AppID（建议以 wx_mock 开头）走完绑定和入草稿箱的流程，不会访问微信服务器。</p>
    </section>
  </div>
</template>

<script setup>
import { onMounted, ref } from "vue";
import { api } from "../api";

const egress = ref("");
const mode = ref("");

onMounted(async () => {
  try {
    const meta = await api("/api/meta");
    egress.value = meta.public_egress_ip || "";
    mode.value = meta.wechat_mode || "";
  } catch {
    egress.value = "";
  }
});
</script>
