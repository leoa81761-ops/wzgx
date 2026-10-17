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
  'user.daily_quota': '修改每日篇数', 'staff.bootstrap': '创建初始超级管理员', 'staff.create': '添加运营人员', 'staff.status': '启用/停用运营人员', 'auth.password': '修改密码',
}
const keyNames = {
  account_id: '公众号', column_id: '栏目', plan_id: '计划', job_id: '任务', user_id: '用户', article_type_id: '文章类型', id: '编号',
  ok: '结果', errcode: '微信错误码', enabled: '启用', kind: '类别', model: '模型', reasoning_effort: '推理强度', key: '密钥', tier: '套餐',
  config_ids: '配置顺序', max_accounts: '可绑数量', quota_exempt: '不限篇数', daily_quota: '每日篇数', before: '原有', after: '现在',
  media_id: '草稿编号', role: '角色', status: '状态', error: '错误', reason: '原因',
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
