// wild-work 管理面板前端（原生 JS，无构建步骤，直接 fetch 管理 API）
"use strict";

const $ = (id) => document.getElementById(id);

// ---------- API 封装 ----------
async function api(path, body) {
  const opts = { method: "GET", headers: { "Content-Type": "application/json" } };
  if (body !== undefined) {
    opts.method = "POST";
    opts.body = JSON.stringify(body);
  }
  const resp = await fetch(path, opts);
  const data = await resp.json().catch(() => ({}));
  if (!resp.ok) {
    throw new Error(data.error || ("请求失败 " + resp.status));
  }
  return data;
}

// ---------- 工具 ----------
function esc(s) {
  return String(s == null ? "" : s).replace(/[&<>"']/g, (c) => ({
    "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;",
  }[c]));
}

let toastTimer = null;
function toast(msg) {
  const t = $("toast");
  t.textContent = msg;
  t.classList.remove("hidden");
  clearTimeout(toastTimer);
  toastTimer = setTimeout(() => t.classList.add("hidden"), 3000);
}

function shortUid(uid) {
  if (!uid) return "";
  return uid.length <= 12 ? uid : uid.slice(0, 6) + "…" + uid.slice(-4);
}

// groupMeta 返回渠道分组的展示元数据：{cls, name, checkin}。
function groupMeta(g) {
  switch (g) {
    case "traework": return { cls: "trae", name: "TraeWork", checkin: false };
    case "qoder": return { cls: "qoder", name: "Qoder", checkin: true };
    case "codebuddy": return { cls: "cb", name: "CodeBuddy(国际)", checkin: true };
    case "wbintl": return { cls: "wbi", name: "WorkBuddy(国际)", checkin: true };
    case "zcode": return { cls: "zc", name: "ZCode (Z.AI)", checkin: false };
    case "autoclaw": return { cls: "ac", name: "AutoClaw", checkin: true };
    default: return { cls: "wb", name: "WorkBuddy", checkin: false };
  }
}

// ---------- 全局状态 ----------
let state = null;

// ---------- 数据加载 ----------
async function loadState() {
  state = await api("/api/state");
  render();
}

async function loadFees() {
  try {
    const fees = await api("/api/fees");
    renderFees(fees);
  } catch (e) { /* 费率接口失败不阻塞 */ }
}

async function loadTokenStats() {
  try {
    const stats = await api("/api/token_usage");
    renderTokenStats(stats);
  } catch (e) { /* Token 统计失败不阻塞 */ }
}

async function refreshTokens() {
  try {
    await loadTokenStats();
    toast("Token 用量已刷新");
  } catch (e) { toast(e.message); }
}

async function refreshFees() {
  $("btnRefreshFees").disabled = true;
  try {
    await api("/api/fees/refresh", {});
    const fees = await api("/api/fees");
    renderFees(fees);
    toast("费率已刷新");
  } catch (e) { toast(e.message); } finally {
    $("btnRefreshFees").disabled = false;
  }
}

// ---------- 积分明细 tooltip ----------
let detailTimer = null;
let detailCache = {};

async function showCreditDetail(e, uid) {
  const el = e.currentTarget;
  if (detailTimer) { clearTimeout(detailTimer); detailTimer = null; }

  let d = detailCache[uid];
  if (!d) {
    try {
      d = await api("/api/account/resource_detail", { uid });
      detailCache[uid] = d;
    } catch (err) { return; }
  }
  if (!d || !d.items || !d.items.length) return;

  let html = `<table class="detail-table"><thead><tr><th>套餐</th><th>总额</th><th>已用</th><th>剩余</th></tr></thead><tbody>`;
  for (const it of d.items) {
    html += `<tr><td>${esc(it.name)}</td><td>${it.total}</td><td>${it.used}</td><td>${it.remain}</td></tr>`;
  }
  html += `</tbody></table>`;

  let tip = $("creditTip");
  if (!tip) {
    tip = document.createElement("div");
    tip.id = "creditTip";
    tip.className = "credit-tip";
    document.body.appendChild(tip);
  }
  tip.innerHTML = html;
  tip.style.display = "block";

  const rect = el.getBoundingClientRect();
  let left = rect.left, top = rect.bottom + 4;
  if (top + 200 > window.innerHeight) top = rect.top - 200;
  if (left + 280 > window.innerWidth) left = window.innerWidth - 290;
  tip.style.left = left + "px";
  tip.style.top = top + "px";
}

function hideCreditDetail() {
  detailTimer = setTimeout(() => {
    const tip = $("creditTip");
    if (tip) tip.style.display = "none";
  }, 200);
  const tip = $("creditTip");
  if (tip) {
    tip.onmouseenter = () => { if (detailTimer) { clearTimeout(detailTimer); detailTimer = null; } };
    tip.onmouseleave = () => { tip.style.display = "none"; };
  }
}

// ---------- 渲染 ----------
function render() {
  renderTopbar();
  renderAccounts();
  renderTimes();
}

function renderTopbar() {
  $("ver").textContent = "v" + state.version;
  $("serverLine").textContent = state.running ? "服务运行中" : "服务未启动";
  $("aboutVer").textContent = state.version;

  const host = (state.listen_host === "0.0.0.0" || state.listen_host === "" || state.listen_host === "::")
    ? "127.0.0.1" : state.listen_host;
  const apiURL = `http://${host}:${state.listen_port}/v1`;
  $("apiAddr").querySelector(".val").textContent = apiURL;

  const key = state.api_key;
  $("apiKeyDisplay").querySelector(".val").textContent = key === "" ? "（无鉴权）" : key;

  // 开机自启开关回写真实状态（注册表为准），避免页面刷新后总显示关闭
  $("chkAutostart").checked = !!state.autostart;
}

function renderAccounts() {
  const grid = $("acctList");
  const empty = $("acctEmpty");
  if (!state.accounts.length) {
    grid.innerHTML = "";
    empty.classList.remove("hidden");
    return;
  }
  empty.classList.add("hidden");

  grid.innerHTML = state.accounts.map((a) => {
    const gm = groupMeta(a.group);
    const group = gm.cls;
    const groupName = gm.name;
    const noCheckin = gm.checkin;

    const checkinTag = a.last_checkin_at
      ? `<span class="tag ${a.last_checkin_ok ? "ok" : "bad"}">${a.last_checkin_ok ? "签到成功" : "签到失败"}</span>`
      : (noCheckin ? '<span class="tag neutral">无签到</span>' : '<span class="tag neutral">未签到</span>');

    const disabledClass = a.disabled ? " disabled" : "";
    const disableIcon = a.disabled ? "▶" : "⏸";
    const disableTitle = a.disabled ? "启用" : "停用";
    const checkinBtn = noCheckin
      ? `<span class="icon-op off" title="${groupName} 无签到活动" onclick="return false">✓</span>`
      : `<span class="icon-op" title="签到" onclick="checkin('${a.uid}')">✓</span>`;
    const growthBtn = a.group === "workbuddy"
      ? `<span class="icon-op" title="成长中心（旅行/盲盒/任务领奖）" onclick="growth('${a.uid}')">🎁</span>`
      : "";

    return `
    <div class="acct-card${disabledClass}">
      <div class="acct-top">
        <div>
          <span class="badge ${group}">${groupName}</span>
          <span class="acct-name">${esc(a.nickname || shortUid(a.uid))}</span>
        </div>
        <div class="acct-ops">
          ${checkinBtn}
          ${growthBtn}
          <span class="icon-op" title="刷新积分" onclick="refreshOne('${a.uid}')">↻</span>
          <span class="icon-op warn" title="${disableTitle}" onclick="toggleDisable('${a.uid}',${a.disabled})">${disableIcon}</span>
          <span class="icon-op danger" title="删除账号" onclick="removeAcct('${a.uid}')">✕</span>
        </div>
      </div>
      <div class="acct-uid">UID: ${esc(shortUid(a.uid))}</div>
      <div class="acct-mid">
        <div class="acct-credits" onmouseenter="showCreditDetail(event,'${a.uid}')" onmouseleave="hideCreditDetail()">${a.credits}<span>积分</span></div>
        <div class="acct-checkin">${checkinTag}</div>
      </div>
    </div>`;
  }).join("");
}

function renderTimes() {
  const box = $("timesBox");
  box.innerHTML = (state.checkin_times || []).map((t) =>
    `<span class="time-chip" title="点击删除" onclick="delTime('${t}')">${t} ✕</span>`).join("");
  $("nextCheckin").textContent = state.next_checkin || "-";
}

// 渠道主题色（卡片渐变图标）
const feeThemes = {
  traework: ["#2563eb", "#38bdf8"],
  qoder:     ["#7c3aed", "#a855f7"],
  codebuddy: ["#0ea5e9", "#06b6d4"],
  wbintl:    ["#f59e0b", "#fbbf24"],
  zcode:     ["#10b981", "#34d399"],
  autoclaw:  ["#6366f1", "#818cf8"],
  workbuddy: ["#2563eb", "#3b82f6"],
};

function renderFees(fees) {
  const box = $("feesBox");
  const channels = fees.channels || [];

  if (channels.length === 0) {
    box.innerHTML = `<div class="note">${esc(fees.note || "")}</div>
      <div class="note">${esc(fees.disclaimer || "")}</div>`;
    return;
  }

  let html = `<div class="note">${esc(fees.note || "")}</div>`;
  if (fees.cached_at) html += `<div class="note">上次更新：${esc(fees.cached_at)}</div>`;
  if (fees.error) html += `<div class="note" style="color:var(--danger)">${esc(fees.error)}</div>`;

  html += `<div class="fees-grid">`;
  for (const ch of channels) {
    const gm = groupMeta(ch.channel);
    const theme = feeThemes[ch.channel] || feeThemes.workbuddy;
    const icon = esc((gm.name || ch.channel).slice(0, 2).toUpperCase());
    const models = ch.models || [];

    let rows = "";
    for (const m of models) {
      const rate = m.rate ? Number(m.rate) : 0;
      let badgeClass = "free";
      if (rate > 0 && rate <= 0.15) badgeClass = "low";
      else if (rate > 0.15 && rate <= 0.5) badgeClass = "normal";
      else if (rate > 0.5) badgeClass = "high";
      
      const rateDisplay = rate > 0 ? `x${rate.toFixed(2)}` : "限免";
      const noteHtml = m.note ? `<div class="fee-note-tag">${esc(m.note)}</div>` : "";
      
      // 模型名：长名字换行，但保持可读性（只分割一次）
      const modelName = esc(m.model);
      let modelCell = `<span>${modelName}</span>`;
      if (modelName.length > 30) {
        const idx = modelName.indexOf('-');
        if (idx > 0 && idx < modelName.length - 1) {
          modelCell = `<span style="display:inline-block;max-width:180px;overflow:hidden;text-overflow:ellipsis;">${modelName.substring(0, idx + 1)}</span><br><span style="opacity:0.7">${modelName.substring(idx + 1)}</span>`;
        }
      }
      
      rows += `<tr>
        <td class="fee-model-cell">${modelCell}</td>
        <td><span class="fee-rate-badge ${badgeClass}">${rateDisplay}</span></td>
      </tr>`;
    }
    
    if (!rows) {
      rows = `<tr><td colspan="2" class="empty-models">暂无模型</td></tr>`;
    }

    html += `<div class="fee-card">
      <div class="fee-card-head">
        <span class="fee-icon" style="background:linear-gradient(135deg,${theme[0]},${theme[1]})">${icon}</span>
        <span class="fee-card-name">${esc(gm.name)}</span>
      </div>
      <div class="fee-body">
        <table class="fee-table">
          <thead>
            <tr>
              <th>模型</th>
              <th style="width:100px">费率</th>
            </tr>
          </thead>
          <tbody>
            ${rows}
          </tbody>
        </table>
      </div>
    </div>`;
  }
  html += `</div>`;
  html += `<div class="note" style="margin-top:12px">${esc(fees.disclaimer || "")}</div>`;
  box.innerHTML = html;
}

// ---------- 账号操作 ----------
async function checkin(uid) {
  try {
    const r = await api("/api/account/checkin", { uid });
    toast(r.ok ? `签到成功：${r.msg}（剩余 ${r.remain}）` : `签到：${r.msg}`);
    loadState();
  } catch (e) { toast(e.message); }
}

async function refreshOne(uid) {
  try {
    const r = await api("/api/account/refresh", { uid });
    toast(`刷新成功：剩余积分 ${r.remain}`);
    loadState();
  } catch (e) { toast(e.message); }
}

async function growth(uid) {
  try {
    const r = await api("/api/account/growth", { uid });
    const parts = [];
    if (r.travel) parts.push("旅行：" + r.travel);
    if (r.lottery) parts.push("盲盒：" + r.lottery);
    if (r.tasks) parts.push("任务：" + r.tasks);
    toast(parts.join(" · ") || "成长中心执行完成");
    loadState();
  } catch (e) { toast(e.message); }
}

async function toggleDisable(uid, currentlyDisabled) {
  const action = currentlyDisabled ? "启用" : "停用";
  confirmDialog(`确定${action}该账号？${currentlyDisabled ? "" : "停用后路由不会分配给该账号。"}`, async () => {
    try {
      await api("/api/account/disable", { uid, disabled: !currentlyDisabled });
      toast(`账号已${action}`);
      loadState();
    } catch (e) { toast(e.message); }
  });
}

function removeAcct(uid) {
  confirmDialog("确定删除该账号？删除后需重新登录。", async () => {
    try {
      await api("/api/account/remove", { uid });
      toast("账号已删除");
      loadState();
    } catch (e) { toast(e.message); }
  });
}

// ---------- 批量签到（分组多选 + 实时进度） ----------
const ciSel = new Set(); // 已勾选账号 uid
let ciRun = false;

function parseMD(s) { // "MM-DD HH:MM" → 今年对应时间（无年份）
  const m = /^(\d{2})-(\d{2})[ ]+(\d{2}):(\d{2})/.exec(s || "");
  if (!m) return null;
  const d = new Date(new Date().getFullYear(), +m[1] - 1, +m[2], +m[3], +m[4]);
  return isNaN(d.getTime()) ? null : d;
}
function isTodayChecked(a) {
  if (!a.last_checkin_ok || !a.last_checkin_at) return false;
  const t = parseMD(a.last_checkin_at);
  if (!t) return false;
  const now = new Date();
  return t.getFullYear() === now.getFullYear() && t.getMonth() === now.getMonth() && t.getDate() === now.getDate();
}
function ciExpired(a) { // 已停用 或 已过到期时间
  if (a.disabled) return true;
  const t = parseMD(a.until);
  return !!t && t.getTime() <= Date.now();
}

function openCheckinDialog() {
  const accounts = state.accounts || [];
  if (!accounts.length) { toast("还没有账号"); return; }
  ciSel.clear();
  const groups = {};
  for (const a of accounts) {
    const g = a.group === "traework" ? "traework" : (a.group === "qoder" ? "qoder" : (a.group === "codebuddy" ? "codebuddy" : (a.group === "wbintl" ? "wbintl" : (a.group === "zcode" ? "zcode" : (a.group === "autoclaw" ? "autoclaw" : "workbuddy")))));
    (groups[g] = groups[g] || []).push(a);
  }
  let html = "";
  for (const g of ["workbuddy", "traework", "qoder", "codebuddy", "wbintl", "zcode", "autoclaw"]) {
    const list = groups[g];
    if (!list || !list.length) continue;
    const gName = groupMeta(g).name;
    html += `<div class="ci-group"><div class="ci-gname">${gName}</div>`;
    for (const a of list) {
      const noCheckin = a.group === "qoder" || a.group === "codebuddy" || a.group === "wbintl" || a.group === "zcode";
      if (!noCheckin) ciSel.add(a.uid);
      html += ciRowHtml(a, noCheckin);
    }
    html += `</div>`;
  }
  $("ciList").innerHTML = html;
  $("ciResults").innerHTML = "";
  $("ciProgress").classList.add("hidden");
  $("btnCiStart").disabled = false;
  ciRun = false;
  updateCiCount();
  $("checkinOverlay").classList.remove("hidden");
}

function ciRowHtml(a, noCheckin) {
  let tag;
  if (noCheckin) tag = `<span class="tag neutral">无签到</span>`;
  else if (isTodayChecked(a)) tag = `<span class="tag ok">今日已签</span>`;
  else if (a.last_checkin_at) tag = `<span class="tag ${a.last_checkin_ok ? "ok" : "bad"}">${a.last_checkin_ok ? "签到成功" : "签到失败"}</span>`;
  else tag = `<span class="tag neutral">未签到</span>`;
  let exp = "";
  if (a.disabled) exp = `<span class="tag danger">已停用</span>`;
  else if (a.until) exp = `<span class="tag warn">${esc(a.until)} 到期</span>`;
  const checked = noCheckin ? "" : "checked";
  return `<label class="ci-row${noCheckin ? " off" : ""}" title="${esc(a.uid)}">
    <input type="checkbox" data-uid="${esc(a.uid)}" ${checked} ${noCheckin ? "disabled" : ""} onchange="ciRowChg(this)">
    <span class="ci-name">${esc(a.nickname || shortUid(a.uid))}</span>
    <span class="ci-credits">${a.credits} 分</span>
    ${tag}${exp}
  </label>`;
}

function ciRowChg(el) {
  if (el.checked) ciSel.add(el.dataset.uid); else ciSel.delete(el.dataset.uid);
  updateCiCount();
}
function ciSyncChecks() {
  document.querySelectorAll("#ciList input[type=checkbox]").forEach((c) => {
    if (!c.disabled) c.checked = ciSel.has(c.dataset.uid);
  });
  updateCiCount();
}
function updateCiCount() {
  const n = document.querySelectorAll("#ciList input[type=checkbox]").length;
  $("ciCount").textContent = `已选 ${ciSel.size} / ${n}`;
}
function ciSetAll(on) {
  document.querySelectorAll("#ciList input[type=checkbox]").forEach((c) => {
    if (c.disabled) return;
    c.checked = on;
    if (on) ciSel.add(c.dataset.uid); else ciSel.delete(c.dataset.uid);
  });
  updateCiCount();
}
function ciInvertSel() {
  document.querySelectorAll("#ciList input[type=checkbox]").forEach((c) => {
    if (c.disabled) return;
    c.checked = !c.checked;
    if (c.checked) ciSel.add(c.dataset.uid); else ciSel.delete(c.dataset.uid);
  });
  updateCiCount();
}
function ciOnlyUnchecked() {
  for (const a of state.accounts || []) if (isTodayChecked(a)) ciSel.delete(a.uid);
  ciSyncChecks();
}
function ciSkipExpired() {
  for (const a of state.accounts || []) if (ciExpired(a)) ciSel.delete(a.uid);
  ciSyncChecks();
}

async function runBatchCheckin() {
  if (ciRun) return;
  const uids = [...ciSel]; // 顺序 = 勾选顺序
  if (!uids.length) { toast("请先选择要签到的账号"); return; }
  ciRun = true;
  $("btnCiStart").disabled = true;
  $("ciProgress").classList.remove("hidden");
  const box = $("ciResults");
  box.innerHTML = "";
  const total = uids.length;
  let ok = 0, already = 0, fail = 0;
  const nameOf = (uid) => {
    const a = (state.accounts || []).find((x) => x.uid === uid);
    return esc((a && (a.nickname || shortUid(uid))) || shortUid(uid));
  };
  for (let i = 0; i < total; i++) {
    const uid = uids[i];
    $("ciStatus").textContent = `正在签到 ${i + 1}/${total} …`;
    $("ciBar").style.width = `${(i / total) * 100}%`;
    let res;
    try { res = await api("/api/account/checkin", { uid }); }
    catch (e) { res = { uid, ok: false, msg: e.message }; }
    const kind = res.ok ? "ok" : ((res.msg || "").includes("已签") ? "already" : "fail");
    if (kind === "ok") ok++; else if (kind === "already") already++; else fail++;
    box.insertAdjacentHTML("beforeend",
      `<div class="ci-result ${kind}"><span class="ci-rname">${nameOf(uid)}</span><span class="ci-rmsg">${esc(res.msg || "")}${res.ok && res.has_remain !== false ? (res.remain !== undefined ? `（剩余 ${res.remain}）` : "") : ""}</span></div>`);
    box.scrollTop = box.scrollHeight;
    $("ciBar").style.width = `${((i + 1) / total) * 100}%`;
  }
  $("ciStatus").textContent = `完成：成功 ${ok} · 今日已签 ${already} · 失败 ${fail}`;
  ciRun = false;
  $("btnCiStart").disabled = false;
  loadState();
}
function closeCheckinDialog() {
  $("checkinOverlay").classList.add("hidden");
  if (ciRun) ciRun = false;
}

// ---------- 豆包保活续期 ----------
let dbBusy = false;
function renderDoubao(res) {
  const list = res.accounts || [];
  const sum = [];
  if (res.finished_at) sum.push(`执行时间 ${esc(res.finished_at)}`);
  if (list.length) sum.push(`有效 ${res.ok} · 失效 ${res.expired} · 异常 ${res.error}`);
  $("dbSummary").textContent = sum.join("　");
  if (!list.length) {
    $("dbList").innerHTML = `<div class="db-row error"><span class="db-uid">—</span><span class="db-detail">${esc(res.note || "暂无记录")}</span></div>`;
    return;
  }
  $("dbList").innerHTML = list.map((a) => {
    const flag = a.renewed ? `<span class="db-flag" style="color:var(--ok)">已续期</span>`
      : a.status === "ok" ? `<span class="db-flag" style="color:var(--ok)">有效</span>`
      : a.status === "expired" ? `<span class="db-flag" style="color:var(--danger)">已失效</span>`
      : `<span class="db-flag" style="color:var(--warn)">异常</span>`;
    const expire = a.sid_expire_at ? ` · 到期 ${esc(a.sid_expire_at)}` : "";
    return `<div class="db-row ${a.status}"><span class="db-uid">${esc(shortUid(a.user_id))}</span><span class="db-detail">${esc(a.detail)}${expire}</span>${flag}</div>`;
  }).join("");
}
async function openDoubaoDialog() {
  $("doubaoOverlay").classList.remove("hidden");
  $("dbSummary").textContent = "";
  $("dbList").innerHTML = `<div class="db-row error"><span class="db-detail">读取上次结果…</span></div>`;
  try {
    renderDoubao(await api("/api/doubao/renew_result"));
  } catch (e) {
    $("dbList").innerHTML = `<div class="db-row error"><span class="db-detail">${esc(e.message)}</span></div>`;
  }
}
async function runDoubaoRenew() {
  if (dbBusy) return;
  dbBusy = true;
  const btn = $("btnDbRenew");
  btn.disabled = true;
  btn.textContent = "保活中…";
  try {
    renderDoubao(await api("/api/doubao/renew", {}));
  } catch (e) {
    $("dbSummary").textContent = "";
    $("dbList").innerHTML = `<div class="db-row error"><span class="db-detail">${esc(e.message)}</span></div>`;
  } finally {
    dbBusy = false;
    btn.disabled = false;
    btn.textContent = "立即保活";
  }
}
function closeDoubaoDialog() { $("doubaoOverlay").classList.add("hidden"); }

async function refreshAll() {
  $("btnRefreshAll").disabled = true;
  try {
    const r = await api("/api/account/refresh_all", {});
    toast(r.busy ? "已有刷新任务进行中" : `积分刷新完成：成功 ${r.ok} / 失败 ${r.failed}`);
    loadState();
  } catch (e) { toast(e.message); } finally {
    $("btnRefreshAll").disabled = false;
  }
}

// ---------- 登录 ----------
let pendingChannel = null;
function promptLogin(channel) {
  pendingChannel = channel;
  const name = groupMeta(channel).name;
  $("lcTitle").textContent = "添加 " + name + " 账号";
  $("lcMsg").textContent = groupMeta(channel).checkin
    ? `点击「登录${name}」将打开浏览器窗口，请按照指示正常登录${name}账号，登录成功后关闭浏览器窗口即可。（${name} 渠道无签到活动，仅 API 转发）`
    : `点击「登录${name}」将打开浏览器窗口，请按照指示正常登录${name}账号，登录成功后关闭浏览器窗口即可。`;
  $("btnLoginConfirm").textContent = "登录" + name;
  $("loginConfirmOverlay").classList.remove("hidden");
}
function confirmLogin() {
  $("loginConfirmOverlay").classList.add("hidden");
  if (pendingChannel) startLogin(pendingChannel);
}

async function startLogin(channel) {
  try {
    const r = await api("/api/login/start", { channel });
    const url = r.auth_url;
    if (!url) { toast("无法获取登录链接"); return; }
    $("loginTitle").textContent = "添加 " + groupMeta(channel).name + " 账号";
    $("loginMsg").textContent = "请在浏览器新窗口中完成登录…";
    $("loginOverlay").classList.remove("hidden");
    $("btnCopyUrl").dataset.url = url;
    window.open(url, "_blank", "noopener,noreferrer");
    startLoginPoll();
  } catch (e) {
    toast(e.message);
  }
}

async function cancelLogin() {
  try {
    await api("/api/login/cancel", {});
    stopLoginPoll();
    $("loginOverlay").classList.add("hidden");
    toast("登录已取消");
  } catch (e) { toast(e.message); }
}

function copyUrl() {
  const url = $("btnCopyUrl").dataset.url;
  if (!url) { toast("暂无链接"); return; }
  navigator.clipboard.writeText(url).then(() => toast("链接已复制")).catch(() => toast("复制失败，请手动复制"));
}

// 登录轮询
let loginPoll = null;
function startLoginPoll() {
  stopLoginPoll();
  loginPoll = setInterval(async () => {
    try {
      const st = await api("/api/state");
      if (!st.login_busy) {
        stopLoginPoll();
        $("loginOverlay").classList.add("hidden");
        toast("登录完成，正在同步账号…");
        await loadState();
        refreshFees();
      }
    } catch (e) { /* 忽略 */ }
  }, 3000);
}
function stopLoginPoll() {
  if (loginPoll) { clearInterval(loginPoll); loginPoll = null; }
}

// ---------- 签到时间 ----------
function delTime(t) {
  const times = (state.checkin_times || []).filter((x) => x !== t);
  saveTimes(times);
}

function addTime() {
  const times = (state.checkin_times || []).slice();
  const now = new Date();
  const next = `${String(now.getHours()).padStart(2, "0")}:${String(now.getMinutes()).padStart(2, "0")}`;
  if (!times.includes(next)) times.push(next);
  saveTimes(times.sort());
}

async function saveTimes(times) {
  try {
    await api("/api/config/checkin_times", { times });
    toast("签到时间已更新");
    loadState();
  } catch (e) { toast(e.message); }
}

// ---------- 开机自启 ----------
async function toggleAutostart() {
  try {
    await api("/api/config/autostart", { on: $("chkAutostart").checked });
    toast("设置已保存");
  } catch (e) { toast(e.message); loadState(); }
}

// ---------- API 配置弹层 ----------
function openApiConfig() {
  $("inPort").value = state.listen_port;
  $("selHost").value = state.listen_host === "127.0.0.1" ? "127.0.0.1"
    : (state.listen_host === "0.0.0.0" || state.listen_host === "" || state.listen_host === "::") ? "0.0.0.0"
    : "__custom__";
  if ($("selHost").value === "__custom__") {
    $("inHost").value = state.listen_host;
    $("customHostRow").classList.remove("hidden");
  } else {
    $("customHostRow").classList.add("hidden");
  }
  $("apiConfigOverlay").classList.remove("hidden");
}

function closeApiConfig() {
  $("apiConfigOverlay").classList.add("hidden");
}

async function saveApiConfig() {
  let host = $("selHost").value;
  if (host === "__custom__") host = $("inHost").value.trim() || "127.0.0.1";
  const port = parseInt($("inPort").value, 10);
  try {
    await api("/api/config/listen", { host, port });
    toast("监听已切换");
    closeApiConfig();
    loadState();
  } catch (e) { toast(e.message); }
}

// ---------- API Key 弹层 ----------
function openApiKey() {
  $("keyInput").value = state.api_key;
  $("apiKeyOverlay").classList.remove("hidden");
  $("keyInput").focus();
}

function closeApiKey() {
  $("apiKeyOverlay").classList.add("hidden");
}

async function saveApiKey() {
  const key = $("keyInput").value.trim();
  try {
    await api("/api/config/api_key", { key });
    closeApiKey();
    toast("API-Key 已更新");
    loadState();
  } catch (e) { toast(e.message); }
}

// ---------- 复制到剪贴板 ----------
async function copyText(text, label) {
  try {
    await navigator.clipboard.writeText(text);
    toast(`${label}已复制到剪贴板`);
  } catch (e) {
    // 降级方案：execCommand
    const ta = document.createElement("textarea");
    ta.value = text;
    ta.style.position = "fixed";
    ta.style.opacity = "0";
    document.body.appendChild(ta);
    ta.select();
    try {
      document.execCommand("copy");
      toast(`${label}已复制到剪贴板`);
    } catch (err) {
      toast("复制失败，请手动复制");
    }
    document.body.removeChild(ta);
  }
}
function openHelp() { $("helpOverlay").classList.remove("hidden"); }
function closeHelp() { $("helpOverlay").classList.add("hidden"); }
function openAbout() { $("aboutOverlay").classList.remove("hidden"); }
function closeAbout() { $("aboutOverlay").classList.add("hidden"); }

// ---------- 通用确认框 ----------
function confirmDialog(msg, onOk) {
  $("confirmMsg").textContent = msg;
  $("confirmOverlay").classList.remove("hidden");
  $("btnConfirmOk").onclick = () => { $("confirmOverlay").classList.add("hidden"); onOk(); };
  $("btnConfirmCancel").onclick = () => $("confirmOverlay").classList.add("hidden");
}

// ---------- 事件绑定 ----------
function bind() {
  $("btnAddWB").onclick = () => promptLogin("workbuddy");
  $("btnAddTrae").onclick = () => promptLogin("traework");
  $("btnAddQoder").onclick = () => promptLogin("qoder");
  $("btnCheckinAll").onclick = openCheckinDialog;
  $("btnRefreshAll").onclick = refreshAll;
  $("btnAddTime").onclick = addTime;
  // 批量签到弹层
  $("btnCiStart").onclick = runBatchCheckin;
  $("btnCiClose").onclick = closeCheckinDialog;
  $("ciSelAll").onclick = () => ciSetAll(true);
  $("ciSelNone").onclick = () => ciSetAll(false);
  $("ciInvert").onclick = ciInvertSel;
  $("ciOnlyUnchecked").onclick = ciOnlyUnchecked;
  $("ciSkipExpired").onclick = ciSkipExpired;
  $("checkinOverlay").onclick = (e) => { if (e.target === $("checkinOverlay")) closeCheckinDialog(); };
  // 豆包保活弹层
  $("btnDoubao").onclick = openDoubaoDialog;
  $("btnDbRenew").onclick = runDoubaoRenew;
  $("btnDbClose").onclick = closeDoubaoDialog;
  $("doubaoOverlay").onclick = (e) => { if (e.target === $("doubaoOverlay")) closeDoubaoDialog(); };
  $("btnCopyUrl").onclick = copyUrl;
  $("btnCancelLogin").onclick = cancelLogin;
  $("btnRefreshFees").onclick = refreshFees;
  $("btnRefreshTokens").onclick = refreshTokens;
  $("chkAutostart").onchange = toggleAutostart;

  $("apiAddr").onclick = () => {
    const v = $("apiAddr").querySelector(".val").textContent;
    copyText(v, "OpenAI 接口地址");
  };
  $("apiKeyDisplay").onclick = () => {
    const v = $("apiKeyDisplay").querySelector(".val").textContent;
    if (v === "（无鉴权）") { toast("当前未设置 API-Key"); return; }
    copyText(v, "API-Key");
  };
  // 修改图标点击弹配置对话框（不触发复制）
  document.querySelectorAll(".icon-edit")[0].onclick = openApiConfig;
  document.querySelectorAll(".icon-edit")[1].onclick = openApiKey;

  $("btnHelp").onclick = openHelp;
  $("btnAbout").onclick = openAbout;
  $("btnHelpClose").onclick = closeHelp;
  $("btnAboutClose").onclick = closeAbout;
  
  // Token 用量统计
  $("btnUsageStats").onclick = openUsageStats;

  // 登录确认弹层
  $("btnLoginConfirm").onclick = confirmLogin;
  $("btnLoginConfirmCancel").onclick = () => $("loginConfirmOverlay").classList.add("hidden");
  $("loginConfirmOverlay").onclick = (e) => { if (e.target === $("loginConfirmOverlay")) $("loginConfirmOverlay").classList.add("hidden"); };

  $("btnApiSave").onclick = saveApiConfig;
  $("btnApiCancel").onclick = closeApiConfig;
  $("selHost").onchange = () => {
    $("customHostRow").classList.toggle("hidden", $("selHost").value !== "__custom__");
  };

  $("btnKeySave").onclick = saveApiKey;
  $("btnKeyCancel").onclick = closeApiKey;
  $("keyInput").addEventListener("keydown", (e) => { if (e.key === "Enter") saveApiKey(); });

  // 点击弹层空白处关闭
  $("apiConfigOverlay").onclick = (e) => { if (e.target === $("apiConfigOverlay")) closeApiConfig(); };
  $("apiKeyOverlay").onclick = (e) => { if (e.target === $("apiKeyOverlay")) closeApiKey(); };
  $("helpOverlay").onclick = (e) => { if (e.target === $("helpOverlay")) closeHelp(); };
  $("aboutOverlay").onclick = (e) => { if (e.target === $("aboutOverlay")) closeAbout(); };
}

// ---------- 初始化 ----------
(async function init() {
  bind();
  try {
    await loadState();
    await loadFees();
    await loadTokenStats();
  } catch (e) {
    toast("无法连接后台服务：" + e.message);
  }
})();

// ---------- Token 用量统计渲染 ----------
function renderTokenStats(data) {
  const container = $("tokenStats");
  if (!container || !data) return;

  const summary = data.summary || {};
  const channels = data.channels || {};

  // 总卡片
  let html = `
    <div class="totals-card">
      <div class="totals-label">累计总 Token 数</div>
      <div class="totals-value">${fmtTotal(summary.total_tokens || 0)}</div>
      <div class="totals-detail">
        <div class="item"><span class="dot" style="background:#2563eb"></span>输入 <b>${fmtTotal(summary.prompt_tokens || 0)}</b></div>
        <div class="item"><span class="dot" style="background:#16a34a"></span>输出 <b>${fmtTotal(summary.completion_tokens || 0)}</b></div>
        <div class="item"><span class="dot" style="background:#f59e0b"></span>缓存命中 <b>${fmtTotal(summary.cache_read_tokens || 0)}</b></div>
        <div class="item"><span class="dot" style="background:#dc2626"></span>缓存写入 <b>${fmtTotal(summary.cache_write_tokens || 0)}</b></div>
      </div>
    </div>
  `;

  // 渠道分组
  const channelNames = {
    workbuddy: "WorkBuddy",
    traework: "TraeWork",
    qoder: "Qoder",
    codebuddy: "CodeBuddy",
    wbintl: "WBIntl",
    zcode: "ZCode",
    autoclaw: "AutoClaw"
  };

  const channelColors = {
    workbuddy: "#7c3aed",
    traework: "#2563eb",
    qoder: "#0891b2",
    codebuddy: "#16a34a",
    wbintl: "#ea580c",
    zcode: "#6366f1",
    autoclaw: "#0d9488"
  };

  if (Object.keys(channels).length > 0) {
    html += '<div class="channels-grid">';
    for (const [kind, stats] of Object.entries(channels)) {
      const name = channelNames[kind] || kind;
      const color = channelColors[kind] || "#6b7280";
      html += `
        <div class="ch-card">
          <div class="ch-head">
            <div class="ch-icon" style="background:${color}">${name[0]}</div>
            <div class="ch-name">${name}</div>
          </div>
          <div class="ch-stats">
            <div class="ch-stat">
              <div class="v">${fmtTotal(stats.prompt || 0)}</div>
              <div class="l">输入</div>
            </div>
            <div class="ch-stat">
              <div class="v">${fmtTotal(stats.completion || 0)}</div>
              <div class="l">输出</div>
            </div>
            <div class="ch-stat">
              <div class="v">${fmtTotal(stats.cache_read || 0)}</div>
              <div class="l">缓存命中</div>
            </div>
            <div class="ch-stat">
              <div class="v">${fmtTotal(stats.cache_write || 0)}</div>
              <div class="l">缓存写入</div>
            </div>
          </div>
        </div>
      `;
    }
    html += '</div>';
  } else {
    html = '<div class="empty" style="text-align:center;padding:40px;color:var(--muted)">暂无 Token 统计数据</div>';
  }

  container.innerHTML = html;
}

function fmtTotal(n) {
  if (n >= 1e6) return (n / 1e6).toFixed(1).replace(/\.0$/, '') + 'M';
  if (n >= 1e3) return (n / 1e3).toFixed(1).replace(/\.0$/, '') + 'k';
  return String(n);
}

// ---------- Token 用量柱状图统计 ----------
let usageChart = null;

async function openUsageStats() {
  const overlay = $("usageOverlay");
  const select = $("usageAccountSelect");
  const btnRefresh = $("btnUsageRefresh");
  const btnClose = $("btnUsageClose");
  
  // 填充账号选择器
  select.innerHTML = '<option value="">选择账号...</option>';
  const accounts = state.accounts || [];
  accounts.forEach(a => {
    const opt = document.createElement('option');
    opt.value = a.uid;
    opt.textContent = `${a.kind}/${shortUid(a.uid)} ${a.nickname ? '(' + esc(a.nickname) + ')' : ''}`;
    select.appendChild(opt);
  });
  
  // 清空图表
  if (usageChart) {
    usageChart.destroy();
    usageChart = null;
  }
  
  // 事件绑定
  btnRefresh.onclick = async () => {
    const uid = select.value;
    if (!uid) { toast("请先选择账号"); return; }
    
    try {
      const data = await api("/api/usage");
      const account = (data.accounts || []).find(a => a.uid === uid);
      
      if (!account || !account.tokens || !account.tokens.length) {
        toast("该账号暂无 Token 统计数据");
        return;
      }
      
      renderUsageChart(account.tokens);
    } catch (e) {
      toast("加载失败：" + e.message);
    }
  };
  
  btnClose.onclick = () => {
    overlay.classList.add("hidden");
    if (usageChart) {
      usageChart.destroy();
      usageChart = null;
    }
  };
  
  overlay.classList.remove("hidden");
}

function renderUsageChart(tokens) {
  const canvas = $("usageChart");
  if (!canvas) return;
  
  // 按天排序（最近在前）
  const sorted = [...tokens].sort((a, b) => b.Day.localeCompare(a.Day));
  const labels = sorted.map(t => t.Day.slice(5)); // 只显示 MM-DD
  const promptData = sorted.map(t => t.prompt);
  const completionData = sorted.map(t => t.completion);
  const totalData = sorted.map(t => t.total);
  
  const ctx = canvas.getContext('2d');
  
  if (usageChart) {
    usageChart.destroy();
  }
  
  usageChart = new Chart(ctx, {
    type: 'bar',
    data: {
      labels: labels,
      datasets: [
        {
          label: '输入',
          data: promptData,
          backgroundColor: 'rgba(37, 99, 235, 0.8)',
          borderColor: 'rgba(37, 99, 235, 1)',
          borderWidth: 1
        },
        {
          label: '输出',
          data: completionData,
          backgroundColor: 'rgba(22, 163, 74, 0.8)',
          borderColor: 'rgba(22, 163, 74, 1)',
          borderWidth: 1
        }
      ]
    },
    options: {
      responsive: true,
      maintainAspectRatio: false,
      plugins: {
        legend: {
          position: 'top',
        },
        tooltip: {
          callbacks: {
            label: function(context) {
              let label = context.dataset.label || '';
              if (label) {
                label += ': ';
              }
              if (context.parsed.y !== null) {
                label += fmtTotal(context.parsed.y);
              }
              return label;
            }
          }
        }
      },
      scales: {
        x: {
          stacked: false,
          title: {
            display: true,
            text: '日期'
          }
        },
        y: {
          stacked: false,
          beginAtZero: true,
          ticks: {
            callback: function(value) {
              return fmtTotal(value);
            }
          },
          title: {
            display: true,
            text: 'Token 数量'
          }
        }
      }
    }
  });
}