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
    case "atria": return { cls: "atria", name: "Atria", checkin: false };
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

function formatExpire(expireAt) {
  if (!expireAt || expireAt <= 0) {
    return { text: "长期有效", cls: "expire-perm", full: "长期有效" };
  }
  const now = Math.floor(Date.now() / 1000);
  const diffSec = expireAt - now;
  const dStr = new Date(expireAt * 1000).toLocaleString();
  if (diffSec <= 0) {
    return { text: "已到期", cls: "expire-urgent", full: `已到期 (${dStr})` };
  }
  const days = Math.floor(diffSec / 86400);
  const hours = Math.floor((diffSec % 86400) / 3600);
  if (days === 0) {
    return { text: `${Math.max(1, hours)}小时后到期`, cls: "expire-urgent", full: `到期时间: ${dStr}` };
  } else if (days <= 3) {
    return { text: `${days}天后到期`, cls: "expire-urgent", full: `到期时间: ${dStr}` };
  } else if (days <= 7) {
    return { text: `${days}天后到期`, cls: "expire-warn", full: `到期时间: ${dStr}` };
  } else {
    return { text: `${days}天后到期`, cls: "expire-normal", full: `到期时间: ${dStr}` };
  }
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

    const exp = formatExpire(a.expire_at);
    const expireTag = `<span class="tag ${exp.cls}" title="${esc(exp.full)}">⏳ ${esc(exp.text)}</span>`;
    const nextPickTag = a.is_next
      ? `<span class="tag next-pick" title="ZeroGate 账号池优先调度候选 #1（快到期/低余额优先策略）">🎯 优先调度</span>`
      : "";

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
        <div style="display:flex;align-items:center;gap:6px;flex-wrap:wrap;">
          <span class="badge ${group}">${groupName}</span>
          <span class="acct-name">${esc(a.nickname || shortUid(a.uid))}</span>
          ${nextPickTag}
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
        <div class="acct-tags">
          ${expireTag}
          ${checkinTag}
        </div>
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
let pendingProvider = "zai";
function pickZcodeProvider(btn) {
  pendingProvider = btn.dataset.provider;
  document.querySelectorAll("#zcodeProviderRow .btn").forEach((b) => b.classList.remove("primary"));
  btn.classList.add("primary");
}
let wbtKind = "wbintl";
function openWbtDialog(channel) {
  wbtKind = channel === "codebuddy" ? "codebuddy" : "wbintl";
  $("wbtTitle").textContent = "添加 " + groupMeta(channel).name + " 账号（粘贴 token）";
  $("wbtToken").value = ""; $("wbtRefresh").value = ""; $("wbtName").value = ""; $("wbtMsg").textContent = "";
  $("wbtokenOverlay").classList.remove("hidden");
}
function closeWbtDialog() { $("wbtokenOverlay").classList.add("hidden"); }
async function wbtImport() {
  const token = $("wbtToken").value.trim();
  if (!token) { toast("请粘贴 accessToken"); return; }
  const domain = wbtKind === "codebuddy" ? "codebuddy.ai" : "workbuddy.ai";
  $("wbtConfirm").disabled = true;
  try {
    const r = await api("/api/wbintl/add", { domain, token, refresh_token: $("wbtRefresh").value.trim(), nickname: $("wbtName").value.trim() });
    closeWbtDialog();
    toast("导入成功：" + (r.nickname || r.uid));
    await loadState();
    refreshFees();
  } catch (e) { $("wbtMsg").textContent = "导入失败：" + e.message; }
  finally { $("wbtConfirm").disabled = false; }
}
let aclDeviceID = null;
async function openAutoclawDialog() {
  pendingChannel = null;
  aclDeviceID = null;
  $("aclPhone").value = "";
  $("aclCode").value = "";
  $("aclMsg").textContent = "";
  $("autoclawOverlay").classList.remove("hidden");
  $("aclPhone").focus();
}
function closeAutoclawDialog() {
  $("autoclawOverlay").classList.add("hidden");
}
async function aclSendCode() {
  const phone = $("aclPhone").value.trim();
  if (!phone) { toast("请先输入手机号"); return; }
  $("aclSend").disabled = true;
  try {
    await api("/api/autoclaw/send_code", { phone });
    $("aclMsg").textContent = "验证码已发送至 " + phone + "，请查收短信";
    $("aclCode").focus();
  } catch (e) { $("aclMsg").textContent = "发送失败：" + e.message; }
  finally { $("aclSend").disabled = false; }
}
async function aclConfirm() {
  const phone = $("aclPhone").value.trim();
  const code = $("aclCode").value.trim();
  if (!phone || !code) { toast("请输入手机号和验证码"); return; }
  $("aclConfirm").disabled = true;
  try {
    const r = await api("/api/autoclaw/login", { phone, code });
    closeAutoclawDialog();
    toast("AutoClaw 登录成功：" + (r.nickname || r.uid));
    await loadState();
    refreshFees();
  } catch (e) { $("aclMsg").textContent = "登录失败：" + e.message; }
  finally { $("aclConfirm").disabled = false; }
}
function promptLogin(channel) {
  pendingChannel = channel;
  const name = groupMeta(channel).name;
  if (channel === "autoclaw") { openAutoclawDialog(); return; }
  if (channel === "wbintl" || channel === "codebuddy") { openWbtDialog(channel); return; }
  const row = $("zcodeProviderRow");
  if (channel === "zcode") {
    pendingProvider = "zai";
    row.classList.remove("hidden");
    row.querySelectorAll(".btn").forEach((b) => b.classList.toggle("primary", b.dataset.provider === "zai"));
  } else {
    row.classList.add("hidden");
  }
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
    const r = await api("/api/login/start", { channel, provider: channel === "zcode" ? pendingProvider : undefined });
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
  if ($("btnUsageStats")) $("btnUsageStats").onclick = openUsageStats;

  // 登录确认弹层
  $("btnLoginConfirm").onclick = confirmLogin;
  $("btnLoginConfirmCancel").onclick = () => $("loginConfirmOverlay").classList.add("hidden");
  $("btnZcZai").onclick = () => pickZcodeProvider($("btnZcZai"));
  $("btnZcBigmodel").onclick = () => pickZcodeProvider($("btnZcBigmodel"));
  $("aclSend").onclick = () => void aclSendCode();
  $("aclConfirm").onclick = () => void aclConfirm();
  $("aclCancel").onclick = closeAutoclawDialog;
  $("wbtCancel").onclick = closeWbtDialog;
  $("wbtConfirm").onclick = () => void wbtImport();
  $("aclSend").onclick = () => void aclSendCode();
  $("aclConfirm").onclick = () => void aclConfirm();
  $("aclCancel").onclick = closeAutoclawDialog;
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

  // 新增 Tab 功能绑定
  if ($("btnRefreshAuditLogs")) $("btnRefreshAuditLogs").onclick = loadAuditLogs;
  if ($("btnRefreshTokens")) $("btnRefreshTokens").onclick = loadTokenAnalytics;
  if ($("btnCreateClient")) $("btnCreateClient").onclick = createClient;
  if ($("inClientName")) $("inClientName").addEventListener("keydown", (e) => { if (e.key === "Enter") createClient(); });
  if ($("btnAddRedirect")) $("btnAddRedirect").onclick = saveRedirect;
  if ($("btnResetRedirects")) $("btnResetRedirects").onclick = resetRedirects;
}

// ---------- Tab 导航控制 ----------
function initTabs() {
  const tabs = document.querySelectorAll(".nav-tab");
  const panes = document.querySelectorAll(".tab-pane");
  const savedTab = localStorage.getItem("zg_active_tab") || "dashboard";

  function switchTab(name) {
    tabs.forEach(t => t.classList.toggle("active", t.dataset.tab === name));
    panes.forEach(p => p.classList.toggle("active", p.id === `tab-${name}`));
    localStorage.setItem("zg_active_tab", name);
    if (name === "dashboard") loadDashboard();
    else if (name === "logs") loadAuditLogs();
    else if (name === "clients") loadClients();
    else if (name === "router") loadRedirects();
    else if (name === "usage") loadTokenAnalytics();
  }

  tabs.forEach(t => {
    t.addEventListener("click", () => switchTab(t.dataset.tab));
  });

  switchTab(savedTab);
}

// ---------- 概览看板数据 ----------
async function loadDashboard() {
  try {
    const sum = await api("/api/audit/summary");
    if ($("statReqs")) $("statReqs").textContent = sum.requests_today || 0;
    if ($("statTokens")) $("statTokens").textContent = fmtTotal(sum.tokens_today || 0);
    if ($("statFails")) $("statFails").textContent = sum.fails_today || 0;

    let activeAccs = 0;
    const channels = {};
    if (state && state.accounts) {
      state.accounts.forEach(a => {
        if (!a.disabled && (!a.cooldown || a.cooldown === "none" || a.cooldown === "")) {
          activeAccs++;
        }
        channels[a.kind] = (channels[a.kind] || 0) + 1;
      });
    }
    if ($("statAccounts")) $("statAccounts").textContent = activeAccs;

    const badgeBox = $("channelBadges");
    if (badgeBox) {
      const keys = Object.keys(channels);
      badgeBox.innerHTML = keys.length ? keys.map(k => {
        const meta = groupMeta(k);
        return `<span class="badge ${meta.cls}" style="font-size:12px; padding:4px 10px;">${meta.name}: ${channels[k]} 个账号</span>`;
      }).join("") : `<span class="tag neutral">暂无账号</span>`;
    }

    const host = state ? state.listen_host : "127.0.0.1";
    const port = state ? state.listen_port : 7863;
    const key = state ? state.api_key : "WildWorkAPI";
    const dispHost = (host === "0.0.0.0" || host === "" || host === "::") ? "127.0.0.1" : host;
    if ($("quickBaseUrl")) $("quickBaseUrl").textContent = `http://${dispHost}:${port}/v1`;
    if ($("quickApiKey")) $("quickApiKey").textContent = key || "（无鉴权）";
  } catch (e) { /* ignore */ }
}

// ---------- 实时调用审计流水 ----------
let auditLogsTimer = null;

async function loadAuditLogs() {
  try {
    const data = await api("/api/audit/logs?limit=100");
    const tbody = $("auditLogsBody");
    if (!tbody) return;
    const logs = data.logs || [];
    if (!logs.length) {
      tbody.innerHTML = `<tr><td colspan="8" style="text-align:center; color:var(--muted); padding:20px;">暂无调用记录</td></tr>`;
      return;
    }
    tbody.innerHTML = logs.map(l => {
      const timeStr = l.time ? l.time.slice(11, 19) : "-";
      const statusCls = l.status === 200 ? "s200" : (l.status === 429 ? "s429" : "sErr");
      const errTooltip = l.error ? `title="${esc(l.error)}"` : "";
      const streamTag = l.stream ? `<span class="tag neutral" style="font-size:9px; padding:1px 4px; margin-left:2px;">流</span>` : "";
      return `
        <tr>
          <td style="font-family:ui-monospace, monospace; font-size:11px; color:var(--muted);">${timeStr}</td>
          <td><strong>${esc(l.client || "Default")}</strong></td>
          <td><code>${esc(l.model_req || "-")}</code>${streamTag}</td>
          <td><span class="badge ${groupMeta(l.channel).cls}" style="margin-right:4px;">${esc(l.channel || "-")}</span><code>${esc(l.model_target || "-")}</code> <span style="font-size:11px; color:var(--muted)">(${esc(shortUid(l.uid))})</span></td>
          <td>${l.duration_ms}ms</td>
          <td>${fmtTotal(l.total_tokens || 0)} <span style="font-size:11px; color:var(--muted)">(${fmtTotal(l.prompt_tokens || 0)}/${fmtTotal(l.completion_tokens || 0)})</span></td>
          <td><span class="status-pill ${statusCls}">${l.status}</span></td>
          <td ${errTooltip} style="max-width:180px; overflow:hidden; text-overflow:ellipsis; white-space:nowrap; font-size:11px; color:${l.error ? 'var(--danger)' : 'var(--muted)'};">${esc(l.error || "成功")}</td>
        </tr>
      `;
    }).join("");
  } catch (e) { /* ignore */ }
}

function setupAuditLogsPolling() {
  clearInterval(auditLogsTimer);
  auditLogsTimer = setInterval(() => {
    const activeTab = localStorage.getItem("zg_active_tab");
    if (activeTab === "logs" && $("chkAutoRefreshLogs") && $("chkAutoRefreshLogs").checked) {
      loadAuditLogs();
    }
  }, 5000);
}

// ---------- 客户端 Key 管理 ----------
async function loadClients() {
  try {
    const res = await api("/api/clients");
    const tbody = $("clientsBody");
    if (!tbody) return;
    const clients = res.clients || [];
    if (!clients.length) {
      tbody.innerHTML = `<tr><td colspan="6" style="text-align:center; color:var(--muted); padding:20px;">暂未创建客户端 Key（可直接使用默认主 Key，或点击上方创建专属 Key）</td></tr>`;
      return;
    }
    tbody.innerHTML = clients.map(c => {
      const lastUsed = c.last_used_at && !c.last_used_at.startsWith("0001") ? c.last_used_at.slice(5, 16).replace("T", " ") : "从未";
      return `
        <tr>
          <td><strong>${esc(c.name)}</strong></td>
          <td><span class="client-key-code" onclick="copyText('${esc(c.key)}', '${esc(c.name)} Key')" title="点击复制">${esc(c.key.slice(0, 10))}...${esc(c.key.slice(-4))} 📋</span></td>
          <td><b>${fmtTotal(c.total_tokens || 0)}</b></td>
          <td style="font-size:12px; color:var(--muted)">${lastUsed}</td>
          <td>
            <label class="switch" style="transform:scale(0.8); margin:0;">
              <input type="checkbox" ${c.enabled ? 'checked' : ''} onchange="toggleClient('${esc(c.key)}', this.checked)">
              <span class="slider"></span>
            </label>
          </td>
          <td>
            <button class="btn small danger" onclick="deleteClient('${esc(c.key)}')">删除</button>
          </td>
        </tr>
      `;
    }).join("");
  } catch (e) {
    toast("加载客户端失败：" + e.message);
  }
}

window.toggleClient = async function(key, enabled) {
  try {
    await api("/api/clients/toggle", { key, enabled });
    toast(enabled ? "已启用" : "已停用");
  } catch (e) {
    toast("操作失败：" + e.message);
    loadClients();
  }
};

window.deleteClient = function(key) {
  confirmDialog("确定删除该客户端 Key？删除后使用该 Key 的客户端将无法调用", async () => {
    try {
      await api("/api/clients/delete", { key });
      toast("已删除");
      loadClients();
    } catch (e) {
      toast("删除失败：" + e.message);
    }
  });
};

async function createClient() {
  const name = $("inClientName").value.trim();
  if (!name) {
    toast("请输入客户端标签（如 Cursor）");
    return;
  }
  try {
    const k = await api("/api/clients/create", { name });
    $("inClientName").value = "";
    toast("客户端 Key 创建成功");
    copyText(k.key, "新生成的 Key");
    loadClients();
  } catch (e) {
    toast("创建失败：" + e.message);
  }
}

// ---------- 模型路由别名管理 ----------
let currentRedirects = {};

async function loadRedirects() {
  try {
    const res = await api("/api/router/redirects");
    currentRedirects = res.redirects || {};
    renderRedirects();
  } catch (e) {
    toast("加载路由规则失败：" + e.message);
  }
}

function renderRedirects() {
  const box = $("redirectsList");
  if (!box) return;
  const keys = Object.keys(currentRedirects).sort();
  if (!keys.length) {
    box.innerHTML = `<div class="empty" style="text-align:center; padding:20px; color:var(--muted)">暂无重定向规则，请求必须包含渠道前缀</div>`;
    return;
  }
  box.innerHTML = keys.map(k => {
    const cands = currentRedirects[k] || [];
    const candTags = cands.map((c, i) => `
      <span class="cand-tag">${esc(c)}</span>
      ${i < cands.length - 1 ? '<span class="cand-arrow">➔</span>' : ''}
    `).join("");
    return `
      <div class="router-item">
        <div class="router-model-name"><code>${esc(k)}</code></div>
        <div class="router-candidates">${candTags}</div>
        <div class="panel-actions">
          <button class="btn small" onclick="editRedirect('${esc(k)}')">编辑</button>
          <button class="btn small danger" onclick="deleteRedirect('${esc(k)}')">删除</button>
        </div>
      </div>
    `;
  }).join("");
}

window.editRedirect = function(name) {
  const cands = currentRedirects[name] || [];
  $("inAliasName").value = name;
  $("inAliasTargets").value = cands.join(", ");
  $("inAliasName").focus();
};

window.deleteRedirect = async function(name) {
  delete currentRedirects[name];
  try {
    await api("/api/router/redirects", { redirects: currentRedirects });
    toast("规则已删除");
    renderRedirects();
  } catch (e) {
    toast("保存失败：" + e.message);
  }
};

async function saveRedirect() {
  const name = $("inAliasName").value.trim();
  const rawTargets = $("inAliasTargets").value.trim();
  if (!name || !rawTargets) {
    toast("请填写标准模型名和候选渠道");
    return;
  }
  const targets = rawTargets.split(",").map(s => s.trim()).filter(Boolean);
  currentRedirects[name] = targets;
  try {
    await api("/api/router/redirects", { redirects: currentRedirects });
    toast("规则已保存");
    $("inAliasName").value = "";
    $("inAliasTargets").value = "";
    renderRedirects();
  } catch (e) {
    toast("保存失败：" + e.message);
  }
}

async function resetRedirects() {
  confirmDialog("确定恢复为系统预置的默认模型映射规则？", async () => {
    const defaults = {
      "claude-3-5-sonnet": ["traework/claude-3-5-sonnet", "wbintl/claude-3-5-sonnet", "workbuddy/claude-3.5-sonnet"],
      "claude-3.5-sonnet": ["traework/claude-3-5-sonnet", "wbintl/claude-3-5-sonnet", "workbuddy/claude-3.5-sonnet"],
      "claude-3-7-sonnet": ["wbintl/claude-3-7-sonnet", "traework/claude-3-7-sonnet"],
      "claude-3.7-sonnet": ["wbintl/claude-3-7-sonnet", "traework/claude-3-7-sonnet"],
      "claude-3-5-haiku": ["wbintl/claude-3-5-haiku", "workbuddy/claude-3.5-haiku"],
      "claude-3.5-haiku": ["wbintl/claude-3-5-haiku", "workbuddy/claude-3.5-haiku"],
      "deepseek-chat": ["workbuddy/deepseek-v4.1-flash", "traework/glm-5.2"],
      "deepseek-v3": ["workbuddy/deepseek-v4.1-flash", "traework/glm-5.2"],
      "deepseek-reasoner": ["workbuddy/deepseek-v4.1-flash", "traework/glm-5.2"],
      "deepseek-r1": ["workbuddy/deepseek-v4.1-flash", "traework/glm-5.2"],
      "gpt-4o": ["qoder/gpt-4o", "traework/glm-5.2"],
      "qwen-2.5-coder": ["traework/qwen3-coder", "workbuddy/glm-5.2"]
    };
    currentRedirects = defaults;
    try {
      await api("/api/router/redirects", { redirects: currentRedirects });
      toast("已恢复默认映射");
      renderRedirects();
    } catch (e) {
      toast("保存失败：" + e.message);
    }
  });
}

// ---------- 初始化 ----------
(async function init() {
  initTabs();
  bind();
  setupAuditLogsPolling();
  try {
    await loadState();
    await loadDashboard();
    await loadFees();
    await loadTokenStats();
  } catch (e) {
    toast("无法连接后台服务：" + e.message);
  }
})();

// ============================================================
// Token 资产大屏与高级图表引擎 (纯原生 SVG，零依赖，高颜值)
// ============================================================

const CHANNEL_METAS = {
  workbuddy: { name: "WorkBuddy", color: "#7c3aed" },
  traework:  { name: "TraeWork",  color: "#2563eb" },
  qoder:     { name: "Qoder",     color: "#0891b2" },
  codebuddy: { name: "CodeBuddy", color: "#16a34a" },
  wbintl:    { name: "WBIntl",    color: "#ea580c" },
  zcode:     { name: "ZCode",     color: "#6366f1" },
  autoclaw:  { name: "AutoClaw",  color: "#0d9488" },
  atria:     { name: "Atria",     color: "#0284c7" },
};

function fmtTokens(n) {
  if (n == null || isNaN(n)) return "0";
  n = Number(n);
  if (n >= 1e9) return (n / 1e9).toFixed(2).replace(/\.00$/, "") + " B";
  if (n >= 1e6) return (n / 1e6).toFixed(2).replace(/\.00$/, "") + " M";
  if (n >= 1e3) return (n / 1e3).toFixed(1).replace(/\.0$/, "") + " k";
  return n.toLocaleString();
}

function fmtTotal(n) {
  return fmtTokens(n);
}

async function loadTokenStats() {
  return loadTokenAnalytics();
}

async function loadTokenAnalytics() {
  try {
    const [tokenData, auditAnalytics, usageData] = await Promise.all([
      api("/api/token_usage").catch(() => ({})),
      api("/api/audit/analytics").catch(() => ({})),
      api("/api/usage").catch(() => ({ accounts: [] })),
    ]);

    renderTokenHero(tokenData.summary || {});
    renderTokenTrend(auditAnalytics.daily_trend || [], usageData.accounts || []);
    renderChannelDonut(tokenData.channels || auditAnalytics.channel_tokens || {});
    renderTopModels(auditAnalytics.top_models || []);
    renderTopClients(auditAnalytics.top_clients || []);
    renderAcctProductivity(usageData.accounts || []);
  } catch (e) {
    console.error("loadTokenAnalytics error:", e);
  }
}

// 1. Hero 财富看板与价值折算
function renderTokenHero(summary) {
  const total = Number(summary.total_tokens || 0);
  const prompt = Number(summary.prompt_tokens || 0);
  const comp = Number(summary.completion_tokens || 0);
  const cache = Number(summary.cache_read_tokens || 0);

  if ($("tkHeroTotal")) $("tkHeroTotal").textContent = fmtTokens(total);
  if ($("tkHeroPrompt")) $("tkHeroPrompt").textContent = fmtTokens(prompt);
  if ($("tkHeroComp")) $("tkHeroComp").textContent = fmtTokens(comp);
  if ($("tkHeroCache")) $("tkHeroCache").textContent = fmtTokens(cache);

  const promptPct = total > 0 ? Math.round((prompt / total) * 100) : 0;
  const compPct = total > 0 ? Math.round((comp / total) * 100) : 0;
  const cacheRate = (prompt + cache) > 0 ? Math.round((cache / (prompt + cache)) * 100) : 0;

  if ($("tkHeroPromptPct")) $("tkHeroPromptPct").textContent = `占比 ${promptPct}%`;
  if ($("tkHeroCompPct")) $("tkHeroCompPct").textContent = `占比 ${compPct}%`;
  if ($("tkHeroCacheRate")) $("tkHeroCacheRate").textContent = `命中率 ${cacheRate}%`;

  // 估算节省金额：市场商业云平均价（以 Claude 3.5 / DeepSeek 混合基准）
  // 输入约 $3/M (~￥21/M), 输出约 $15/M (~￥105/M), 缓存读取约 $0.3/M (~￥2.1/M)
  const savingsYuan = (prompt * 0.000021 + comp * 0.000105 + cache * 0.000003).toFixed(2);
  const savingsUsd = (savingsYuan / 7.2).toFixed(2);
  if ($("tkHeroSavings")) {
    $("tkHeroSavings").textContent = `约为你白嫖节省: ￥${savingsYuan} (约 $${savingsUsd})`;
  }
}

// 2. 近 14 天 Token 每日消耗趋势（SVG 堆叠柱状图）
function renderTokenTrend(auditDaily, accounts) {
  const wrap = $("tokenTrendChartWrap");
  if (!wrap) return;

  const dayMap = {};
  
  // 来源 A: auditDaily
  (auditDaily || []).forEach(d => {
    if (!d.day) return;
    dayMap[d.day] = dayMap[d.day] || { prompt: 0, comp: 0, cache: 0, total: 0 };
    dayMap[d.day].prompt += Number(d.prompt || 0);
    dayMap[d.day].comp += Number(d.completion || 0);
    dayMap[d.day].cache += Number(d.cache || 0);
    dayMap[d.day].total += Number(d.total || 0);
  });

  // 来源 B: accounts.tokens
  (accounts || []).forEach(a => {
    (a.tokens || []).forEach(t => {
      if (!t.day) return;
      dayMap[t.day] = dayMap[t.day] || { prompt: 0, comp: 0, cache: 0, total: 0 };
      dayMap[t.day].prompt = Math.max(dayMap[t.day].prompt, Number(t.prompt || 0));
      dayMap[t.day].comp = Math.max(dayMap[t.day].comp, Number(t.completion || 0));
      dayMap[t.day].cache = Math.max(dayMap[t.day].cache, Number(t.cache_read || 0));
      dayMap[t.day].total = Math.max(dayMap[t.day].total, Number(t.total || 0));
    });
  });

  // 生成近 14 天序列
  const days = [];
  const now = new Date();
  for (let i = 13; i >= 0; i--) {
    const d = new Date(now.getTime() - i * 86400 * 1000);
    const key = d.toISOString().slice(0, 10);
    const item = dayMap[key] || { prompt: 0, comp: 0, cache: 0, total: 0 };
    days.push({ day: key, label: key.slice(5), ...item });
  }

  const maxTotal = Math.max(...days.map(d => d.total + d.cache), 1000);

  const W = 740, H = 220;
  const padLeft = 55, padRight = 15, padTop = 20, padBottom = 28;
  const chartW = W - padLeft - padRight;
  const chartH = H - padTop - padBottom;

  let gridLines = "";
  for (let step = 0; step <= 4; step++) {
    const yVal = Math.round((maxTotal / 4) * step);
    const y = padTop + chartH - (step / 4) * chartH;
    gridLines += `
      <line x1="${padLeft}" y1="${y}" x2="${W - padRight}" y2="${y}" stroke="#e2e8f0" stroke-dasharray="4" />
      <text x="${padLeft - 8}" y="${y + 4}" text-anchor="end" font-size="10" fill="#94a3b8">${fmtTokens(yVal)}</text>
    `;
  }

  const stepW = chartW / days.length;
  const barW = Math.max(16, Math.min(30, stepW * 0.62));
  let bars = "";

  days.forEach((d, i) => {
    const x = padLeft + i * stepW + (stepW - barW) / 2;
    const pHeight = (d.prompt / maxTotal) * chartH;
    const cHeight = (d.comp / maxTotal) * chartH;
    const kHeight = (d.cache / maxTotal) * chartH;

    const pY = padTop + chartH - pHeight;
    const cY = pY - cHeight;
    const kY = cY - kHeight;

    const tip = `${d.day}\n总计: ${fmtTokens(d.total + d.cache)}\n输入: ${fmtTokens(d.prompt)}\n输出: ${fmtTokens(d.comp)}\n缓存: ${fmtTokens(d.cache)}`;

    bars += `
      <g class="chart-bar-group">
        <title>${esc(tip)}</title>
        <rect x="${x}" y="${pY}" width="${barW}" height="${pHeight}" fill="#3b82f6" rx="2" />
        <rect x="${x}" y="${cY}" width="${barW}" height="${cHeight}" fill="#10b981" rx="2" />
        <rect x="${x}" y="${kY}" width="${barW}" height="${kHeight}" fill="#f59e0b" rx="2" />
        <text x="${x + barW / 2}" y="${H - 10}" text-anchor="middle" font-size="10" fill="#64748b">${d.label}</text>
      </g>
    `;
  });

  wrap.innerHTML = `
    <svg viewBox="0 0 ${W} ${H}" style="width:100%; height:100%; display:block;" preserveAspectRatio="xMidYMid meet">
      ${gridLines}
      ${bars}
    </svg>
  `;
}

// 3. 白嫖渠道贡献占比（SVG 环形 Donut 图）
function renderChannelDonut(channelTokens) {
  const wrap = $("channelDonutWrap");
  if (!wrap) return;

  let total = 0;
  const items = [];
  for (const [k, val] of Object.entries(channelTokens)) {
    const tokens = typeof val === "object" ? Number(val.total_tokens || 0) : Number(val || 0);
    if (tokens > 0) {
      total += tokens;
      const meta = CHANNEL_METAS[k] || { name: k, color: "#64748b" };
      items.push({ kind: k, name: meta.name, color: meta.color, tokens });
    }
  }

  if (total === 0 || items.length === 0) {
    wrap.innerHTML = `<div class="empty-chart">暂无各渠道 Token 消耗记录</div>`;
    return;
  }

  items.sort((a, b) => b.tokens - a.tokens);

  const cx = 95, cy = 95, r = 68;
  const C = 2 * Math.PI * r;
  let offset = 0;
  let circles = "";
  let legend = "";

  items.forEach(it => {
    const pct = ((it.tokens / total) * 100).toFixed(1);
    const dash = (it.tokens / total) * C;
    circles += `
      <circle cx="${cx}" cy="${cy}" r="${r}" fill="transparent"
        stroke="${it.color}" stroke-width="26"
        stroke-dasharray="${dash} ${C - dash}" stroke-dashoffset="${-offset}"
        transform="rotate(-90 ${cx} ${cy})">
        <title>${esc(it.name)}: ${fmtTokens(it.tokens)} (${pct}%)</title>
      </circle>
    `;
    offset += dash;

    legend += `
      <div class="donut-legend-item">
        <div class="left">
          <span class="donut-legend-dot" style="background:${it.color}"></span>
          <span>${esc(it.name)}</span>
        </div>
        <div class="donut-legend-val">${pct}% <span style="font-size:11px;color:var(--muted);font-weight:400">(${fmtTokens(it.tokens)})</span></div>
      </div>
    `;
  });

  wrap.innerHTML = `
    <div style="width:190px; height:190px; position:relative; flex-shrink:0;">
      <svg viewBox="0 0 190 190" style="width:100%; height:100%;">
        <circle cx="${cx}" cy="${cy}" r="${r}" fill="transparent" stroke="#f1f5f9" stroke-width="26" />
        ${circles}
        <text x="${cx}" y="${cy - 4}" text-anchor="middle" font-size="16" font-weight="700" fill="var(--text)">${fmtTokens(total)}</text>
        <text x="${cx}" y="${cy + 14}" text-anchor="middle" font-size="11" fill="var(--muted)">总产出</text>
      </svg>
    </div>
    <div class="donut-legend">
      ${legend}
    </div>
  `;
}

// 4. 热门模型白嫖榜 TOP 5
function renderTopModels(models) {
  const box = $("topModelsList");
  if (!box) return;
  if (!models || models.length === 0) {
    box.innerHTML = `<div class="empty-hint">暂无模型调用数据</div>`;
    return;
  }
  const top5 = models.slice(0, 5);
  const max = top5[0].total_tokens || 1;
  const badges = ["top1", "top2", "top3", "", ""];

  box.innerHTML = top5.map((m, idx) => {
    const pct = Math.max(8, Math.round((m.total_tokens / max) * 100));
    return `
      <div class="rank-item">
        <span class="rank-num ${badges[idx]}">${idx + 1}</span>
        <div class="rank-info">
          <div class="rank-header">
            <span class="rank-name" title="${esc(m.model)}">${esc(m.model)}</span>
            <span class="rank-val">${fmtTokens(m.total_tokens)} <span style="font-weight:400;color:var(--muted)">(${m.req_count || 0}次)</span></span>
          </div>
          <div class="rank-bar-bg">
            <div class="rank-bar-fill" style="width:${pct}%"></div>
          </div>
        </div>
      </div>
    `;
  }).join("");
}

// 5. 客户端排行 TOP 5
function renderTopClients(clients) {
  const box = $("topClientsList");
  if (!box) return;
  if (!clients || clients.length === 0) {
    box.innerHTML = `<div class="empty-hint">暂无客户端调用数据</div>`;
    return;
  }
  const top5 = clients.slice(0, 5);
  const max = top5[0].total_tokens || 1;
  const badges = ["top1", "top2", "top3", "", ""];

  box.innerHTML = top5.map((c, idx) => {
    const pct = Math.max(8, Math.round((c.total_tokens / max) * 100));
    return `
      <div class="rank-item">
        <span class="rank-num ${badges[idx]}">${idx + 1}</span>
        <div class="rank-info">
          <div class="rank-header">
            <span class="rank-name">${esc(c.client || "Default")}</span>
            <span class="rank-val">${fmtTokens(c.total_tokens)} <span style="font-weight:400;color:var(--muted)">(${c.req_count || 0}次)</span></span>
          </div>
          <div class="rank-bar-bg">
            <div class="rank-bar-fill" style="width:${pct}%; background:linear-gradient(90deg, #10b981, #34d399)"></div>
          </div>
        </div>
      </div>
    `;
  }).join("");
}

// 6. 账号 Token 生产力榜单
function renderAcctProductivity(accounts) {
  const tbody = $("acctProductivityBody");
  if (!tbody) return;
  if (!accounts || accounts.length === 0) {
    tbody.innerHTML = `<tr><td colspan="7" style="text-align:center; padding:20px; color:var(--muted)">暂无账号生产力数据</td></tr>`;
    return;
  }

  const list = [...accounts].map(a => {
    let sumPrompt = 0, sumComp = 0, sumCache = 0, sumTotal = 0;
    (a.tokens || []).forEach(t => {
      sumPrompt += Number(t.prompt || 0);
      sumComp += Number(t.completion || 0);
      sumCache += Number(t.cache_read || 0);
      sumTotal += Number(t.total || 0);
    });
    const hitRate = (sumPrompt + sumCache) > 0 ? Math.round((sumCache / (sumPrompt + sumCache)) * 100) : null;
    return { ...a, sumTotal, sumCache, hitRate };
  });

  list.sort((a, b) => b.sumTotal - a.sumTotal);

  tbody.innerHTML = list.map(a => {
    const gm = groupMeta(a.kind);
    const exp = formatExpire(a.expire_at);
    const expireTag = `<span class="tag ${exp.cls}">⏳ ${esc(exp.text)}</span>`;
    const stateAcct = (state && state.accounts ? state.accounts : []).find(s => s.uid === a.uid && s.group === a.kind);
    const isNext = stateAcct && stateAcct.is_next;
    const statusTag = isNext
      ? `<span class="tag next-pick">🎯 优先调度 #1</span>`
      : (stateAcct && stateAcct.disabled ? `<span class="tag danger">已停用</span>` : `<span class="tag neutral">就绪待命</span>`);

    return `
      <tr>
        <td>
          <span class="badge ${gm.cls}" style="margin-right:6px;">${gm.name}</span>
          <b>${esc(a.nickname || shortUid(a.uid))}</b>
        </td>
        <td><code style="font-size:11px;color:var(--muted)">${esc(shortUid(a.uid))}</code></td>
        <td><b>${fmtTokens(a.sumTotal)}</b></td>
        <td>${fmtTokens(a.today || 0)}</td>
        <td>${a.hitRate !== null ? a.hitRate + '%' : '—'}</td>
        <td>${expireTag}</td>
        <td>${statusTag}</td>
      </tr>
    `;
  }).join("");
}