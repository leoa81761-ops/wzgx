<template>
  <h1>操作日志</h1>
  <div class="row" style="margin-bottom:12px">
    <select v-model="action" style="max-width:220px" @change="load(0)"><option value="">全部操作</option>
      <option v-for="(n, k) in actionNames" :key="k" :value="k">{{ n }}</option></select>
  </div>
  <div v-if="err" class="msg">{{ err }}</div>
  <table>
    <tr><th class="nowrap">时间</th><th>操作人</th><th class="nowrap">操作</th><th>对象</th><th>详情</th></tr>
    <tr v-for="l in list" :key="l.id">
      <td class="muted nowrap">{{ fmtTime(l.at) }}</td><td>{{ l.actor || '系统' }}</td>
      <td class="nowrap">{{ actionNames[l.action] || l.action }}</td>
      <td class="muted">{{ l.target }}</td><td class="muted">{{ describe(l.detail) }}</td>
    </tr>
  </table>
  <p v-if="!list.length && !err" class="muted">没有记录。</p>
  <div class="row" style="margin-top:10px"><button :disabled="offset === 0" @click="load(offset - 100)">上一页</button><button :disabled="list.length < 100" @click="load(offset + 100)">下一页</button></div>
</template>
<script setup>
import { ref, onMounted } from 'vue'
import { get, fmtTime } from '../api.js'
const actionNames = {
  'account.bind': '绑定公众号', 'account.check': '检查公众号接口', 'account.profile': '修改写作设置', 'account.update': '修改公众号信息',
  'account.unbind': '解绑公众号', 'column.save': '保存栏目', 'column.delete': '删除栏目', 'plan.save': '保存写作计划', 'plan.delete': '删除写作计划',
  'job.enqueue': '开始写一篇', 'article.push': '推送到草稿箱', 'article.push_failed': '推送失败', 'secret.tavily': '修改 Tavily 密钥',
  'article_type.save': '保存文章类型', 'config.save': '保存模型配置', 'config.delete': '删除模型配置', 'config.test': '测试模型连接',
  'route.save': '保存环节路由', 'route.delete': '删除环节路由', 'sensitive_words.save': '修改敏感词', 'user.allowance': '修改用户套餐/可绑数量',
  'user.daily_quota': '修改每日篇数', 'staff.bootstrap': '创建初始超级管理员', 'staff.create': '添加运营人员', 'staff.status': '启用/停用运营人员', 'auth.password': '修改密码', 'account.pause': '暂停公众号', 'account.resume': '恢复公众号',
  'account.auto_pause': '自动暂停公众号', 'account.confirm_topic': '切换选题确认', 'topic.approve': '采用选题', 'topic.change': '改题目',
  'topic.reject': '否决选题', 'settings.save': '修改平台设置', 'config.pricing': '修改模型单价与上限', 'job.retry': '重试任务', 'job.cancel': '取消任务', 'order.activate': '手动开通套餐', 'account.layout': '更换排版', 'tier.save': '修改套餐', 'complaint.handle': '处理投诉', 'user.export': '导出数据', 'user.delete_request': '申请注销', 'user.delete_cancel': '登录取消注销', 'user.purge': '注销删除数据', 'invite.valid': '邀请生效', 'style.learn': '提炼写作风格', 'style.toggle': '开关写作风格', 'style.delete': '删除写作风格', 'ref_image.add': '添加授权图片', 'ref_image.delete': '删除授权图片', 'article.ref_image': '文章换用授权图片', 'account.stats_weighting': '设置按数据调整栏目', 'account.auto_publish': '设置自动发表', 'account.auto_push': '设置自动推送草稿箱', 'article.auto_push_failed': '自动推送失败', 'article.publish_cancel': '取消自动发表', 'article.publish_submit': '提交发表', 'article.published': '已发表', 'article.publish_failed': '发表失败', 'publish.pause': '暂停自己的自动发表', 'publish.resume': '恢复自己的自动发表', 'publish.platform_pause': '暂停全平台自动发表', 'publish.platform_resume': '恢复全平台自动发表',
}
const keyNames = {
  account_id: '公众号', column_id: '栏目', plan_id: '计划', job_id: '任务', user_id: '用户', article_type_id: '文章类型', id: '编号',
  ok: '结果', errcode: '微信错误码', enabled: '启用', kind: '类别', model: '模型', reasoning_effort: '推理强度', key: '密钥', tier: '套餐',
  config_ids: '配置顺序', max_accounts: '可绑数量', quota_exempt: '不限篇数', daily_quota: '每日篇数', before: '原有', after: '现在',
  media_id: '草稿编号', topic: '题目', untouched: '连续未处理篇数', auto_pause_after: '自动暂停篇数', daily_cap: '每日花费上限', price_in_per_m: '输入单价', price_out_per_m: '输出单价', order_id: '订单', complaint_id: '投诉', invitee_id: '被邀请人', bonus: '奖励篇数', price_text: '价格说明', visible: '显示', name: '名称', contact: '联系方式', invite_bonus: '邀请奖励', samples: '范文篇数', column_id: '栏目', price_per_image: '每张图片单价', publish_id: '发表编号', review_minutes: '检查时间（分钟）', publish_cap: '每天发表上限', auto_publish: '自动发表', layout: '排版', rerendered: '重新排版篇数', months: '月数', amount: '金额', role: '角色', status: '状态', error: '错误', reason: '原因',
}
const valueNames = { replaced: '已更换', kept: '未改', operator: '运营', super_admin: '超级管理员', active: '正常', disabled: '停用', '*': '全部' }
function val(k, v) {
  if (k === 'ok') return v ? '成功' : '失败'
  if (typeof v === 'boolean') return v ? '是' : '否'
  if (Array.isArray(v)) return v.map((x) => '#' + x).join(' → ')
  if (k.endsWith('_id') || k === 'id') return '#' + v
  if (k === 'before' || k === 'after') return `${v} 个`
  return valueNames[v] ?? String(v)
}
function describe(d) {
  if (!d || typeof d !== 'object') return ''
  return Object.entries(d).filter(([, v]) => v !== null && v !== '').map(([k, v]) => `${keyNames[k] || k}：${val(k, v)}`).join('；')
}
const list = ref([]), action = ref(''), offset = ref(0), err = ref('')
async function load(off) {
  try { list.value = await get(`/api/admin/audit?offset=${off}&action=${encodeURIComponent(action.value)}`); offset.value = off; err.value = '' } catch (e) { err.value = e.message }
}
onMounted(() => load(0))
</script>
