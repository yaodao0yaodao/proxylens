const $ = selector => document.querySelector(selector);
let token = localStorage.getItem('proxylens-token') || '';
let refreshTimer = null;
let nodeRows = [];
let nodeSort = {key: 'priority', direction: -1};
let selectedTaskID = '';
let selectedContinuousID = '';
let taskNames = new Map();
let currentTasks = new Map();

async function api(path, options = {}) {
  options.headers = {...(options.headers || {}), Authorization: `Bearer ${token}`};
  const response = await fetch(`/api/${path}`, options);
  let body = null;
  try { body = await response.json(); } catch {}
  if (!response.ok) throw new Error(body?.error || response.statusText);
  return response.status === 204 ? null : body;
}

function toast(message) {
  const element = $('#toast');
  element.textContent = message;
  element.style.display = 'block';
  clearTimeout(toast.timer);
  toast.timer = setTimeout(() => { element.style.display = 'none'; }, 3500);
}

function esc(value) {
  return String(value ?? '').replace(/[&<>"']/g, char => ({'&':'&amp;','<':'&lt;','>':'&gt;','"':'&quot;',"'":'&#39;'}[char]));
}

function fmtTime(value) {
  if (!value) return '尚未';
  const date = new Date(value);
  return Number.isNaN(date.valueOf()) || date.getFullYear() < 2000 ? '尚未' : date.toLocaleString();
}

function fmtBytes(value) {
  const bytes = Number(value) || 0;
  if (bytes >= 1073741824) return `${(bytes / 1073741824).toFixed(2)} GiB`;
  return `${(bytes / 1048576).toFixed(2)} MiB`;
}

function publishURL(value) { return new URL(value, window.location.href).href; }

function fmtRemaining(seconds) {
  seconds = Math.max(0, Number(seconds) || 0);
  const hours = Math.floor(seconds / 3600), minutes = Math.ceil((seconds % 3600) / 60);
  return hours ? `${hours}小时${minutes}分钟` : `${minutes}分钟`;
}

async function copyText(value) {
  try {
    if (navigator.clipboard && window.isSecureContext) {
      await navigator.clipboard.writeText(value);
      toast('已复制到剪贴板');
      return;
    }
  } catch {}
  const area = document.createElement('textarea');
  area.value = value;
  area.readOnly = true;
  area.style.cssText = 'position:fixed;opacity:0;left:-9999px';
  document.body.append(area);
  area.select();
  area.setSelectionRange(0, area.value.length);
  const copied = document.execCommand('copy');
  area.remove();
  if (!copied) throw new Error('浏览器拒绝访问剪贴板');
  toast('已复制到剪贴板');
}

async function downloadAPI(path, filename) {
  const response = await fetch(`/api/${path}`, {headers: {Authorization: `Bearer ${token}`}});
  if (!response.ok) throw new Error(`${filename}下载失败`);
  const objectURL = URL.createObjectURL(await response.blob());
  const link = document.createElement('a');
  link.href = objectURL;
  link.download = filename;
  document.body.append(link);
  link.click();
  link.remove();
  setTimeout(() => URL.revokeObjectURL(objectURL), 1000);
}

function setAuthenticated(authenticated) {
  $('#add').hidden = !authenticated;
  $('#settingsBtn').hidden = !authenticated;
  $('#versionBtn').hidden = !authenticated;
  $('#overview').hidden = !authenticated;
  $('#auth').hidden = authenticated;
  $('#tasks').hidden = !authenticated;
  if (!authenticated) {
    $('#nodes').hidden = true;
    clearInterval(refreshTimer);
    refreshTimer = null;
  } else if (!refreshTimer) {
    refreshTimer = setInterval(() => {
      if (!$('#tasks').hidden && !document.querySelector('dialog[open]')) loadTasks(false);
    }, 3000);
  }
}

function taskCard(task) {
  const state = task.runtime?.status || 'idle';
  const stage = task.runtime?.stage || '空闲';
  const title = task.name || '正在自动获取名称…';
  const url = publishURL(task.sing_box_url);
  const notices = (task.notices || []).map(item => item.name).filter(Boolean).join('；') || '暂无';
  const statusClass = state === 'running' ? 'running' : state === 'paused' ? 'paused' : 'idle';
  const continuousButton = task.runtime?.continuous
    ? task.runtime?.stop_after_current
      ? '<button class="ghost" disabled>本次检测完成后结束</button>'
      : `<button class="danger" data-stop-continuous="${task.id}">取消不间断完整检测（剩余${fmtRemaining(task.runtime?.remaining_seconds)}）</button>`
    : `<button class="ghost" data-continuous="${task.id}">触发4小时不间断完整检测</button>`;
  const important = String(task.last_error || '').split('\n').map(value => value.trim()).filter(Boolean);
  const article = document.createElement('article');
  article.className = 'card';
  article.innerHTML = `
    <div class="card-top">
      <div class="card-title-actions"><button class="task-name" data-copy-name="${esc(title)}" title="点击复制任务名称">${esc(title)}</button><button class="danger compact" data-del="${task.id}">删除</button></div>
      <span class="status ${statusClass}">${esc(stage)}</span>
    </div>
    <div class="card-meta">
      <div class="muted">源订阅更新时间：${fmtTime(task.last_subscription_at)}</div>
      <div class="muted">sing-box 配置更新时间：${fmtTime(task.last_sing_box_at)}</div>
      <div class="muted full-row">套餐通知：${esc(notices)}</div>
      ${important.map(value => `<div class="error full-row">重要通知：${esc(value)}</div>`).join('')}
    </div>
    <div class="task-actions">
      <div class="task-action-row">${continuousButton}${state === 'paused' ? `<button data-start="${task.id}">启动</button>` : `<button class="ghost" data-pause="${task.id}">暂停</button>`}</div>
      <div class="task-action-row"><button class="ghost" data-nodes="${task.id}">查看质量</button><button class="ghost" data-conflicts="${task.id}">节点去重</button><button class="ghost" data-task-config="${task.id}">任务设置</button><button data-copy="${esc(url)}">复制 sing-box 订阅</button></div>
    </div>`;
  return article;
}

function renderOverview(overview) {
  const storage = overview.storage || {};
  $('#storage').textContent = `数据库占用 ${fmtBytes(storage.database_bytes)}；所在分区剩余 ${fmtBytes(storage.partition_free_bytes)}；${storage.database_path || ''}`;
  const alerts = overview.alerts || [];
  $('#softwareNotices').innerHTML = alerts.length
    ? alerts.map(value => `<div class="notice-line">重要通知：${esc(value)}</div>`).join('')
    : '<div class="muted">重要通知：暂无</div>';
}

async function loadTasks(showError = true) {
  try {
    const [tasks, overview] = await Promise.all([api('tasks'), api('overview')]);
    currentTasks = new Map(tasks.map(task => [task.id, task]));
    taskNames = new Map(tasks.map(task => [task.id, task.name || '未命名任务']));
    setAuthenticated(true);
    $('#cards').replaceChildren(...tasks.map(taskCard));
    renderOverview(overview);
  } catch (error) {
    setAuthenticated(false);
    if (showError && token) toast(error.message);
  }
}

const qualityValue = (row, key) => {
  const quality = row.quality || {};
  const value = {availability: quality.availability, latency: quality.average_latency_ms, priority: quality.priority}[key];
  return key === 'latency' && !(value > 0) ? null : Number(value) || 0;
};

function renderNodes() {
  const rows = [...nodeRows].sort((a, b) => {
    const av = qualityValue(a, nodeSort.key), bv = qualityValue(b, nodeSort.key);
    if (av === null) return bv === null ? 0 : 1;
    if (bv === null) return -1;
    return nodeSort.direction * (av - bv);
  });
  document.querySelectorAll('.sort').forEach(button => {
    button.classList.toggle('active', button.dataset.sort === nodeSort.key);
    button.dataset.arrow = button.dataset.sort === nodeSort.key ? (nodeSort.direction > 0 ? '↑' : '↓') : '';
  });
  $('#nodeRows').innerHTML = rows.map(row => {
    const node = row.node, quality = row.quality || {};
    return `<tr><td>${esc(node.display_name || '出口未识别')}</td><td>${esc(node.original_name)}</td><td>${esc(node.protocol || '-')}</td><td>${((quality.availability || 0) * 100).toFixed(1)}% (${quality.samples || 0})</td><td>${quality.average_latency_ms ? `${quality.average_latency_ms.toFixed(0)} ms` : '-'}</td><td>${(quality.priority || 0).toFixed(1)}</td></tr>`;
  }).join('');
}

async function showNodes(id) {
  nodeRows = await api(`tasks/${id}/nodes`);
  nodeSort = {key: 'priority', direction: -1};
  $('#nodeTitle').textContent = `${taskNames.get(id) || '任务'} · 节点质量`;
  $('#tasks').hidden = true;
  $('#nodes').hidden = false;
  renderNodes();
}

async function openTaskSettings(id) {
  const settings = await api(`tasks/${id}/settings`);
  const task = currentTasks.get(id);
  if (!task) throw new Error('任务数据已刷新，请重试');
  selectedTaskID = id;
  $('#taskSettingsTitle').textContent = `${taskNames.get(id) || '任务'} · 任务设置`;
  $('#taskName').value = task.name || '';
  $('#taskSource').value = task.source_url || '';
  $('#taskDetectionMinutes').value = settings.detection_minutes ?? '';
  $('#taskGooglePlayMode').value = settings.google_play_mode || '';
  $('#taskLANEnabled').value = settings.lan_enabled === true ? 'true' : settings.lan_enabled === false ? 'false' : '';
  $('#taskLANPort').value = settings.lan_port ?? '';
  $('#taskSettingsDialog').showModal();
}

async function openIdentityConflicts(id) {
  selectedTaskID = id;
  const conflicts = ((await api(`tasks/${id}/identity-conflicts`)) || []).filter(item => !item.resolved_at);
  $('#conflictTitle').textContent = `${taskNames.get(id) || '任务'} · 节点去重`;
  const candidateDetail = node => {
    if (!node) return '<p>节点详细信息暂不可用</p>';
    const location = node.country || node.country_code || '尚未取得出口地区';
    const endpoint = `${node.server || '未知服务器'}:${node.port || '未知端口'}`;
    const exit = node.exit_ip || '尚未取得';
    const asn = node.asn || '尚未取得';
    const multiplier = Number(node.multiplier || 1).toLocaleString(undefined, {maximumFractionDigits: 3});
    return `<div class="conflict-node-head"><strong>${esc(node.display_name || node.original_name || '未命名节点')}</strong><span class="status ${node.removed ? 'paused' : 'running'}">${node.removed ? '已移除，等待复活' : '当前订阅中'}</span></div>
      <div class="conflict-node-details">
        <div><span>原始名称</span><b>${esc(node.original_name || '无')}</b></div>
        <div><span>协议</span><b>${esc((node.protocol || '未知').toUpperCase())}</b></div>
        <div><span>服务器</span><b>${esc(endpoint)}</b></div>
        <div><span>出口</span><b>${esc(location)} · ${esc(exit)}</b></div>
        <div><span>倍率 / 永久编号</span><b>${esc(multiplier)}x / ${esc(node.number || '未分配')}</b></div>
        <div><span>ASN</span><b>${esc(asn)}</b></div>
        <div class="full-row"><span>节点 ID</span><b class="wrap-id">${esc(node.id)}</b></div>
        <div><span>首次记录</span><b>${fmtTime(node.added_at)}</b></div>
        <div><span>最近变化</span><b>${fmtTime(node.modified_at)}</b></div>
        ${node.removed_at ? `<div><span>移除时间</span><b>${fmtTime(node.removed_at)}</b></div>` : ''}
      </div>`;
  };
  const reason = value => ({'ambiguous continuity key':'连接特征对应多个旧节点','ambiguous protocol and source name':'协议和原始名称对应多个旧节点'})[value] || value;
  $('#conflictList').innerHTML = conflicts.length ? conflicts.map(item => `
    <article class="card"><h3>当前订阅中的节点</h3><div class="conflict-node incoming">${candidateDetail(item.incoming)}</div><p class="muted">${esc(reason(item.reason))}，系统无法确定它是不是下面某个旧节点改名或换了服务器。</p>
    <h4>如果它是旧节点，请选择要继承的历史：</h4>
    <div class="conflict-candidates">${(item.candidates || []).map(candidate => `<div class="conflict-node">${candidateDetail(candidate)}<div class="actions"><button type="button" data-merge-target="${esc(candidate.id)}" data-merge-source="${esc(item.incoming_id)}">合并到这个节点并继承历史</button></div></div>`).join('')}</div>
    <div class="actions"><button type="button" class="ghost" data-keep-new="${item.id}">这是新节点，不继承历史</button></div></article>`).join('') : '<p>没有需要人工确认的重复节点。</p>';
  $('#conflictDialog').showModal();
}

function renderVersion(info) {
  const software = info.software || {}, core = info.sing_box || {}, build = info.build || {}, maintenance = info.rule_maintenance || {};
  const rules = info.rules || [], custom = info.routing_customizations || [], compatibility = info.compatibility || [], input = info.input_compatibility || {};
  const revision = build.revision ? String(build.revision).slice(0, 12) + (build.modified ? '（有本地修改）' : '') : '未写入';
  $('#versionInfo').innerHTML = `
    <div class="version-summary">
      <div class="version-card"><strong>ProxyLens ${esc(software.version || '未知')}</strong><div class="muted">程序文件：${fmtTime(software.updated_at)}</div><div class="muted">${esc(build.os || '未知')}/${esc(build.arch || '未知')} · ${esc(build.go_version || 'Go 未知')} · 数据库 v${esc(build.database_schema ?? '未知')}</div><div class="muted">构建修订：${esc(revision)}</div></div>
      <div class="version-card"><strong>sing-box ${esc(core.version || '未知')}</strong><div class="muted">核心文件：${fmtTime(core.updated_at)}</div><div class="muted">最近检查：${fmtTime(core.last_checked_at)} · 最近尝试：${fmtTime(core.last_attempt_at)}</div>${core.error ? `<div class="error">${esc(core.error)}</div>` : ''}</div>
    </div>
    <section class="version-section"><h3>输入兼容能力</h3><div class="version-card"><strong>${esc(input.format || '未知')}</strong><div class="muted">拉取 UA：${esc(input.fetch_ua || '未知')}</div><div>节点协议：${esc((input.protocols || []).join('、'))}</div><div>传输层：${esc((input.transports || []).join('、'))}</div></div></section>
    <section class="version-section"><h3>配置兼容能力与 User-Agent</h3><p class="muted">相同内容不重复保存：原生 sing-box 桌面端复用 Carton 对应核心版本的配置；Android 使用 SFA 配置。</p><div class="compat-list">${compatibility.map(item => `<div class="compat-row"><strong>${esc(item.client)}</strong><code>${esc(item.ua)}</code><span>${esc(item.output)}</span><span class="muted">${esc(item.notes)}</span></div>`).join('')}</div></section>
    <section class="version-section"><h3>ProxyLens 修改后的分流与生成逻辑 <span class="version-badge custom">非上游原版</span></h3><p class="muted">下面是 ProxyLens 在上游规则文件之上重新编排或新增的行为。</p><div class="custom-list">${custom.map(item => `<article class="custom-rule"><div><strong>${esc(item.name)}</strong> <span class="version-badge">${esc(item.scope)}</span></div><div>${esc(item.behavior)}</div><div class="muted">依据：${esc(item.based_on)}</div></article>`).join('')}</div></section>
    <section class="version-section"><h3>上游原版规则文件 <span class="version-badge upstream">未修改文件内容</span></h3><p class="muted">最近整套规则成功：${fmtTime(maintenance.last_success_at)} · 最近尝试：${fmtTime(maintenance.last_attempt_at)}。文件保持上游原样，ProxyLens 只在生成配置时组合和改写分流顺序。</p><div class="version-list">${rules.map(rule => `<div class="version-row"><strong>${esc(rule.tag)}</strong><span class="version-badge upstream">${esc(rule.source_type || '上游原版')}</span><span class="muted">${esc(rule.version || '未知版本')}</span><span class="muted">${fmtTime(rule.updated_at)}</span>${rule.error ? `<span class="error">${esc(rule.error)}</span>` : ''}</div>`).join('') || '<div class="muted">暂无规则信息</div>'}</div></section>`;
}

document.addEventListener('click', async event => {
  const button = event.target.closest('button');
  if (!button || button.disabled) return;
  try {
    if (button.dataset.sort) {
      const key = button.dataset.sort;
      nodeSort.direction = nodeSort.key === key ? -nodeSort.direction : (key === 'latency' ? 1 : -1);
      nodeSort.key = key;
      renderNodes();
    } else if (button.dataset.copy) await copyText(button.dataset.copy);
    else if (button.dataset.copyName) await copyText(button.dataset.copyName);
    else if (button.dataset.continuous) { selectedContinuousID = button.dataset.continuous; $('#continuousDialog').showModal(); }
    else if (button.dataset.stopContinuous) { await api(`tasks/${button.dataset.stopContinuous}/continuous`, {method:'DELETE'}); toast('将在本次检测完成后结束'); await loadTasks(false); }
    else if (button.dataset.pause) { await api(`tasks/${button.dataset.pause}/pause`, {method:'POST'}); toast('已暂停检测，现有订阅继续可用'); await loadTasks(false); }
    else if (button.dataset.start) { await api(`tasks/${button.dataset.start}/start`, {method:'POST'}); toast('任务已启动'); await loadTasks(false); }
    else if (button.dataset.nodes) await showNodes(button.dataset.nodes);
    else if (button.dataset.conflicts) await openIdentityConflicts(button.dataset.conflicts);
    else if (button.dataset.keepNew) {
      await api(`tasks/${selectedTaskID}/identity-conflicts/${button.dataset.keepNew}/keep-new`, {method:'POST'});
      toast('已确认为新节点，不合并旧历史'); await openIdentityConflicts(selectedTaskID);
    } else if (button.dataset.mergeTarget && confirm('确认这是同一个旧节点？确认后会继承旧节点的检测历史和永久编号。')) {
      await api(`tasks/${selectedTaskID}/merge-nodes`, {method:'POST', headers:{'Content-Type':'application/json'}, body:JSON.stringify({target_id:button.dataset.mergeTarget, source_id:button.dataset.mergeSource})});
      toast('节点历史已合并并重新计算质量'); await openIdentityConflicts(selectedTaskID);
    } else if (button.dataset.taskConfig) await openTaskSettings(button.dataset.taskConfig);
    else if (button.dataset.del && confirm('删除此任务及其全部数据？此操作不可恢复。')) { await api(`tasks/${button.dataset.del}`, {method:'DELETE'}); await loadTasks(false); }
  } catch (error) { toast(error.message); }
});

$('#login').onclick = () => { token = $('#token').value.trim(); localStorage.setItem('proxylens-token', token); loadTasks(); };
$('#readLocalToken').onclick = async () => {
  try {
    const response = await fetch('/api/local-token');
    if (!response.ok) throw new Error('此页面不是服务所在的本机地址，请从 LuCI 查看管理令牌');
    token = (await response.json()).token || '';
    $('#token').value = token;
    localStorage.setItem('proxylens-token', token);
    await loadTasks();
  } catch (error) { toast(error.message); }
};
$('#token').addEventListener('keydown', event => { if (event.key === 'Enter') $('#login').click(); });
$('#logs').onclick = () => downloadAPI('logs', 'proxylens.log').catch(error => toast(error.message));
$('#backup').onclick = () => downloadAPI('backup', 'proxylens-backup.sqlite').catch(error => toast(error.message));
$('#add').onclick = () => { $('#newTaskForm').reset(); $('#newTask').showModal(); };
$('#cancelTask').onclick = () => $('#newTask').close();
$('#cancelSettings').onclick = () => $('#settingsDialog').close();
$('#cancelTaskSettings').onclick = () => $('#taskSettingsDialog').close();
$('#cancelConflicts').onclick = () => $('#conflictDialog').close();
$('#cancelContinuous').onclick = () => $('#continuousDialog').close();
$('#closeVersion').onclick = () => $('#versionDialog').close();
$('#versionBtn').onclick = async () => {
  $('#versionInfo').textContent = '正在读取…';
  $('#versionDialog').showModal();
  try { renderVersion(await api('version-info')); } catch (error) { $('#versionInfo').innerHTML = `<div class="error">${esc(error.message)}</div>`; }
};
$('#confirmContinuous').onclick = async () => {
  try {
    await api(`tasks/${selectedContinuousID}/continuous`, {method:'POST'});
    $('#continuousDialog').close();
    toast('4小时不间断完整检测已开始');
    await loadTasks(false);
  } catch (error) { toast(error.message); }
};
$('#back').onclick = () => { $('#nodes').hidden = true; $('#tasks').hidden = false; loadTasks(false); };

function applyOptionalTaskFields(payload, prefix) {
  const detection = $(`#${prefix}DetectionMinutes`).value;
  const mode = $(`#${prefix}GooglePlayMode`).value;
  const port = $(`#${prefix}LANPort`).value;
  const enabled = $(`#${prefix}LANEnabled`).value;
  if (detection !== '') payload.detection_minutes = Number(detection);
  if (mode !== '') payload.google_play_mode = mode;
  if (port !== '') payload.lan_port = Number(port);
  if (enabled !== '') payload.lan_enabled = enabled === 'true';
}

$('#newTaskForm').onsubmit = async event => {
  event.preventDefault();
  const payload = {name:$('#name').value.trim(), subscription_url:$('#sub').value.trim(), subscription_ua:'clash.meta'};
  applyOptionalTaskFields(payload, 'new');
  try {
    await api('tasks', {method:'POST', headers:{'Content-Type':'application/json'}, body:JSON.stringify(payload)});
    $('#newTask').close();
    toast('任务已创建，正在自动命名和检测');
    await loadTasks(false);
  } catch (error) { toast(error.message); }
};

$('#settingsBtn').onclick = async () => {
  try {
    const settings = await api('settings');
    $('#detectionMinutes').value = settings.detection_minutes;
    $('#globalGooglePlayMode').value = settings.google_play_mode;
    $('#globalLANPort').value = settings.lan_port;
    $('#globalLANEnabled').value = String(settings.lan_enabled);
    $('#settingsDialog').showModal();
  } catch (error) { toast(error.message); }
};

$('#settingsForm').onsubmit = async event => {
  event.preventDefault();
  const payload = {detection_minutes:Number($('#detectionMinutes').value), google_play_mode:$('#globalGooglePlayMode').value, lan_port:Number($('#globalLANPort').value), lan_enabled:$('#globalLANEnabled').value === 'true'};
  try {
    await api('settings', {method:'PUT', headers:{'Content-Type':'application/json'}, body:JSON.stringify(payload)});
    $('#settingsDialog').close();
    toast('全局设置已保存，所有未暂停任务将重新检测');
    await loadTasks(false);
  } catch (error) { toast(error.message); }
};

$('#taskSettingsForm').onsubmit = async event => {
  event.preventDefault();
  const task = currentTasks.get(selectedTaskID);
  const payload = {name:$('#taskName').value.trim(), subscription_url:$('#taskSource').value.trim(), subscription_ua:task?.subscription_user_agent || 'clash.meta'};
  applyOptionalTaskFields(payload, 'task');
  try {
    await api(`tasks/${selectedTaskID}/settings`, {method:'PUT', headers:{'Content-Type':'application/json'}, body:JSON.stringify(payload)});
    $('#taskSettingsDialog').close();
    toast('任务设置已保存，正在重新检测');
    await loadTasks(false);
  } catch (error) { toast(error.message); }
};

setAuthenticated(false);
if (token) loadTasks(false);
