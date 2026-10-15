// ZeroGate 管理面板前端（原生 JS，无构建步骤，直接 fetch 管理 API）
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

// ---------- 图表取色（读 CSS 变量，双主题自动适配） ----------
function cssVar(name) {
  return window.__cssVar ? window.__cssVar(name) : "";
}
function chColor(kind) {
  const v = cssVar("--ch-" + kind);
  if (v) return v;
  const fb = { workbuddy: "#7c3aed", traework: "#2563eb", qoder: "#0891b2", codebuddy: "#16a34a", zcode: "#6366f1", autoclaw: "#0d9488", atria: "#0284c7", copilot: "#475569", doubao: "#dc2626", gptgemini: "#db2777", glmflash: "#65a30d", groq: "#f59e0b", tierflow: "#d946ef" };
  return fb[kind] || "#64748b";
}

// groupMeta 返回渠道分组的展示元数据：{cls, name, checkin}。
function groupMeta(g) {
  switch (g) {
    case "traework": return { cls: "trae", name: "TraeWork", checkin: false };
    case "qoder": return { cls: "qoder", name: "Qoder", checkin: false };
    case "codebuddy": return { cls: "cb", name: "CodeBuddy", checkin: false };
    case "zcode": return { cls: "zc", name: "ZCode (Z.AI)", checkin: false };
    case "autoclaw": return { cls: "ac", name: "AutoClaw", checkin: true };
    case "atria": return { cls: "atria", name: "Atria", checkin: false };
    case "copilot": return { cls: "copilot", name: "Copilot (Key)", checkin: false };
    case "doubao": return { cls: "doubao", name: "Doubao (Key)", checkin: false };
    case "gptgemini": return { cls: "gptgemini", name: "GptGemini (Key)", checkin: false };
    case "glmflash": return { cls: "glmflash", name: "GLM-Flash (Key)", checkin: false };
    case "groq": return { cls: "groq", name: "Groq (Key)", checkin: false };
    case "tierflow": return { cls: "tierflow", name: "Tierflow (Key)", checkin: false };
    default: return { cls: "wb", name: "WorkBuddy", checkin: false };
  }
}

// NO_CHECKIN_GROUPS 无签到活动的渠道：无网页签到体系渠道 + key 型白嫖渠道。
const NO_CHECKIN_GROUPS = ["codebuddy", "zcode", "atria", "copilot", "doubao", "gptgemini", "glmflash", "groq", "tierflow"];

// ---------- 全局状态 ----------
let state = null;
// readOnly：只读大屏模式。只读监听（config.json → listen_dashboard）刻意不暴露 /api/state，
// 前端据此收敛为“仅 Token 大屏”，隐藏其余管理入口。
let readOnly = false;
// switchTabFn：initTabs 内部 switchTab 的引用，供只读模式强制切换 Tab。
let switchTabFn = null;

// ---------- 数据加载 ----------
async function loadState() {
  try {
    state = await api("/api/state");
  } catch (e) {
    // 只读大屏监听不暴露 /api/state（含 API-Key）。用安全兜底状态填充，
    // 避免下方各渲染函数解引用 state 抛错；随后由 applyReadOnlyMode 收敛界面。
    readOnly = true;
    state = {
      version: "",
      running: true,
      listen_host: "127.0.0.1",
      listen_port: 7863,
      api_key: "",
      autostart: false,
      accounts: [],
      checkin_times: [],
      next_checkin: "",
      growth_times: [],
    };
  }
  render();
  if (readOnly) applyReadOnlyMode();
}

// applyReadOnlyMode 只读大屏模式：仅保留 Token 资产大屏 + 调用流水，隐藏其余 Tab 与管理入口。
function applyReadOnlyMode() {
  const allowed = new Set(["usage", "logs"]);
  document.querySelectorAll(".nav-tab").forEach(t => {
    if (!allowed.has(t.dataset.tab)) t.style.display = "none";
  });
  document.querySelectorAll(".dock-item-btn").forEach(t => {
    if (!allowed.has(t.dataset.tab)) t.style.display = "none";
  });
  document.querySelectorAll(".sidebar-item-btn").forEach(t => {
    if (!allowed.has(t.dataset.tab)) t.style.display = "none";
  });
  document.querySelectorAll(".tab-pane").forEach(p => {
    if (p.id !== "tab-usage" && p.id !== "tab-logs") p.classList.remove("active");
  });
  const topInfo = document.querySelector(".top-info");
  if (topInfo) topInfo.style.display = "none";
  const btnDoubao = $("btnDoubao");
  if (btnDoubao) btnDoubao.style.display = "none";
  const title = document.querySelector(".brand .title");
  if (title) title.innerHTML = 'ZeroGate <span class="ver">只读大屏</span>';
  if ($("serverLine")) $("serverLine").textContent = "Token 资产大屏（只读）";
  if ($("mobileStatusText")) $("mobileStatusText").textContent = "只读大屏";
  if (switchTabFn) switchTabFn("usage");
}

async function loadFees() {
  try {
    const fees = await api("/api/fees");
    renderFees(fees);
  } catch (e) { /* 费率接口失败不阻塞 */ }
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

// 积分套餐到期文本：0/空 = 长期有效，否则月-日（悬浮 title 已有完整时间则省略）
// 兜底：旧 state 持久化过上游"永不到期"哨兵值（9999-12-31），也按长期处理
function resourceExpireText(expireAt) {
  if (!expireAt || expireAt <= 0 || expireAt >= 4102444800) return "长期有效";
  const t = new Date(expireAt * 1000);
  const md = (t.getMonth() + 1) + "-" + String(t.getDate()).padStart(2, "0");
  const days = Math.ceil((expireAt * 1000 - Date.now()) / 86400e3);
  if (days <= 0) return md + " (已过期)";
  return md + " (剩" + days + "天)";
}

let activeCreditHoverUID = null;

async function showCreditDetail(e, uid) {
  const el = e.currentTarget;
  activeCreditHoverUID = uid;
  if (detailTimer) { clearTimeout(detailTimer); detailTimer = null; }

  let d = detailCache[uid];
  if (!d) {
    try {
      d = await api("/api/account/resource_detail", { uid });
      detailCache[uid] = d;
    } catch (err) { return; }
  }
  // 核心时序防御：如果异步请求完成时，鼠标已经移开当前账号，则直接丢弃不展示
  if (activeCreditHoverUID !== uid) return;
  if (!d || !d.items || !d.items.length) return;

  let html = `<div class="credit-tip-head"><span>套餐明细 · ${esc(shortUid(uid))}</span><button class="credit-tip-close" onclick="closeCreditTip(event)" title="关闭">${I.x}</button></div>`;
  html += `<table class="detail-table"><thead><tr><th>套餐</th><th>总额</th><th>已用</th><th>剩余</th><th>到期</th></tr></thead><tbody>`;
  for (const it of d.items) {
    html += `<tr><td>${esc(it.name)}</td><td>${it.total}</td><td>${it.used}</td><td>${it.remain}</td><td>${esc(resourceExpireText(it.expire_at))}</td></tr>`;
  }
  html += `</tbody></table>`;

  let tip = $("creditTip");
  if (!tip) {
    tip = document.createElement("div");
    tip.id = "creditTip";
    tip.className = "credit-tip";
    document.body.appendChild(tip);
    tip.addEventListener("mouseenter", () => {
      if (detailTimer) { clearTimeout(detailTimer); detailTimer = null; }
    });
    tip.addEventListener("mouseleave", () => {
      closeCreditTip();
    });
  }
  tip.innerHTML = html;
  tip.style.display = "block";
  tip.classList.add("visible");

  const rect = el.getBoundingClientRect();
  let left = rect.left, top = rect.bottom + 6;
  if (top + 220 > window.innerHeight) top = Math.max(10, rect.top - 220);
  if (left + 300 > window.innerWidth) left = window.innerWidth - 310;
  tip.style.left = Math.max(8, left) + "px";
  tip.style.top = Math.max(8, top) + "px";
}

function hideCreditDetail() {
  activeCreditHoverUID = null;
  detailTimer = setTimeout(() => {
    closeCreditTip();
  }, 200);
}

function closeCreditTip(e) {
  if (e) e.stopPropagation();
  activeCreditHoverUID = null;
  if (detailTimer) { clearTimeout(detailTimer); detailTimer = null; }
  const tip = $("creditTip");
  if (tip) {
    tip.classList.remove("visible");
    tip.style.display = "none";
  }
}

// 页面滚动与点击外部时自动平滑隐藏悬浮窗
window.addEventListener("scroll", () => closeCreditTip(), { passive: true });
document.addEventListener("pointerdown", (e) => {
  const tip = $("creditTip");
  if (tip && tip.style.display !== "none" && !tip.contains(e.target) && !e.target.closest(".acct-credits")) {
    closeCreditTip();
  }
});

// ---------- 渲染 ----------
function render() {
  renderTopbar();
  renderAccounts();
  renderTimes();
  renderGrowthTimes();
  if (window.__fillIcons) window.__fillIcons();
}

function renderTopbar() {
  if ($("ver")) $("ver").textContent = "v" + state.version;
  if ($("verTop")) $("verTop").textContent = "v" + state.version;
  if ($("verMobile")) $("verMobile").textContent = "v" + state.version;
  if ($("serverLine")) $("serverLine").textContent = state.running ? "服务运行中" : "服务未启动";
  if ($("sidebarStatusText")) $("sidebarStatusText").textContent = state.running ? (state.listen_port + " 运行中") : "服务未启动";
  if ($("mobileStatusText")) {
    $("mobileStatusText").textContent = state.running ? (state.listen_port + " 运行中") : "未启动";
    $("mobileStatusText").style.color = state.running ? "var(--ok)" : "var(--danger)";
  }
  const statusPill = $("sidebarStatusPill");
  if (statusPill) {
    statusPill.style.color = state.running ? "var(--ok)" : "var(--danger)";
    statusPill.style.background = state.running ? "var(--ok-soft)" : "var(--danger-soft)";
  }
  if ($("sysRunStatusTag")) {
    $("sysRunStatusTag").textContent = state.running ? "服务正常运行中" : "服务未启动";
    $("sysRunStatusTag").className = state.running ? "tag ok" : "tag danger";
  }
  if ($("sysEnvDesc")) {
    $("sysEnvDesc").textContent = `ZeroGate Core v${state.version} · 多渠道聚合中枢 · 监听 127.0.0.1:${state.listen_port}`;
  }
  if ($("aboutVer")) $("aboutVer").textContent = state.version;

  const host = (state.listen_host === "0.0.0.0" || state.listen_host === "" || state.listen_host === "::")
    ? "127.0.0.1" : state.listen_host;
  const apiURL = `http://${host}:${state.listen_port}/v1`;
  const anthropicURL = `http://${host}:${state.listen_port}`;
  
  if ($("apiAddr")) $("apiAddr").querySelector(".val").textContent = apiURL;
  if ($("anthropicAddr")) $("anthropicAddr").querySelector(".val").textContent = anthropicURL;

  const key = state.api_key;
  if ($("apiKeyDisplay")) $("apiKeyDisplay").querySelector(".val").textContent = key === "" ? "（无鉴权）" : key;

  // 开机自启开关回写真实状态（注册表为准），避免页面刷新后总显示关闭
  if ($("chkAutostart")) $("chkAutostart").checked = !!state.autostart;
}


function formatExpire(expireAt) {
  // 兜底：旧 state 持久化过上游"永不到期"哨兵值（9999-12-31 = 253402214400），
  // 不拦会显示成"2912169 天后到期"；超过 2100 年一律按长期有效
  if (!expireAt || expireAt <= 0 || expireAt >= 4102444800) {
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

// 获取账号临期套餐详情（支持多套餐提取最早到期额度与名称，双重兜底缓存）
function getExpiringDetail(a) {
  let expireAt = Number(a.expire_at || 0);
  let expRemain = Number(a.expiring_remain || 0);
  let expName = a.expiring_name || "";

  const extractFromItems = (items) => {
    if (!Array.isArray(items) || items.length === 0) return null;
    let earliest = 0, eRemain = 0, eName = "";
    for (const it of items) {
      const r = Number(it.remain || 0);
      const exp = Number(it.expire_at || 0);
      if (r <= 0 || exp <= 0 || exp >= 4102444800) continue;
      if (!earliest || exp < earliest) {
        earliest = exp;
        eRemain = r;
        eName = it.name || "";
      } else if (exp === earliest) {
        eRemain += r;
      }
    }
    return earliest ? { expireAt: earliest, expRemain: eRemain, expName: eName } : null;
  };

  if ((!expRemain || !expName) && Array.isArray(a.resource_items) && a.resource_items.length > 0) {
    const res = extractFromItems(a.resource_items);
    if (res) {
      if (!expireAt) expireAt = res.expireAt;
      if (!expRemain) expRemain = res.expRemain;
      if (!expName) expName = res.expName;
    }
  }

  if ((!expRemain || !expName) && window.state && Array.isArray(window.state.accounts)) {
    const acc = window.state.accounts.find(x => x.uid === a.uid);
    if (acc) {
      if (!expireAt && acc.expire_at) expireAt = Number(acc.expire_at);
      if (!expRemain && acc.expiring_remain) expRemain = Number(acc.expiring_remain);
      if (!expName && acc.expiring_name) expName = acc.expiring_name;
      if ((!expRemain || !expName) && Array.isArray(acc.resource_items)) {
        const res = extractFromItems(acc.resource_items);
        if (res) {
          if (!expireAt) expireAt = res.expireAt;
          if (!expRemain) expRemain = res.expRemain;
          if (!expName) expName = res.expName;
        }
      }
    }
  }

  if ((!expRemain || !expName) && detailCache[a.uid] && Array.isArray(detailCache[a.uid].items)) {
    const res = extractFromItems(detailCache[a.uid].items);
    if (res) {
      if (!expireAt) expireAt = res.expireAt;
      if (!expRemain) expRemain = res.expRemain;
      if (!expName) expName = res.expName;
    }
  }

  return { expireAt, expRemain, expName };
}

// 精简套餐展示名（去除活动批次与括号编号，保留核心特征）
function shortPkgName(name) {
  if (!name) return "临期包";
  let s = String(name).replace(/\(.*?\)/g, "").replace(/（.*?）/g, "").replace(/【.*?】/g, "").replace(/\[.*?\]/g, "").trim();
  if (s.length > 8) s = s.slice(0, 7) + "…";
  return s || "临期包";
}

// ---------- 账号矩阵：模式分区 + 渠道手风琴 ----------
// mode 由后端 /api/state 下发：proxy 反代 / bridge 桥接（zcode-proxy）/ direct 直连 API。
// 缺省回退按渠道判定，与后端 channelDefaultMode 保持一致。
const MODE_SECTIONS = [
  { key: "proxy",  name: "反代账号", desc: "网关持有网页版/客户端凭证，模拟官方客户端转发" },
  { key: "bridge", name: "桥接账号", desc: "转发到本地 zcode-proxy 实例，网关不直接持有凭证" },
  { key: "direct", name: "直连账号", desc: "官方 API Key 直连，非反代，独立管理" },
];
const CHANNEL_ORDER = ["workbuddy", "traework", "qoder", "codebuddy", "zcode", "autoclaw", "atria", "copilot", "doubao", "gptgemini", "glmflash", "groq", "tierflow"];
const GROUP_KEY_LS = "zg_acct_groups";
let acctSearchQ = "";
let acctFilterKey = "all";

function acctMode(a) {
  return MODE_SECTIONS.some(s => s.key === a.mode) ? a.mode : "proxy";
}

function acctMatches(a) {
  if (acctSearchQ) {
    const gm = groupMeta(a.group);
    const hay = `${a.nickname || ""} ${a.uid || ""} ${a.group || ""} ${gm.name}`.toLowerCase();
    if (!hay.includes(acctSearchQ)) return false;
  }
  const expiring = a.expire_at > 0 && a.expire_at <= Math.floor(Date.now() / 1000) + 3 * 86400;
  switch (acctFilterKey) {
    case "ok": return !a.disabled && !a.cooling;
    case "cooling": return !a.disabled && a.cooling;
    case "expiring": return !a.disabled && expiring;
    case "disabled": return a.disabled;
    default: return true;
  }
}

function loadGroupOpen() {
  try { return JSON.parse(localStorage.getItem(GROUP_KEY_LS)) || {}; } catch (e) { return {}; }
}

function saveGroupOpen(gkey, open) {
  const m = loadGroupOpen();
  m[gkey] = open;
  try { localStorage.setItem(GROUP_KEY_LS, JSON.stringify(m)); } catch (e) { /* ignore */ }
}

function setAllGroups(open) {
  const m = loadGroupOpen();
  state.accounts.forEach(a => { m[`${acctMode(a)}:${a.group}`] = open; });
  try { localStorage.setItem(GROUP_KEY_LS, JSON.stringify(m)); } catch (e) { /* ignore */ }
  renderAccounts();
}

function renderAcctGroupHTML(modeKey, group, list) {
  const gm = groupMeta(group);
  const okN = list.filter(x => !x.disabled && !x.cooling).length;
  const coolN = list.filter(x => !x.disabled && x.cooling).length;
  const disN = list.filter(x => x.disabled).length;
  const posExp = list.map(x => x.expire_at).filter(t => t > 0);
  const expText = posExp.length ? formatExpire(Math.min(...posExp)).text : "长期有效";
  const gkey = `${modeKey}:${group}`;
  const open = loadGroupOpen()[gkey] !== false;
  return `
  <details class="acct-group" data-gkey="${gkey}"${open ? " open" : ""}>
    <summary>
      <div class="gh-head">
        <span class="arrow">${I.chevron}</span>
        <span class="ch-dot" style="background:${chColor(group)}"></span>
        <span class="gh-name">${esc(gm.name)}</span>
        <span class="gh-count">${list.length}</span>
      </div>
      <div class="gh-meta">
        <span>正常 <span class="v">${okN}</span></span>
        ${coolN ? `<span class="gh-warn">冷却 <span class="v">${coolN}</span></span>` : ""}
        ${disN ? `<span class="gh-bad">停用 <span class="v">${disN}</span></span>` : ""}
        <span>最早到期 <span class="v">${esc(expText)}</span></span>
      </div>
    </summary>
    <div class="acct-grid acct-group-body">${list.map(acctCardHTML).join("")}</div>
  </details>`;
}

function renderAccounts() {
  const wrap = $("acctList");
  const empty = $("acctEmpty");
  if (!state.accounts.length) {
    wrap.innerHTML = "";
    empty.classList.remove("hidden");
    return;
  }
  empty.classList.add("hidden");

  const matched = state.accounts.filter(acctMatches);
  if (!matched.length) {
    wrap.innerHTML = `<div class="empty">没有匹配的账号，试试调整搜索词或筛选条件</div>`;
    return;
  }

  let html = "";
  for (const sec of MODE_SECTIONS) {
    const all = state.accounts.filter(a => acctMode(a) === sec.key);
    if (!all.length) continue; // 该模式没有任何账号：不渲染分区
    const shown = matched.filter(a => acctMode(a) === sec.key);
    const groups = [...new Set(shown.map(a => a.group))]
      .sort((x, y) => {
        const ix = CHANNEL_ORDER.indexOf(x), iy = CHANNEL_ORDER.indexOf(y);
        return (ix < 0 ? 99 : ix) - (iy < 0 ? 99 : iy);
      });
    html += `
    <div class="mode-section">
      <div class="mode-head">
        <span class="mode-name">${sec.name}</span>
        <span class="mode-count">${shown.length}/${all.length}</span>
        <span class="mode-desc">${esc(sec.desc)}</span>
      </div>`;
    if (!shown.length) {
      html += `<div class="empty">该分区无匹配账号</div>`;
    } else {
      for (const g of groups) {
        html += renderAcctGroupHTML(sec.key, g, shown.filter(a => a.group === g));
      }
    }
    html += `</div>`;
  }
  wrap.innerHTML = html;

  // details 的 toggle 事件不冒泡，逐个绑定开合记忆
  wrap.querySelectorAll("details.acct-group").forEach(d => {
    d.addEventListener("toggle", () => saveGroupOpen(d.dataset.gkey, d.open));
  });
}

function acctCardHTML(a) {
  const gm = groupMeta(a.group);
  const group = gm.cls;
  const groupName = gm.name;
  const noCheckin = gm.checkin;

  const checkinTag = a.last_checkin_at
    ? `<span class="tag ${a.last_checkin_ok ? "ok" : "bad"}">${a.last_checkin_ok ? "签到成功" : "签到失败"}</span>`
    : (noCheckin ? '<span class="tag neutral">无签到</span>' : '<span class="tag neutral">未签到</span>');

  const expDetail = getExpiringDetail(a);
  const curExpAt = expDetail.expireAt || a.expire_at;
  const exp = formatExpire(curExpAt);
  const curRemain = Number(a.credits || 0);
  const isPartialExp = curExpAt > 0 && curExpAt < 4102444800 && expDetail.expRemain > 0 && expDetail.expRemain < curRemain;
  const isTokAcct = (a.group === "zcode" || a.group === "atria");
  const unitAcct = isTokAcct ? "token" : "积分";

  let expireTag = `<span class="tag ${exp.cls}" onclick="showCreditDetail(event, '${esc(a.uid)}')" title="${esc(exp.full)} · 点击查看明细" style="cursor:pointer;">${I.clock} ${esc(exp.text)}</span>`;
  if (isPartialExp) {
    const expFmt = isTokAcct ? fmtTokens(expDetail.expRemain) : fmtTotal(expDetail.expRemain);
    const safeFmt = isTokAcct ? fmtTokens(curRemain - expDetail.expRemain) : fmtTotal(curRemain - expDetail.expRemain);
    expireTag = `<span class="tag ${exp.cls} exp-part-badge" onclick="showCreditDetail(event, '${esc(a.uid)}')" title="${esc(exp.full)} · 仅此套餐临期（${expDetail.expRemain} ${unitAcct}），其余 ${safeFmt} ${unitAcct} 有效；点击查看明细" style="cursor:pointer;">${I.alert} 仅 ${expFmt} ${unitAcct} ${esc(exp.text)}</span>`;
  }
  const nextPickTag = a.is_next
    ? `<span class="tag next-pick" title="本渠道第一顺位：下一次请求将优先调用此账号（zcode 按桥接轮转顺序，其余渠道按快到期/低余额优先）">${I.target} 优先调度</span>`
    : "";
  const bridgeDownTag = a.bridge_down
    ? `<span class="tag danger" title="该账号对应的本地桥进程未启动（bridgeUrl 端口连不上），请求打到它会直接失败；请到「本地桥」页启动该实例">${I.alert} 桥未启动</span>`
    : "";

  const disabledClass = a.disabled ? " disabled" : "";
  const disableIcon = a.disabled ? I.play : I.pause;
  const disableTitle = a.disabled ? "启用" : "停用";
  const checkinBtn = noCheckin
    ? `<span class="icon-op off" title="${groupName} 无签到活动" onclick="return false">${I.check}</span>`
    : `<span class="icon-op" title="签到" onclick="checkin('${a.uid}')">${I.check}</span>`;
  const growthBtn = a.group === "workbuddy"
    ? `<span class="icon-op" title="成长中心（旅行/盲盒/任务领奖）" onclick="growth('${a.uid}')">${I.gift}</span>`
    : "";

  // 积分分类详情统一走悬浮明细（showCreditDetail），卡片内不再重复展开

  return `
  <div class="acct-card${disabledClass}">
    <div class="acct-top">
      <div style="display:flex;align-items:center;gap:6px;flex-wrap:wrap;">
        <span class="badge ${group}">${groupName}</span>
        <span class="acct-name">${esc(a.nickname || shortUid(a.uid))}</span>
        ${nextPickTag}
        ${bridgeDownTag}
      </div>
      <div class="acct-ops">
        ${checkinBtn}
        ${growthBtn}
        <span class="icon-op" title="刷新积分" onclick="refreshOne('${a.uid}')">${I.refresh}</span>
        <span class="icon-op warn" title="${disableTitle}" onclick="toggleDisable('${a.uid}',${a.disabled})">${disableIcon}</span>
        <span class="icon-op danger" title="删除账号" onclick="removeAcct('${a.uid}')">${I.x}</span>
      </div>
    </div>
    <div class="acct-uid">UID: ${esc(shortUid(a.uid))}</div>
    <div class="acct-mid">
      ${(() => {
        const isTok = (a.group === "zcode" || a.group === "atria");
        if (isTok) {
          const formatted = fmtTokens(a.credits);
          return `<div class="acct-credits is-token" onmouseenter="showCreditDetail(event,'${a.uid}')" onmouseleave="hideCreditDetail()" title="${Number(a.credits || 0).toLocaleString()} Token">
            ${formatted}<span>Token</span>
          </div>`;
        }
        return `<div class="acct-credits" onmouseenter="showCreditDetail(event,'${a.uid}')" onmouseleave="hideCreditDetail()" title="${Number(a.credits || 0).toLocaleString()} 积分">
          ${Number(a.credits || 0).toLocaleString()}<span>积分</span>
        </div>`;
      })()}
      <div class="acct-tags">
        ${(() => {
          if (a.group === "zcode" || a.group === "atria") {
            return `<span class="tag ratio-tag" title="官方按 Token 计量，1 积分 = 1 Token">${I.coins} 1分 = 1 Token</span>`;
          }
          if (a.group === "workbuddy") return `<span class="tag ratio-tag" title="普通模型约 1~2 积分/次">${I.zap} ~1-2分/次</span>`;
          if (a.group === "traework") return `<span class="tag ratio-tag" title="每日重置额度">${I.refresh} 每日重置</span>`;
          if (a.group === "qoder") return `<span class="tag ratio-tag" title="按量扣减积分">${I.zap} 按量扣减</span>`;
          if (a.group === "codebuddy") return `<span class="tag ratio-tag" title="国际版按倍率扣分">${I.zap} 倍率扣分</span>`;
          if (a.group === "autoclaw") return `<span class="tag ratio-tag" title="短请求≈1分">${I.zap} 短请求≈1分</span>`;
          return "";
        })()}
        ${expireTag}
        ${checkinTag}
      </div>
    </div>
  </div>`;
}

// ---------- 添加账号下拉菜单 ----------
const ADD_CHANNELS = [
  { ch: "workbuddy", name: "WorkBuddy",        hint: "浏览器登录" },
  { ch: "traework",  name: "TraeWork",         hint: "浏览器登录" },
  { ch: "qoder",     name: "Qoder",            hint: "浏览器登录" },
  { ch: "codebuddy", name: "CodeBuddy (国际)", hint: "浏览器登录" },
  { ch: "zcode",     name: "ZCode (Z.AI)",     hint: "浏览器登录 · 桥接" },
  { ch: "autoclaw",  name: "AutoClaw",         hint: "手机验证码" },
  { ch: "atria",     name: "Atria (官方 Key)", hint: "粘贴 atr_ Key" },
];

function renderAddMenu() {
  const menu = $("addAcctMenu");
  if (!menu) return;
  menu.innerHTML = ADD_CHANNELS.map(c => {
    const gm = groupMeta(c.ch);
    return `<button class="dropdown-item" data-channel="${c.ch}">
      <span class="ch-dot" style="background:${chColor(c.ch)}"></span>
      <span class="di-name">${esc(c.name)}</span>
      <span class="di-hint">${esc(c.hint)}</span>
    </button>`;
  }).join("");
}

function toggleAddMenu(show) {
  const menu = $("addAcctMenu");
  if (!menu) return;
  const open = show === undefined ? menu.classList.contains("hidden") : show;
  menu.classList.toggle("hidden", !open);
  const drop = $("addAcctDrop");
  if (drop) drop.classList.toggle("open", open);
}

function renderTimes() {
  const box = $("timesBox");
  box.innerHTML = (state.checkin_times || []).map((t) =>
    `<span class="time-chip" title="点击删除" onclick="delTime('${t}')">${t}${I.x}</span>`).join("");
  $("nextCheckin").textContent = state.next_checkin || "-";
}

// 渠道主题色（卡片渐变图标）
const feeThemes = {
  traework: ["#2563eb", "#38bdf8"],
  qoder:     ["#7c3aed", "#a855f7"],
  codebuddy: ["#0ea5e9", "#06b6d4"],
  zcode:     ["#10b981", "#34d399"],
  autoclaw:  ["#6366f1", "#818cf8"],
  copilot:   ["#334155", "#64748b"],
  doubao:    ["#dc2626", "#f87171"],
  gptgemini: ["#db2777", "#f472b6"],
  glmflash:  ["#65a30d", "#a3e635"],
  groq:      ["#f59e0b", "#fbbf24"],
  tierflow:  ["#d946ef", "#e879f9"],
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

  let html = `<div class="note">客户端填写的模型 ID = 渠道名/模型名，例：<code>autoclaw/glm-5.3-flash</code>；点击「⧉」一键复制。</div>`;
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
      // 防御：自定义反代模型名含斜杠（形如 "qoder/xxx"），拼接后成多段怪名，直接跳过
      if (!m.model || m.model.includes("/")) continue;
      const rate = m.rate ? Number(m.rate) : 0;
      let badgeClass = "free";
      if (rate > 0 && rate <= 0.15) badgeClass = "low";
      else if (rate > 0.15 && rate <= 0.5) badgeClass = "normal";
      else if (rate > 0.5) badgeClass = "high";

      // Rate=0 时区分「按量计费」与「限免」：上游无公开倍率的渠道以 Note 说明计费方式
      const isBilled = /按量|扣积分|计费|会员|×?积分|点价/.test(m.note || "");
      const rateDisplay = rate > 0 ? `x${rate.toFixed(2)}` : (isBilled ? "按量" : "限免");
      const fullId = `${ch.channel}/${m.model}`;
      const noteHtml = m.note ? `<div class="fee-note-tag" title="${esc(m.note)}">${esc(m.note)}</div>` : "";

      rows += `<tr>
        <td class="fee-model-cell">
          <button type="button" class="fee-copy" data-id="${esc(fullId)}" onclick="copyId(this.getAttribute('data-id'))" title="复制模型 ID">${I.copy}</button>
          <code class="fee-id" onclick="copyId('${esc(fullId)}')" title="点击复制">${esc(fullId)}</code>
        </td>
        <td><span class="fee-rate-badge ${badgeClass}">${rateDisplay}</span></td>
        <td>${noteHtml}</td>
      </tr>`;
    }
    
    if (!rows) {
      rows = `<tr><td colspan="3" class="empty-models">暂无模型</td></tr>`;
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
              <th>模型 ID（点击复制）</th>
              <th style="width:70px">费率</th>
              <th style="width:46%">说明</th>
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

// 复制模型 ID 到剪贴板（含旧浏览器降级）。
function copyId(text) {
  const ok = () => toast(`已复制模型 ID：${text}`);
  if (navigator.clipboard && window.isSecureContext) {
    navigator.clipboard.writeText(text).then(ok).catch(() => fallbackCopy(text, ok));
  } else {
    fallbackCopy(text, ok);
  }
}
function fallbackCopy(text, ok) {
  const ta = document.createElement("textarea");
  ta.value = text;
  ta.style.position = "fixed";
  ta.style.opacity = "0";
  document.body.appendChild(ta);
  ta.select();
  try { document.execCommand("copy"); ok(); } catch (e) { toast("复制失败，请手动选择"); }
  document.body.removeChild(ta);
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
    const g = CHANNEL_ORDER.includes(a.group) ? a.group : "workbuddy";
    (groups[g] = groups[g] || []).push(a);
  }
  let html = "";
  for (const g of CHANNEL_ORDER) {
    const list = groups[g];
    if (!list || !list.length) continue;
    const gName = groupMeta(g).name;
    html += `<div class="ci-group"><div class="ci-gname">${gName}</div>`;
    for (const a of list) {
      const noCheckin = NO_CHECKIN_GROUPS.includes(a.group);
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
let wbtKind = "codebuddy";
function openWbtDialog(channel) {
  wbtKind = "codebuddy";
  $("wbtTitle").textContent = "添加 " + groupMeta(channel).name + " 账号（粘贴 token）";
  $("wbtToken").value = ""; $("wbtRefresh").value = ""; $("wbtName").value = ""; $("wbtMsg").textContent = "";
  $("wbtokenOverlay").classList.remove("hidden");
}
function closeWbtDialog() { $("wbtokenOverlay").classList.add("hidden"); }
async function wbtImport() {
  const token = $("wbtToken").value.trim();
  if (!token) { toast("请粘贴 accessToken"); return; }
  const domain = "codebuddy.ai";
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
  if (channel === "atria") { openAtriaDialog(); return; }
  const row = $("zcodeProviderRow");
  if (channel === "zcode") {
    pendingProvider = "zai";
    row.classList.remove("hidden");
    row.querySelectorAll(".btn").forEach((b) => b.classList.toggle("primary", b.dataset.provider === "zai"));
  } else {
    row.classList.add("hidden");
  }
  $("lcTitle").textContent = "添加 " + name + " 账号";
  if (channel === "codebuddy") {
    $("lcMsg").textContent = `点击「登录${name}」将打开浏览器窗口，请按指示登录 ${name} 账号（国际版，与 WorkBuddy 同源共用账号），完成后关闭浏览器窗口即可。`;
  } else {
    $("lcMsg").textContent = groupMeta(channel).checkin
      ? `点击「登录${name}」将打开浏览器窗口，请按照指示正常登录${name}账号，登录成功后关闭浏览器窗口即可。（${name} 渠道无签到活动，仅 API 转发）`
      : `点击「登录${name}」将打开浏览器窗口，请按照指示正常登录${name}账号，登录成功后关闭浏览器窗口即可。`;
  }
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
    $("loginOverlay").classList.remove("hidden");
    $("btnCopyUrl").dataset.url = url;
    if (channel === "zcode") {
      // zcode-proxy CLI 已自行拉起浏览器打开授权页，面板不再重复打开（否则同页开两次）
      $("loginMsg").textContent = "浏览器已自动打开授权页，完成授权后返回即可（若未打开请点「复制链接」手动打开）";
    } else {
      $("loginMsg").textContent = "请在浏览器新窗口中完成登录…";
      window.open(url, "_blank", "noopener,noreferrer");
    }
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
  $("btnAddAcct").onclick = (e) => { e.stopPropagation(); toggleAddMenu(); };
  $("addAcctMenu").addEventListener("click", (e) => {
    const item = e.target.closest(".dropdown-item");
    if (!item) return;
    toggleAddMenu(false);
    promptLogin(item.dataset.channel);
  });
  document.addEventListener("click", (e) => {
    if (!e.target.closest("#addAcctDrop")) toggleAddMenu(false);
  });
  $("acctSearch").addEventListener("input", (e) => {
    acctSearchQ = e.target.value.trim().toLowerCase();
    renderAccounts();
  });
  document.querySelectorAll("#acctFilter .chip").forEach(c => {
    c.addEventListener("click", () => {
      acctFilterKey = c.dataset.filter;
      document.querySelectorAll("#acctFilter .chip").forEach(x => x.classList.toggle("active", x === c));
      renderAccounts();
    });
  });
  $("btnExpandAll").onclick = () => setAllGroups(true);
  $("btnCollapseAll").onclick = () => setAllGroups(false);
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
  $("chkAutostart").onchange = toggleAutostart;

  // 成长中心
  $("btnAddGrowthTime").onclick = addGrowthTime;
  $("btnSaveGrowthTimes").onclick = () => saveGrowthTimes((state.growth_times || []).slice());
  $("btnRunGrowthNow").onclick = runGrowthNow;
  $("btnRefreshGrowthToday").onclick = loadGrowthToday;
  $("btnRefreshGrowthAccounts").onclick = loadGrowthAccounts;
  $("btnRefreshGrowthRecords").onclick = loadGrowthRecords;
  $("selGrowthLimit").onchange = loadGrowthRecords;
  $("btnGrowthDetailClose").onclick = closeGrowthDetail;
  $("growthDetailOverlay").onclick = (e) => { if (e.target === $("growthDetailOverlay")) closeGrowthDetail(); };
  // 成长中心 · 签到与 ZCode 周末包领取
  $("btnGrowthBatchCheckin").onclick = openCheckinDialog;
  $("btnClaimRefresh").onclick = loadClaimStatus;
  $("btnClaimNow").onclick = runClaimNow;

  // 本地桥
  $("btnBridgeRefresh").onclick = loadBridges;
  $("btnBridgeStartAll").onclick = bridgeStartAll;

  // Atria Key 管理
  $("btnAtriaAdd").onclick = addAtriaKey;
  $("btnAtriaBatch").onclick = addAtriaBatch;
  $("btnAtriaClose").onclick = closeAtriaDialog;
  $("atriaOverlay").onclick = (e) => { if (e.target === $("atriaOverlay")) closeAtriaDialog(); };
  $("atriaKey").addEventListener("keydown", (e) => { if (e.key === "Enter") addAtriaKey(); });

  $("apiAddr").onclick = (e) => {
    e.stopPropagation();
    const v = $("apiAddr").querySelector(".val").textContent;
    copyText(v, "OpenAI 接口地址");
  };
  $("apiKeyDisplay").onclick = (e) => {
    e.stopPropagation();
    const v = $("apiKeyDisplay").querySelector(".val").textContent;
    if (v === "（无鉴权）") { toast("当前未设置 API-Key"); return; }
    copyText(v, "API-Key");
  };
  // 修改图标点击弹配置对话框（不触发复制）
  const btnEditApiConfig = $("btnEditApiConfig");
  if (btnEditApiConfig) {
    btnEditApiConfig.onclick = (e) => {
      e.stopPropagation();
      openApiConfig();
    };
  }
  const btnEditApiKey = $("btnEditApiKey");
  if (btnEditApiKey) {
    btnEditApiKey.onclick = (e) => {
      e.stopPropagation();
      openApiKey();
    };
  }
  const edits = document.querySelectorAll(".icon-edit");
  if (!btnEditApiKey && edits.length > 0) {
    edits[edits.length - 1].onclick = (e) => {
      e.stopPropagation();
      openApiKey();
    };
  }

  $("btnHelp").onclick = openHelp;
  $("btnAbout").onclick = openAbout;
  $("btnHelpClose").onclick = closeHelp;
  $("btnAboutClose").onclick = closeAbout;

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
  initAuditLogsEvents();
  if ($("btnCreateClient")) $("btnCreateClient").onclick = createClient;
  if ($("inClientName")) $("inClientName").addEventListener("keydown", (e) => { if (e.key === "Enter") createClient(); });
  if ($("btnAddRedirect")) $("btnAddRedirect").onclick = saveRedirect;
  if ($("btnResetRedirects")) $("btnResetRedirects").onclick = resetRedirects;
  if ($("btnRefreshTokens")) $("btnRefreshTokens").onclick = loadTokenAnalytics;
  if ($("btnRefreshAssets")) $("btnRefreshAssets").onclick = loadAssets;
}

// ---------- Tab 导航控制（同时接管原生 nav-tab、侧边栏 sidebar 与 悬浮 Command Dock） ----------
function initTabs() {
  const tabs = document.querySelectorAll(".nav-tab");
  const dockBtns = document.querySelectorAll(".dock-item-btn");
  const sidebarBtns = document.querySelectorAll(".sidebar-item-btn");
  const panes = document.querySelectorAll(".tab-pane");
  const savedTab = localStorage.getItem("zg_active_tab") || "dashboard";

  function switchTab(name) {
    tabs.forEach(t => t.classList.toggle("active", t.dataset.tab === name));
    dockBtns.forEach(b => b.classList.toggle("active", b.dataset.tab === name));
    sidebarBtns.forEach(b => b.classList.toggle("active", b.dataset.tab === name));
    panes.forEach(p => p.classList.toggle("active", p.id === `tab-${name}`));
    localStorage.setItem("zg_active_tab", name);

    // 移动端切页体验优化：平滑回顶并将当前 Dock 按钮居中
    if (window.innerWidth <= 768) {
      window.scrollTo({ top: 0, behavior: "smooth" });
    }
    const activeDock = document.querySelector(`.dock-item-btn[data-tab="${name}"]`);
    if (activeDock && typeof activeDock.scrollIntoView === "function") {
      activeDock.scrollIntoView({ behavior: "smooth", inline: "center", block: "nearest" });
    }

    if (name === "dashboard") loadDashboard();
    else if (name === "usage") { loadTokenAnalytics(); loadAssets(); }
    else if (name === "logs") loadAuditLogs();
    else if (name === "clients") loadClients();
    else if (name === "router") loadRedirects();
    else if (name === "growth") initGrowthTab();
    else if (name === "bridges") loadBridges();
    else if (name === "models") loadFees();
    else if (name === "settings") loadSettingsView();
  }

  tabs.forEach(t => {
    t.addEventListener("click", () => switchTab(t.dataset.tab));
  });
  dockBtns.forEach(b => {
    b.addEventListener("click", () => switchTab(b.dataset.tab));
  });
  sidebarBtns.forEach(b => {
    b.addEventListener("click", () => switchTab(b.dataset.tab));
  });

  switchTabFn = switchTab;
  switchTab(savedTab);

  // 初始化壁纸、侧边栏、移动抽屉、布局模式与双协议药丸复制
  initWallpaperSystem();
  initProtocolPills();
  initSidebar();
  initMobileDrawer();
  initLayoutMode();
}

function initMobileDrawer() {
  const sidebar = $("zgSidebar");
  const backdrop = $("zgSidebarBackdrop");
  const toggleBtn = $("btnMobileSidebarToggle");
  const closeBtn = $("btnSidebarDrawerClose");

  const openDrawer = () => {
    if (sidebar) sidebar.classList.add("drawer-open");
    if (backdrop) backdrop.classList.add("active");
  };
  const closeDrawer = () => {
    if (sidebar) sidebar.classList.remove("drawer-open");
    if (backdrop) backdrop.classList.remove("active");
  };

  if (toggleBtn) {
    toggleBtn.onclick = (e) => {
      if (e) { e.preventDefault(); e.stopPropagation(); }
      if (sidebar && sidebar.classList.contains("drawer-open")) {
        closeDrawer();
      } else {
        openDrawer();
      }
    };
  }

  if (closeBtn) {
    closeBtn.onclick = (e) => {
      if (e) { e.preventDefault(); e.stopPropagation(); }
      closeDrawer();
    };
  }

  if (backdrop) {
    backdrop.onclick = (e) => {
      if (e) { e.preventDefault(); e.stopPropagation(); }
      closeDrawer();
    };
  }

  // 点击侧栏任意选项，在移动端自动平滑收起抽屉
  document.querySelectorAll(".sidebar-item-btn").forEach(btn => {
    btn.addEventListener("click", () => {
      if (window.innerWidth <= 768) {
        closeDrawer();
      }
    });
  });

  // 按 ESC 键关闭侧栏抽屉
  document.addEventListener("keydown", (e) => {
    if (e.key === "Escape" && sidebar && sidebar.classList.contains("drawer-open")) {
      closeDrawer();
    }
  });
}

function initSidebar() {
  const sidebar = $("zgSidebar");
  const toggleBtn = $("btnSidebarCollapse") || $("btnToggleSidebar");
  if (!sidebar) return;

  const updateToggleUI = (collapsed) => {
    if (!toggleBtn) return;
    toggleBtn.innerHTML = collapsed ? (I.chevronR || I.chevron) : (I.chevronL || I.arrowL || I.chevron);
    toggleBtn.title = collapsed ? "展开侧栏" : "折叠侧栏";
  };

  const isCollapsed = localStorage.getItem("zg_sidebar_collapsed") === "true";
  if (isCollapsed) {
    sidebar.classList.add("sidebar-collapsed");
  }
  updateToggleUI(isCollapsed);

  if (toggleBtn) {
    toggleBtn.onclick = (e) => {
      if (e) {
        e.preventDefault();
        e.stopPropagation();
      }
      sidebar.classList.toggle("sidebar-collapsed");
      const coll = sidebar.classList.contains("sidebar-collapsed");
      localStorage.setItem("zg_sidebar_collapsed", coll ? "true" : "false");
      updateToggleUI(coll);
    };
  }
}

function initLayoutMode() {
  const container = $("zgAppContainer");
  if (!container) return;
  const mode = localStorage.getItem("zg_layout_mode") || "sidebar";
  container.className = `layout-${mode}`;
}

function loadSettingsView() {
  // 1. 布局模式切换（侧边栏 vs 底部 Dock）
  const curLayout = localStorage.getItem("zg_layout_mode") || "sidebar";
  const layoutChips = document.querySelectorAll("#settingLayoutSwitch .chip");
  layoutChips.forEach(chip => {
    chip.classList.toggle("active", chip.dataset.layout === curLayout);
    chip.onclick = () => {
      const mode = chip.dataset.layout;
      localStorage.setItem("zg_layout_mode", mode);
      const container = $("zgAppContainer");
      if (container) container.className = `layout-${mode}`;
      layoutChips.forEach(c => c.classList.toggle("active", c.dataset.layout === mode));
      toast(`已切换至${mode === "sidebar" ? "现代侧边栏" : "底部赛博 Command Dock"}布局`);
    };
  });

  // 2. 主题切换
  const curTheme = document.body.classList.contains("light-theme") ? "light" : "dark";
  const themeChips = document.querySelectorAll("#settingThemeSwitch .chip");
  themeChips.forEach(chip => {
    chip.classList.toggle("active", chip.dataset.theme === curTheme);
    chip.onclick = () => {
      const theme = chip.dataset.theme;
      if (theme === "light") {
        document.body.classList.add("light-theme");
      } else {
        document.body.classList.remove("light-theme");
      }
      localStorage.setItem("zg_theme", theme);
      themeChips.forEach(c => c.classList.toggle("active", c.dataset.theme === theme));
      document.dispatchEvent(new CustomEvent("zg:themechange", { detail: { theme } }));
      toast(`已切换为${theme === "light" ? "浅色" : "深色二次元"}模式`);
    };
  });

  // 3. 设置中的壁纸缩略图与滑块同步
  const savedWp = localStorage.getItem("zg_wallpaper") || "./wallpapers/cyberpunk.jpg";
  document.querySelectorAll(".wp-grid-mini .wp-thumb").forEach(th => {
    th.classList.toggle("active", th.dataset.wp === savedWp);
    th.onclick = () => {
      const wp = th.dataset.wp;
      localStorage.setItem("zg_wallpaper", wp);
      const layer = $("zgVnBgLayer");
      if (layer) layer.style.backgroundImage = `url('${wp}')`;
      document.querySelectorAll(".wp-thumb").forEach(t => t.classList.toggle("active", t.dataset.wp === wp));
      toast("已更换壁纸 ✨");
    };
  });

  const inpAlphaSet = $("inpMaskAlphaSetting");
  const txtAlphaSet = $("txtMaskAlphaSetting");
  const savedAlpha = localStorage.getItem("zg_mask_alpha") || "0.72";
  if (inpAlphaSet) {
    inpAlphaSet.value = savedAlpha;
    if (txtAlphaSet) txtAlphaSet.textContent = Math.round(savedAlpha * 100) + "%";
    inpAlphaSet.oninput = () => {
      const v = inpAlphaSet.value;
      document.documentElement.style.setProperty("--vn-mask-alpha", v);
      localStorage.setItem("zg_mask_alpha", v);
      if (txtAlphaSet) txtAlphaSet.textContent = Math.round(v * 100) + "%";
      const inpAlphaMain = $("inpMaskAlpha");
      if (inpAlphaMain) inpAlphaMain.value = v;
    };
  }

  const inpBlurSet = $("inpMaskBlurSetting");
  const txtBlurSet = $("txtMaskBlurSetting");
  const savedBlur = localStorage.getItem("zg_mask_blur") || "5";
  if (inpBlurSet) {
    inpBlurSet.value = savedBlur;
    if (txtBlurSet) txtBlurSet.textContent = savedBlur + "px";
    inpBlurSet.oninput = () => {
      const v = inpBlurSet.value;
      document.documentElement.style.setProperty("--vn-mask-blur", v + "px");
      localStorage.setItem("zg_mask_blur", v);
      if (txtBlurSet) txtBlurSet.textContent = v + "px";
      const inpBlurMain = $("inpMaskBlur");
      if (inpBlurMain) inpBlurMain.value = v;
    };
  }

  // 4. 网关服务与端点配置
  if (state) {
    if ($("cfgListenHost") && !$("cfgListenHost").value) $("cfgListenHost").value = state.listen_host || "127.0.0.1";
    if ($("cfgListenPort") && !$("cfgListenPort").value) $("cfgListenPort").value = state.listen_port || 7863;
    if ($("cfgApiKey") && !$("cfgApiKey").value) $("cfgApiKey").value = state.api_key || "WildWorkAPI";
    if ($("chkAutostart")) $("chkAutostart").checked = !!state.autostart;
    if ($("sysEnvDesc")) $("sysEnvDesc").textContent = `ZeroGate Core v2.0.1 · 多渠道聚合中枢 · ${state.listen_host || "127.0.0.1"}:${state.listen_port || 7863}`;
  }

  if ($("btnSaveListenCfg")) {
    $("btnSaveListenCfg").onclick = async () => {
      const host = ($("cfgListenHost").value || "").trim() || "127.0.0.1";
      const port = parseInt($("cfgListenPort").value, 10) || 7863;
      try {
        await api("/api/config/listen", { host, port });
        toast("监听配置已保存（端点改动在重启后生效）");
        await loadState();
      } catch (e) { toast(e.message); }
    };
  }

  if ($("btnSaveApiKeyCfg")) {
    $("btnSaveApiKeyCfg").onclick = async () => {
      const key = ($("cfgApiKey").value || "").trim();
      if (!key) { toast("API Key 不能为空"); return; }
      try {
        await api("/api/config/api_key", { key });
        toast("主 API-Key 已保存");
        await loadState();
      } catch (e) { toast(e.message); }
    };
  }

  // 5. 缓存与数据维护
  if ($("btnRefreshFeesCache")) {
    $("btnRefreshFeesCache").onclick = async () => {
      await loadFees();
      toast("渠道模型价格与费率缓存已刷新");
    };
  }

  if ($("btnClearLocalCache")) {
    $("btnClearLocalCache").onclick = () => {
      confirmDialog("确定要清除前端本地个性化缓存吗？这会恢复默认壁纸和布局设置。", () => {
        localStorage.removeItem("zg_wallpaper");
        localStorage.removeItem("zg_mask_alpha");
        localStorage.removeItem("zg_mask_blur");
        localStorage.removeItem("zg_layout_mode");
        localStorage.removeItem("zg_sidebar_collapsed");
        localStorage.removeItem("zg_theme");
        toast("本地配置已重置，正在重新应用默认值…");
        setTimeout(() => location.reload(), 800);
      });
    };
  }
}

function initWallpaperSystem() {
  const wpCard = $("wpCard");
  const btnToggle = $("btnWpToggle");
  const btnClose = $("btnCloseWp");

  if (btnToggle && wpCard) {
    btnToggle.onclick = (e) => {
      e.stopPropagation();
      wpCard.classList.toggle("open");
    };
  }
  if (btnClose && wpCard) {
    btnClose.onclick = () => wpCard.classList.remove("open");
  }
  document.addEventListener("click", (e) => {
    if (wpCard && wpCard.classList.contains("open")) {
      if (!wpCard.contains(e.target) && btnToggle && !btnToggle.contains(e.target)) {
        wpCard.classList.remove("open");
      }
    }
  });

  function setBg(wp) {
    if (!wp) wp = "./wallpapers/cyberpunk.jpg";
    localStorage.setItem("zg_wallpaper", wp);
    const layer = $("zgVnBgLayer");
    if (!layer) return;
    if (wp.startsWith("linear-gradient") || wp.startsWith("radial-gradient")) {
      layer.style.backgroundImage = wp;
    } else if (wp.startsWith("url(") || wp.startsWith("http") || wp.startsWith("./")) {
      layer.style.backgroundImage = wp.startsWith("url(") ? wp : `url('${wp}')`;
    } else {
      layer.style.backgroundImage = `url('${wp}')`;
    }
    document.querySelectorAll(".wp-thumb").forEach(th => {
      th.classList.toggle("active", th.dataset.wp === wp);
    });
  }

  // 默认使用第一张超高清二次元少女壁纸（赛博黑客少女）
  const savedWp = localStorage.getItem("zg_wallpaper");
  const cur = savedWp || "./wallpapers/cyberpunk.jpg";
  setBg(cur);

  // 遮罩暗化控制
  const inpAlpha = $("inpMaskAlpha");
  const txtAlpha = $("txtMaskAlpha");
  const savedAlpha = localStorage.getItem("zg_mask_alpha") || "0.72";
  if (inpAlpha) {
    inpAlpha.value = savedAlpha;
    document.documentElement.style.setProperty("--vn-mask-alpha", savedAlpha);
    if (txtAlpha) txtAlpha.textContent = Math.round(savedAlpha * 100) + "%";
    inpAlpha.oninput = () => {
      const v = inpAlpha.value;
      document.documentElement.style.setProperty("--vn-mask-alpha", v);
      localStorage.setItem("zg_mask_alpha", v);
      if (txtAlpha) txtAlpha.textContent = Math.round(v * 100) + "%";
    };
  }

  // 虚化模糊控制
  const inpBlur = $("inpMaskBlur");
  const txtBlur = $("txtMaskBlur");
  const savedBlur = localStorage.getItem("zg_mask_blur") || "5";
  if (inpBlur) {
    inpBlur.value = savedBlur;
    document.documentElement.style.setProperty("--vn-mask-blur", savedBlur + "px");
    if (txtBlur) txtBlur.textContent = savedBlur + "px";
    inpBlur.oninput = () => {
      const v = inpBlur.value;
      document.documentElement.style.setProperty("--vn-mask-blur", v + "px");
      localStorage.setItem("zg_mask_blur", v);
      if (txtBlur) txtBlur.textContent = v + "px";
    };
  }

  // 绑定二次元少女壁纸点击
  document.querySelectorAll(".wp-thumb").forEach(th => {
    th.onclick = () => {
      const wp = th.dataset.wp;
      setBg(wp);
      toast("已更换二次元全景壁纸 ✨");
      if (wpCard) wpCard.classList.remove("open");
    };
  });

  // 绑定纯色流光渐变快捷切换
  document.querySelectorAll("[data-wp-grad]").forEach(btn => {
    btn.onclick = () => {
      const g = btn.dataset.wpGrad;
      setBg(g);
      toast("已切换流光渐变背景");
      if (wpCard) wpCard.classList.remove("open");
    };
  });

  const btnSave = $("btnSaveCustomWp");
  if (btnSave) {
    btnSave.onclick = () => {
      const v = ($("inpCustomWp").value || "").trim();
      if (!v) { toast("请输入图片网址"); return; }
      setBg(v);
      toast("已应用自定义壁纸");
      if (wpCard) wpCard.classList.remove("open");
    };
  }
}

function initProtocolPills() {
  const copyPill = (id, label) => {
    const el = $(id);
    if (!el) return;
    el.onclick = () => {
      const val = el.querySelector(".val") ? el.querySelector(".val").textContent : el.textContent;
      navigator.clipboard.writeText(val.trim()).then(() => {
        toast(`已复制 ${label}: ${val.trim()}`);
      });
    };
  };

  copyPill("apiAddrPill", "OpenAI 端点");
  copyPill("anthropicAddrPill", "Anthropic / Claude 端点");
  copyPill("apiKeyDisplayPill", "API Key");
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
        if (!a.disabled && !a.cooling) {
          activeAccs++;
        }
        channels[a.group] = (channels[a.group] || 0) + 1;
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
    if ($("quickAnthropicUrl")) $("quickAnthropicUrl").textContent = `http://${dispHost}:${port}`;
    if ($("quickApiKey")) $("quickApiKey").textContent = key || "（无鉴权）";
  } catch (e) { /* ignore */ }
}


// ---------- 渠道与计量元数据（供大屏与审计流水公用） ----------
// unit 标记该渠道余额的计量单位：token = 余额即 token；credit = 积分/额度
const CHANNEL_METAS = {
  workbuddy: { name: "WorkBuddy", color: "#7c3aed", unit: "credit" },
  traework:  { name: "TraeWork",  color: "#2563eb", unit: "credit" },
  qoder:     { name: "Qoder",     color: "#0891b2", unit: "credit" },
  codebuddy: { name: "CodeBuddy", color: "#16a34a", unit: "credit" },
  zcode:     { name: "ZCode",     color: "#6366f1", unit: "token"  },
  autoclaw:  { name: "AutoClaw",  color: "#0d9488", unit: "credit" },
  atria:     { name: "Atria",     color: "#0284c7", unit: "token"  },
  wbintl:    { name: "WBIntl",    color: "#4f46e5", unit: "credit" },
};

function unitOf(kind) {
  const m = CHANNEL_METAS[kind];
  return (m && m.unit) || "credit";
}
function isTokenUnit(kind) { return unitOf(kind) === "token"; }

// ---------- 实时调用审计流水 ----------
let allAuditLogs = [];
let auditTypeFilter = "all"; // "all" | "error"
let auditLogsTimer = null;
let currentDetailLog = null;

function extractLogCredits(l) {
  if (typeof l.credits === "number" && !isNaN(l.credits) && l.credits > 0) {
    return l.credits;
  }
  if (l.usage_raw) {
    const m = l.usage_raw.match(/"(?:credits|cost|credit)":\s*([0-9.]+)/i);
    if (m && m[1]) {
      const val = parseFloat(m[1]);
      if (!isNaN(val)) return val;
    }
  }
  return null;
}

function renderLogCostCell(l) {
  const meta = CHANNEL_METAS[l.channel];
  const isCreditChan = (meta && meta.unit === "credit") || (!isTokenUnit(l.channel) && l.channel !== "zcode" && l.channel !== "atria");
  const parsedCredits = extractLogCredits(l);

  if (isCreditChan) {
    let num = 1; // 积分渠道默认单次调用基准扣 1 积分
    let isFree = false;
    if (parsedCredits !== null) {
      num = parsedCredits;
      if (num === 0) isFree = true;
    } else if (l.status !== 200 && !l.total_tokens) {
      num = 0;
    }

    let creditBadge = "";
    if (isFree || num === 0) {
      creditBadge = `<span class="audit-credit-tag" style="background:rgba(245,158,11,0.08);color:#d97706;border-color:rgba(245,158,11,0.25);">${I.coins} 0 积分 (限免)</span>`;
    } else {
      const numStr = num < 1 ? num.toFixed(2) : String(num);
      creditBadge = `<span class="audit-credit-tag">${I.coins} ${numStr} 积分</span>`;
    }

    const tokStr = l.total_tokens ? `${fmtTotal(l.total_tokens)} tok` : "0 tok";
    const tokTitle = `输入: ${fmtTotal(l.prompt_tokens || 0)} / 输出: ${fmtTotal(l.completion_tokens || 0)}${l.cache_tokens ? ` / 缓存: ${fmtTotal(l.cache_tokens)}` : ""}`;
    return `
      <div class="audit-cost-row">
        ${creditBadge}
        <span class="audit-tok-text" title="${tokTitle}">${tokStr}</span>
      </div>
    `;
  }

  // Token 渠道（zcode, atria 等）
  const totalTok = fmtTokens(l.total_tokens || 0);
  const cacheSub = l.cache_tokens ? ` · 缓 ${fmtTotal(l.cache_tokens)}` : "";
  const tokTitle = `输入: ${fmtTotal(l.prompt_tokens || 0)} / 输出: ${fmtTotal(l.completion_tokens || 0)}${cacheSub}`;
  return `
    <div class="audit-cost-row">
      <span class="audit-token-tag">${I.coins} ${totalTok} Token</span>
      ${l.cache_tokens ? `<span class="audit-tok-text" title="${tokTitle}">(缓 ${fmtTotal(l.cache_tokens)})</span>` : ""}
    </div>
  `;
}

function renderLogModelCell(l) {
  const chanMeta = groupMeta(l.channel);
  const chanBadge = `<span class="badge ${chanMeta.cls}" style="margin-right:3px;">${esc(l.channel || "未知")}</span>`;
  const targetModel = esc(l.model_target || l.model_req || "-");
  const modelCode = `<span class="audit-model-code">${targetModel}</span>`;
  const streamTag = l.stream ? `<span class="tag neutral" style="font-size:9px; padding:1px 4px; margin-left:2px;">流</span>` : "";
  const uidBadge = l.uid ? `<span class="audit-uid-badge" title="${esc(l.uid)}">${esc(shortUid(l.uid))}</span>` : "";

  // 避免请求模型与命中渠道/模型重复：
  // 只有当客户端请求的模型去掉了前缀后与 targetModel 仍然不同（比如映射转发），才显示来源
  let reqMapHtml = "";
  if (l.model_req) {
    const cleanReq = l.model_req.includes("/") ? l.model_req.split("/").slice(1).join("/") : l.model_req;
    const cleanTarget = l.model_target && l.model_target.includes("/") ? l.model_target.split("/").slice(1).join("/") : (l.model_target || "");
    if (cleanReq && cleanTarget && cleanReq !== cleanTarget && l.model_req !== l.model_target) {
      reqMapHtml = `<div class="audit-redirect-badge" title="客户端请求原模型: ${esc(l.model_req)}" style="margin-top:3px;">↳ 映射自: ${esc(l.model_req)}</div>`;
    }
  }

  return `
    <div class="audit-route-cell">
      ${chanBadge}
      ${modelCode}
      ${streamTag}
      ${uidBadge}
    </div>
    ${reqMapHtml}
  `;
}

function renderLogErrorCell(l) {
  const isErr = l.status !== 200 || !!l.error;
  if (!isErr) {
    return `<div class="audit-ok-cell">${I.check}<span>正常完成</span></div>`;
  }
  let errText = l.error || `HTTP ${l.status} 异常响应`;
  errText = errText.replace(/[\r\n]+/g, " ").trim();
  const shortErr = errText.length > 55 ? errText.slice(0, 52) + "..." : errText;
  return `
    <div class="audit-error-action-cell">
      <span class="audit-error-preview" onclick="openAuditDetail('${esc(l.id)}')" title="点击查看详情与排查: ${esc(errText)}">
        ${esc(shortErr)}
      </span>
      <div class="audit-btn-group">
        <button class="btn tiny" onclick="copyAuditErrorById('${esc(l.id)}', event)" title="复制此报错信息发给 AI">
          <span data-ic="copy"></span>复制
        </button>
        <button class="btn tiny danger" onclick="openAuditDetail('${esc(l.id)}')" title="查看完整调用排查与上下文">
          排查
        </button>
      </div>
    </div>
  `;
}

function renderAuditLogsTable() {
  const tbody = $("auditLogsBody");
  if (!tbody) return;

  const selChannel = $("selAuditChannel") ? $("selAuditChannel").value : "";
  const selStatus = $("selAuditStatus") ? $("selAuditStatus").value : "";

  const filtered = allAuditLogs.filter(l => {
    const isErr = l.status !== 200 || !!l.error;
    if (auditTypeFilter === "error" && !isErr) return false;

    if (selChannel && l.channel !== selChannel) return false;

    if (selStatus) {
      if (selStatus === "error") {
        if (!isErr) return false;
      } else if (selStatus === "200") {
        if (l.status !== 200 || l.error) return false;
      } else {
        if (String(l.status) !== selStatus) return false;
      }
    }
    return true;
  });

  if (!filtered.length) {
    tbody.innerHTML = `<tr><td colspan="7" style="text-align:center; color:var(--muted); padding:30px;">未发现符合条件的调用记录</td></tr>`;
    return;
  }

  tbody.innerHTML = filtered.map(l => {
    const isErr = l.status !== 200 || !!l.error;
    const rowCls = isErr ? ' class="audit-error-row"' : "";
    const timeStr = l.time ? l.time.slice(11, 19) : "-";
    const statusCls = l.status === 200 ? "s200" : (l.status === 429 ? "s429" : (l.status === 502 ? "s502" : (l.status === 409 ? "s409" : (l.status === 500 || l.status === 503 ? "s500" : "sErr"))));

    const durStr = l.duration_ms < 1000 ? `${l.duration_ms}ms` : `${(l.duration_ms / 1000).toFixed(1)}s`;
    let ttftHtml = "";
    if (l.ttft_ms) {
      const ttftStr = l.ttft_ms < 1000 ? `${l.ttft_ms}ms` : `${(l.ttft_ms / 1000).toFixed(1)}s`;
      const ttftColor = l.ttft_ms < 5000 ? "var(--ok, green)" : (l.ttft_ms < 15000 ? "var(--warn, orange)" : "var(--danger, red)");
      ttftHtml = `<div style="font-size:10px; color:${ttftColor}; font-family:var(--mono);" title="首字到达时间">首字 ${ttftStr}</div>`;
    }

    const clientSnippet = l.key_snippet ? `<div style="font-size:10px; color:var(--muted); font-family:var(--mono);">${esc(l.key_snippet)}</div>` : "";

    return `
      <tr${rowCls}>
        <td style="font-family:var(--mono); font-size:11px; color:var(--muted);" title="${esc(l.time || '')}">${timeStr}</td>
        <td><strong>${esc(l.client || "Default")}</strong>${clientSnippet}</td>
        <td>${renderLogModelCell(l)}</td>
        <td style="font-family:var(--mono); font-size:11px;"><div>${durStr}</div>${ttftHtml}</td>
        <td>${renderLogCostCell(l)}</td>
        <td><span class="status-pill ${statusCls}">${l.status}</span></td>
        <td>${renderLogErrorCell(l)}</td>
      </tr>
    `;
  }).join("");

  if (window.__fillIcons) window.__fillIcons(tbody);
}

function updateAuditChannelSelect() {
  const sel = $("selAuditChannel");
  if (!sel) return;
  const curVal = sel.value;
  const channels = new Set();
  allAuditLogs.forEach(l => { if (l.channel) channels.add(l.channel); });
  Object.keys(CHANNEL_METAS || {}).forEach(c => channels.add(c));

  const sorted = Array.from(channels).sort();
  sel.innerHTML = `<option value="">全部渠道</option>` + sorted.map(c => {
    const name = (CHANNEL_METAS[c] && CHANNEL_METAS[c].name) || c;
    const selAttr = c === curVal ? " selected" : "";
    return `<option value="${esc(c)}"${selAttr}>${esc(name)}</option>`;
  }).join("");
}

async function loadAuditLogs(isManual = false) {
  const btn = $("btnRefreshAuditLogs");
  const ic = btn ? btn.querySelector("[data-ic]") : null;
  if (isManual && btn) {
    btn.classList.add("is-loading");
    if (ic) ic.classList.add("is-spinning");
    btn.disabled = true;
  }
  try {
    const data = await api("/api/audit/logs?limit=200");
    allAuditLogs = data.logs || [];

    const totalCount = allAuditLogs.length;
    const errorCount = allAuditLogs.filter(l => l.status !== 200 || l.error).length;
    if ($("badgeAllLogsCount")) $("badgeAllLogsCount").textContent = totalCount;
    if ($("badgeErrorLogsCount")) $("badgeErrorLogsCount").textContent = errorCount;

    updateAuditChannelSelect();
    renderAuditLogsTable();

    if (isManual) {
      toast(`审计流水已更新（共 ${totalCount} 条记录${errorCount ? `，含 ${errorCount} 条异常` : ""}）`);
    }
  } catch (e) {
    if (isManual) {
      toast("刷新流水失败：" + e.message);
    }
  } finally {
    if (isManual && btn) {
      setTimeout(() => {
        btn.classList.remove("is-loading");
        if (ic) ic.classList.remove("is-spinning");
        btn.disabled = false;
      }, 350);
    }
  }
}

window.copyAuditErrorById = function(id, evt) {
  if (evt) evt.stopPropagation();
  const item = allAuditLogs.find(l => l.id === id);
  if (!item) { toast("未找到该条记录"); return; }
  const errContent = item.error || `HTTP ${item.status} 错误`;
  copyText(errContent, "报错信息");
};

function buildAuditReport(l) {
  const time = l.time ? l.time.replace("T", " ").replace(/\+.*/, "") : "-";
  const errBody = l.error || "(无具体上游报错体，返回状态码 " + l.status + ")";
  const meta = CHANNEL_METAS[l.channel];
  const chanName = (meta && meta.name) || l.channel || "未知";
  const credits = extractLogCredits(l);
  const costInfo = credits !== null ? `${credits} 积分` : `${l.total_tokens || 0} Token`;

  return `### ZeroGate 调用异常诊断报告
- **请求 ID**: \`${l.id || "-"}\`
- **发生时间**: ${time}
- **HTTP 状态**: \`${l.status}\`
- **命中渠道**: **${chanName}** (\`${l.channel || "-"}\`)
- **账号 UID**: \`${l.uid || "无 / 全部不可用"}\`
- **目标模型**: \`${l.model_target || "-"}\`
- **客户端请求模型**: \`${l.model_req || "-"}\`
- **客户端名称**: ${l.client || "Default"}
- **总耗时**: ${l.duration_ms || 0}ms${l.ttft_ms ? ` (首字: ${l.ttft_ms}ms)` : ""}
- **消耗计量**: ${costInfo}
- **错误详情与上游响应**:
\`\`\`
${errBody}
\`\`\`
*提示：可直接将本报告发送给 AI 助手快速排查网关路由或账号配置。*`;
}

function copyLatestError() {
  const latestErr = allAuditLogs.find(l => l.status !== 200 || l.error);
  if (!latestErr) {
    toast("近期暂无调用异常记录，网关运行平稳");
    return;
  }
  const report = buildAuditReport(latestErr);
  copyText(report, "最新报错 AI 诊断报告");
}

window.openAuditDetail = function(id) {
  const item = allAuditLogs.find(l => l.id === id);
  if (!item) { toast("未找到该条记录"); return; }
  currentDetailLog = item;

  const overlay = $("auditDetailOverlay");
  const content = $("auditDetailContent");
  const statusPill = $("auditDetailStatusPill");
  const idEl = $("auditDetailId");
  if (!overlay || !content) return;

  const statusCls = item.status === 200 ? "s200" : (item.status === 429 ? "s429" : (item.status === 502 ? "s502" : (item.status === 409 ? "s409" : (item.status === 500 || item.status === 503 ? "s500" : "sErr"))));
  if (statusPill) statusPill.innerHTML = `<span class="status-pill ${statusCls}">HTTP ${item.status}</span>`;
  if (idEl) idEl.textContent = `ID: ${item.id || "-"}`;

  const timeStr = item.time ? item.time.replace("T", " ").replace(/\+.*/, "") : "-";
  const durStr = `${item.duration_ms}ms${item.ttft_ms ? ` (首字 ${item.ttft_ms}ms)` : ""}`;
  const meta = CHANNEL_METAS[item.channel];
  const chanName = (meta && meta.name) || item.channel || "未知";
  const credits = extractLogCredits(item);
  const costStr = credits !== null ? `${credits} 积分 (${fmtTotal(item.total_tokens || 0)} tok)` : `${fmtTotal(item.total_tokens || 0)} Token (入 ${fmtTotal(item.prompt_tokens || 0)} / 出 ${fmtTotal(item.completion_tokens || 0)})`;

  let hint = "";
  if (item.status === 502) {
    hint = `<div style="margin:10px 0; padding:8px 12px; background:rgba(239,68,68,0.12); border-left:3px solid #ef4444; border-radius:4px; font-size:12px; color:#fca5a5;">
      <strong>【502 Bad Gateway 诊断提示】</strong>上游直连网络中断/超时、EOF 断开，或上游账号 Session 失效被登出。建议检查该账号的凭证状态或重试。
    </div>`;
  } else if (item.status === 409) {
    hint = `<div style="margin:10px 0; padding:8px 12px; background:rgba(234,88,12,0.12); border-left:3px solid #ea580c; border-radius:4px; font-size:12px; color:#fdba74;">
      <strong>【409 Conflict 诊断提示】</strong>检测到并发任务冲突或会话锁抢占，上游拒绝并发请求。
    </div>`;
  } else if (item.status === 429) {
    hint = `<div style="margin:10px 0; padding:8px 12px; background:rgba(245,158,11,0.12); border-left:3px solid #f59e0b; border-radius:4px; font-size:12px; color:#fde047;">
      <strong>【429 Too Many Requests 诊断提示】</strong>上游或本地频率超限、额度暂时耗尽，已触发自动退避。
    </div>`;
  } else if (item.status === 503) {
    hint = `<div style="margin:10px 0; padding:8px 12px; background:rgba(236,72,153,0.12); border-left:3px solid #ec4899; border-radius:4px; font-size:12px; color:#f9a8d4;">
      <strong>【503 账号池熔断/冷却】</strong>该渠道账号池全部不可用或处于短暂熔断冷却中，等待冷却计时归零后将自动恢复。
    </div>`;
  }

  const errRaw = item.error ? esc(item.error) : `<span style="color:var(--ok)">调用成功无报错</span>`;

  content.innerHTML = `
    <div class="audit-detail-grid">
      <div class="audit-detail-kv"><span class="k">请求时间</span><span class="v">${timeStr}</span></div>
      <div class="audit-detail-kv"><span class="k">总耗时 / 首字</span><span class="v">${durStr}</span></div>
      <div class="audit-detail-kv"><span class="k">客户端</span><span class="v">${esc(item.client || "Default")}${item.key_snippet ? ` (${item.key_snippet})` : ""}</span></div>
      <div class="audit-detail-kv"><span class="k">命中渠道 / 账号</span><span class="v">${esc(chanName)} / ${esc(shortUid(item.uid) || "无")}</span></div>
      <div class="audit-detail-kv"><span class="k">调度模型</span><span class="v">${esc(item.model_target || "-")}${item.stream ? " (流式)" : ""}</span></div>
      <div class="audit-detail-kv"><span class="k">客户端请求模型</span><span class="v">${esc(item.model_req || "-")}</span></div>
      <div class="audit-detail-kv" style="grid-column: span 2;"><span class="k">用量与消耗</span><span class="v">${costStr}</span></div>
    </div>
    ${hint}
    <div style="font-size:12px; font-weight:600; margin:10px 0 6px 0; color:var(--text-sub);">原始响应与报错详情 (可全选复制给 AI)：</div>
    <div class="audit-detail-err-box">${errRaw}</div>
  `;

  overlay.classList.remove("hidden");
};

function closeAuditDetail() {
  const overlay = $("auditDetailOverlay");
  if (overlay) overlay.classList.add("hidden");
  currentDetailLog = null;
}

function initAuditLogsEvents() {
  const btnAll = $("btnFilterAllLogs");
  const btnErr = $("btnFilterErrorLogs");
  if (btnAll) {
    btnAll.onclick = () => {
      auditTypeFilter = "all";
      btnAll.classList.add("active");
      if (btnErr) btnErr.classList.remove("active");
      renderAuditLogsTable();
    };
  }
  if (btnErr) {
    btnErr.onclick = () => {
      auditTypeFilter = "error";
      btnErr.classList.add("active");
      if (btnAll) btnAll.classList.remove("active");
      renderAuditLogsTable();
    };
  }

  if ($("selAuditChannel")) $("selAuditChannel").onchange = renderAuditLogsTable;
  if ($("selAuditStatus")) $("selAuditStatus").onchange = renderAuditLogsTable;

  if ($("btnCopyLatestError")) $("btnCopyLatestError").onclick = copyLatestError;

  if ($("btnRefreshAuditLogs")) $("btnRefreshAuditLogs").onclick = () => loadAuditLogs(true);

  if ($("btnCopyAuditReport")) {
    $("btnCopyAuditReport").onclick = () => {
      if (!currentDetailLog) return;
      copyText(buildAuditReport(currentDetailLog), "AI 诊断报告");
    };
  }
  if ($("btnCopyAuditRaw")) {
    $("btnCopyAuditRaw").onclick = () => {
      if (!currentDetailLog) return;
      copyText(currentDetailLog.error || `HTTP ${currentDetailLog.status}`, "原始报错信息");
    };
  }
  if ($("btnCloseAuditDetail")) $("btnCloseAuditDetail").onclick = closeAuditDetail;
  if ($("auditDetailOverlay")) {
    $("auditDetailOverlay").onclick = (e) => {
      if (e.target === $("auditDetailOverlay")) closeAuditDetail();
    };
  }
}

function setupAuditLogsPolling() {
  clearInterval(auditLogsTimer);
  auditLogsTimer = setInterval(() => {
    const activeTab = localStorage.getItem("zg_active_tab");
    if (activeTab === "logs" && $("chkAutoRefreshLogs") && $("chkAutoRefreshLogs").checked) {
      loadAuditLogs(false);
    }
  }, 4000);
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
          <td><span class="client-key-code" onclick="copyText('${esc(c.key)}', '${esc(c.name)} Key')" title="点击复制">${esc(c.key.slice(0, 10))}...${esc(c.key.slice(-4))}${I.copy}</span></td>
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
      ${i < cands.length - 1 ? `<span class="cand-arrow">${I.chevron}</span>` : ''}
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
      "claude-3-5-sonnet": ["traework/claude-3-5-sonnet", "workbuddy/claude-3.5-sonnet"],
      "claude-3.5-sonnet": ["traework/claude-3-5-sonnet", "workbuddy/claude-3.5-sonnet"],
      "claude-3-7-sonnet": ["traework/claude-3-7-sonnet"],
      "claude-3.7-sonnet": ["traework/claude-3-7-sonnet"],
      "claude-3-5-haiku": ["workbuddy/claude-3.5-haiku"],
      "claude-3.5-haiku": ["workbuddy/claude-3.5-haiku"],
      "deepseek-chat": ["codebuddy/deepseek-v4.1-flash", "qoder/glm-5.3-flash", "traework/glm-5.2"],
      "deepseek-v3": ["codebuddy/deepseek-v4.1-flash", "qoder/glm-5.3-flash", "traework/glm-5.2"],
      "deepseek-reasoner": ["codebuddy/deepseek-v4.1-flash", "qoder/glm-5.3-flash", "traework/glm-5.2"],
      "deepseek-r1": ["codebuddy/deepseek-v4.1-flash", "qoder/glm-5.3-flash", "traework/glm-5.2"],
      "workbuddy/deepseek-v4.1-flash": ["codebuddy/deepseek-v4.1-flash", "qoder/glm-5.3-flash"],
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
  if (window.__fillIcons) window.__fillIcons();
  initTabs();
  try {
    bind();
  } catch (err) {
    console.error("bind 异常:", err);
  }
  renderAddMenu();
  setupAuditLogsPolling();
  document.addEventListener("zg:themechange", () => {
    if (localStorage.getItem("zg_active_tab") === "usage") loadTokenAnalytics();
  });
  try {
    await loadState();
    await loadDashboard();
    await loadFees();
  } catch (e) {
    console.error("加载状态异常:", e);
    toast("无法连接后台服务：" + e.message);
  }
  if (window.__fillIcons) window.__fillIcons();
})();

function fmtTokens(n) {
  if (n == null || isNaN(n)) return "0";
  n = Number(n);
  if (n >= 1e8) return (n / 1e8).toFixed(2).replace(/\.00$/, "") + " 亿";
  if (n >= 1e4) return (n / 1e4).toFixed(1).replace(/\.0$/, "") + " 万";
  return n.toLocaleString();
}

function fmtTotal(n) {
  return fmtTokens(n);
}

// ============================================================
// 剩余资产总览（积分折算 token）
// ============================================================

async function loadAssets() {
  try {
    const res = await fetch("/api/assets");
    if (!res.ok) return;
    const data = await res.json();
    const channels = data.channels || [];

    // 1. Token 核心储备库 (zcode, atria 等实测 1:1 Token 渠道)
    const tokenChs = channels.filter(c => c.convertible || c.channel === "zcode" || c.channel === "atria");
    const totalTokens = tokenChs.reduce((s, c) => s + (c.tokens || c.credits || 0), 0);
    const heroTokEl = $("assetHeroTokens");
    if (heroTokEl) heroTokEl.textContent = fmtTotal(totalTokens) + " Token";
    const heroCredEl = $("assetHeroCredits");
    if (heroCredEl) {
      heroCredEl.textContent = `来自 ${tokenChs.map(c => (CHANNEL_METAS[c.channel] && CHANNEL_METAS[c.channel].name) || c.channel).join(" + ")} · 1 积分 = 1 Token（直通官方）`;
    }

    const tokTb = $("tokenAssetsBody");
    if (tokTb) {
      tokTb.innerHTML = tokenChs.map(c => {
        const meta = CHANNEL_METAS[c.channel] || { name: c.channel, color: "#6366f1" };
        const acct = `${c.healthy}/${c.accounts}`;
        const note = c.note || "1 积分 = 1 Token · 直通上游";
        return `
          <tr>
            <td><span class="badge" style="background:${meta.color}1a; color:${meta.color};">${esc(meta.name)}</span></td>
            <td><strong>${acct}</strong> 账号在线</td>
            <td><strong style="color:var(--ok); font-family:var(--mono); font-size:14px;">${fmtTotal(c.tokens || c.credits)}</strong> Token</td>
            <td><span class="assets-note">${esc(note)}</span></td>
          </tr>`;
      }).join("") || `<tr><td colspan="4" style="text-align:center; color:var(--muted); padding:16px;">暂无 Token 储备渠道</td></tr>`;
    }

    // 2. 各渠道配额与积分池 (workbuddy, traework, qoder, codebuddy, autoclaw)
    const creditChs = channels.filter(c => !c.convertible && c.channel !== "zcode" && c.channel !== "atria");
    const totalCredits = creditChs.reduce((s, c) => s + (c.credits || 0), 0);
    const heroCreditsTotalEl = $("assetHeroCreditsTotal");
    if (heroCreditsTotalEl) heroCreditsTotalEl.textContent = fmtTotal(totalCredits) + " 积分/额度";
    if ($("assetsUpdatedAt")) $("assetsUpdatedAt").textContent = "更新于 " + (data.updated_at || "");

    const credTb = $("creditAssetsBody");
    if (credTb) {
      credTb.innerHTML = creditChs.map(c => {
        const meta = CHANNEL_METAS[c.channel] || { name: c.channel, color: "#64748b" };
        const acct = `${c.healthy}/${c.accounts}`;
        const note = c.note || "按量扣除 / 每日配额制";
        return `
          <tr>
            <td><span class="badge" style="background:${meta.color}1a; color:${meta.color};">${esc(meta.name)}</span></td>
            <td><strong>${acct}</strong> 账号在线</td>
            <td><strong style="color:var(--chart-credit); font-family:var(--mono); font-size:14px;">${fmtTotal(c.credits)}</strong></td>
            <td><span class="assets-note">${esc(note)}</span></td>
          </tr>`;
      }).join("") || `<tr><td colspan="4" style="text-align:center; color:var(--muted); padding:16px;">暂无积分渠道</td></tr>`;
    }

    renderRunway();
  } catch (e) { /* 面板加载失败静默 */ }
}

// ============================================================
// Token 资产大屏与高级图表引擎 (纯原生 SVG，零依赖，高颜值)
// ============================================================
// unit 标记该渠道余额的计量单位：token = 余额即 token；credit = 积分/额度。
// CHANNEL_METAS、unitOf 与 isTokenUnit 见前置全局定义区（供审计流水与大屏共用）。

// 本地时区 YMD（后端按日聚合用本地日期，不能用 toISOString 的 UTC 日期）
function localYMD(d) {
  const t = d || new Date();
  return t.getFullYear() + "-" + String(t.getMonth() + 1).padStart(2, "0") + "-" + String(t.getDate()).padStart(2, "0");
}

// 最近一次 usage 响应缓存：续航估算用（loadAssets 与 loadTokenAnalytics 都触发重算）
let _lastAccounts = [];

async function loadTokenAnalytics() {
  try {
    const [tokenData, auditAnalytics, usageData] = await Promise.all([
      api("/api/token_usage").catch(() => ({})),
      api("/api/audit/analytics").catch(() => ({})),
      api("/api/usage").catch(() => ({ accounts: [] })),
    ]);

    const accounts = usageData.accounts || [];
    _lastAccounts = accounts;

    renderTokenHero(tokenData.summary || {}, tokenData.models || {}, tokenData.channels || {});
    renderCreditSummary(accounts);
    renderCreditTrend(accounts);
    renderTokenTrend(auditAnalytics.daily_trend || [], accounts);
    renderSavingsTrend(auditAnalytics.daily_trend || [], tokenData.models || {});
    renderTokenHeatmap(auditAnalytics.daily_trend || [], accounts);
    renderChannelDonut(tokenData.channels || auditAnalytics.channel_tokens || {});
    renderTopModels(tokenData.models || {}, auditAnalytics.top_models || []);
    renderLowBalanceAccounts(accounts);
    renderAcctProductivity(accounts);
    renderRunway();
  } catch (e) {
    console.error("loadTokenAnalytics error:", e);
  }
}

// ---------------------------------------------------------------------------
// 模型官方 API 单价（2026-09-19 网上检索，USD / 1M tokens；人民币计价模型按 7.2 汇率折算）
// 匹配：模型名小数点归一为 - 后按数组顺序找第一个子串命中（注意具体型号必须排在系列名前）。
// 来源：docs.z.ai / api-docs.deepseek.com / developers.openai.com / volcengine /
//       help.aliyun.com(百炼) / platform.kimi.com / anthropic.com / ai.google.dev 转述
// ---------------------------------------------------------------------------
const USD_CNY = 7.2;
const MODEL_PRICES = [
  { re: "glm-5-3-flash",       in: 0.11, cache: 0.032, out: 0.39 },  // bigmodel.cn GLM-5.3-Flash 刊例 ¥0.8/¥2.8
  { re: "glm-5-3-flashx",      in: 0.21, cache: 0.06,  out: 0.69 },  // 估计档（介于 Flash 与标准之间）
  { re: "glm-5-3",             in: 0.97, cache: 0.18,  out: 3.06 },  // bigmodel.cn GLM-5.3 刊例 ¥7/¥22
  { re: "glm-5-2",             in: 0.97, cache: 0.18,  out: 3.06 },
  { re: "glm-4",               in: 0.60, cache: 0.11,  out: 2.20 },  // GLM-4.6 档
  { re: "zai",                 in: 0.11, cache: 0.032, out: 0.39 },  // zai_auto/zai-auto 系渠道按 GLM-Flash 国内档
  { re: "hy3-preview",         in: 0.167, cache: 0.056, out: 0.556 }, // 腾讯混元 Hy3 preview（WorkBuddy auto 默认，TokenHub ¥1.2/¥4）
  { re: "doubao-seed-2-1-pro",   in: 0.83, cache: 0.17, out: 4.17 }, // 火山 ¥6 / 缓存¥1.2 / ¥30
  { re: "doubao-seed-2-1-turbo", in: 0.42, cache: 0.08, out: 2.08 }, // 火山 ¥3 / 缓存¥0.6 / ¥15
  { re: "doubao",                in: 0.42, cache: 0.08, out: 2.08 }, // 豆包其余型号按 Turbo 档估
  { re: "deepseek-v4-pro",     in: 0.66, cache: 0.022, out: 1.98 },  // DeepSeek 官方谷时（峰时 1.32/3.96）
  { re: "deepseek",            in: 0.15, cache: 0.003, out: 0.60 },  // V4.1-Flash 谷时（峰时 0.30/1.20）
  { re: "gpt-5-6-sol",         in: 4.00, cache: 1.00,  out: 20.00 }, // OpenAI GPT-5.6 Sol（降价后）
  { re: "gpt-5-6-terra",       in: 2.50, cache: 0.25,  out: 15.00 },
  { re: "gpt-5-6-luna",        in: 1.00, cache: 0.10,  out: 5.00 },  // 输出价为发布档估计
  { re: "gpt-5",               in: 1.25, cache: 0.13,  out: 10.00 }, // GPT-5.5 等旧档估计
  { re: "gpt-4o",              in: 2.50, cache: 1.25,  out: 10.00 },
  { re: "claude-3-7-sonnet",   in: 3.00, cache: 0.30,  out: 15.00 },
  { re: "claude-3-5-sonnet",   in: 3.00, cache: 0.30,  out: 15.00 },
  { re: "claude-3-5-haiku",    in: 0.80, cache: 0.08,  out: 4.00 },
  { re: "claude-opus",         in: 15.00, cache: 1.50, out: 75.00 },
  { re: "qwen3-8-max",         in: 1.67, cache: 0.21,  out: 6.25 },  // 百炼 ¥12 / 缓存¥1.5 / ¥44.97
  { re: "qwen3-8-flash",       in: 0.14, cache: 0.03,  out: 0.42 },  // 百炼 ¥1 / ¥3
  { re: "kimi-k3",             in: 2.78, cache: 0.28,  out: 13.89 }, // Kimi ¥20 / 缓存¥2 / ¥100
  { re: "kimi",                in: 1.67, cache: 0.28,  out: 6.94 },  // K2.6 等旧档估计
  { re: "gemini-3-5-flash",    in: 1.50, cache: 0.15,  out: 9.00 },
  { re: "gemini",              in: 0.60, cache: 0.06,  out: 3.00 },  // Flash-Lite 档
  // 渠道主力模型档：历史 CSV 行没记模型名，后端以 "<渠道> (历史)" 记账，
  // 按该渠道实际跑的主力模型档估算（必须排在上面型号条目之后，避免抢匹配 "渠道/模型" 前缀）。
  { re: "zcode",                in: 0.11, cache: 0.032, out: 0.39 },
  { re: "atria",                in: 0.11, cache: 0.032, out: 0.39 },
  { re: "autoclaw",             in: 0.11, cache: 0.032, out: 0.39 },
  { re: "traework",             in: 0.42, cache: 0.08,  out: 2.08 },
  { re: "workbuddy",            in: 0.15, cache: 0.003, out: 0.60 },
  { re: "codebuddy",            in: 0.833, cache: 0.042, out: 2.50 }, // CodeBuddy(国际) 后端混元 Hy4（TokenHub ¥6/缓存¥0.3/¥18）
  { re: "qoder",                in: 0.14, cache: 0.03,  out: 0.42 },
];
// 未收录且无渠道前缀的模型：按中档模型估计
const FALLBACK_PRICE = { re: "", in: 1.00, cache: 0.10, out: 4.00 };

// 价格表可经 web/pricing.json 单独维护：存在则覆盖内置表（无需改逻辑代码），
// 加载前先用内置表兜底；pricing.json 未随 embed 打包时静默忽略。
fetch("./pricing.json")
  .then((r) => (r.ok ? r.json() : Promise.reject(new Error("no pricing.json"))))
  .then((rows) => {
    if (Array.isArray(rows) && rows.length) {
      MODEL_PRICES.length = 0;
      MODEL_PRICES.push(...rows);
    }
  })
  .catch(() => {});

function modelPrice(model) {
  let n = String(model || "").toLowerCase().replace(/\./g, "-");
  if (n === "auto") n = "hy3-preview"; // WorkBuddy auto 路由不暴露底层模型，按默认混元 Hy3 记
  n = n.replace(/_/g, "-");            // zai_auto 等下划线归一连字符
  for (const p of MODEL_PRICES) {
    if (n.includes(p.re)) return p;
  }
  return FALLBACK_PRICE;
}

// 按 输入/输出/缓存命中 用量折算官方 API 美元成本（单价 USD/1M）。
// GLM/DeepSeek/OpenAI 的 prompt_tokens 通常【包含】缓存命中部分，而 Anthropic 两者分开：
// 统一按 "缓存命中 ≤ 输入 则从输入中扣除" 归一，避免缓存被按输入价重复计费。
// 白嫖估值从低：上游不回传缓存字段（如 codebuddy/workbuddy/qoder）≠ 无缓存——
// 同模型正常回传的渠道实测命中率 96~98%。命中率 < 30% 视为上报不完整（部分响应
// 才带缓存字段），统一按 90% 折算补齐，让估值落在真实开销的保守下沿而非最坏上限。
const EST_CACHE_HIT = 0.9;
const EST_CACHE_MIN_RATE = 0.3;
function priceUSD(model, prompt, completion, cacheRead) {
  const p = modelPrice(model);
  let cache = Math.max(0, Math.min(cacheRead, prompt));
  if (prompt > 0 && cache / prompt < EST_CACHE_MIN_RATE) cache = prompt * EST_CACHE_HIT;
  const plainInput = prompt - cache;
  return (plainInput * p.in + completion * p.out + cache * p.cache) / 1e6;
}

// 1. Hero 财富看板与价值折算
function renderTokenHero(summary, models, channels) {
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

  // 分渠道缓存命中率：同一「命中率 X%」数字会掩盖渠道差异——qoder/workbuddy
  // 等上游不回传缓存字段（命中率显示 0% 是统计缺失而非无缓存），traework 有真实命中。
  if ($("tkHeroCacheRate")) {
    const chRates = Object.entries(channels || {})
      .filter(([, s]) => (Number(s.prompt_tokens) + Number(s.cache_read_tokens)) > 0)
      .map(([k, s]) => {
        const p = Number(s.prompt_tokens) || 0;
        const c = Number(s.cache_read_tokens) || 0;
        if (c === 0 && p > 0) return `${k} 未上报`;
        return `${k} ${Math.round((c / (p + c)) * 100)}%`;
      });
    $("tkHeroCacheRate").textContent = `命中率 ${cacheRate}%`;
    if (chRates.length > 0) {
      $("tkHeroCacheRate").title = `分渠道缓存命中率：${chRates.join("、")}\n（未上报 = 上游 usage 不含缓存字段，非真实无缓存）`;
    }
  }

  // 白嫖节省：优先用 token_usage 的按模型全量统计 × 各模型官方 API 单价逐模型折算；
  // 没有按模型数据（旧版后端）时退回全局 输入/输出/缓存 拆分 × 中档单价。
  let savingsUsd = 0;
  const breakdown = [];
  const modelEntries = Object.entries(models || {});
  if (modelEntries.length > 0) {
    for (const [name, m] of modelEntries) {
      const usd = priceUSD(
        name,
        Number(m.prompt_tokens || 0),
        Number(m.completion_tokens || 0),
        Number(m.cache_read_tokens || 0),
      );
      savingsUsd += usd;
      if (usd > 0.0001) {
        const label = name.includes(" (历史)")
          ? name.replace(" (历史)", "·历史用量（按渠道主力模型档）")
          : (name || "未知模型");
        breakdown.push({ name: label, usd, tokens: Number(m.total_tokens || 0) });
      }
    }
  } else {
    savingsUsd = priceUSD("", prompt, comp, cache);
    breakdown.push({ name: "全局拆分估算", usd: savingsUsd, tokens: total });
  }
  breakdown.sort((a, b) => b.usd - a.usd);

  const tipLines = breakdown.slice(0, 6).map(b =>
    `${b.name}：¥${(b.usd * USD_CNY).toFixed(2)}（${fmtTokens(b.tokens)} tokens）`);
  const tip = `按各模型官方 API 单价折算（2026-09 检索，汇率 1 USD=${USD_CNY} CNY）\n${tipLines.join("\n")}`;

  const savingsYuan = (savingsUsd * USD_CNY).toFixed(2);
  const savingsUsdStr = savingsUsd.toFixed(2);
  if ($("tkHeroSavings")) {
    $("tkHeroSavings").textContent = `约为你白嫖节省: ￥${savingsYuan} (约 $${savingsUsdStr})`;
    $("tkHeroSavings").title = tip;
  }
}

// 1.5 消耗/变动汇总卡片：积分族与 Token 族分列（计量单位不同，绝不合并求和）
//   - 积分族：余额为积分/额度，看「积分净变动」（正=消耗，负=签到/赠送入账）
//   - Token 族：余额即 token，看「真实 Token 消耗」（上游 usage 上报的 tokens[]，
//     而非余额 delta——后者被套餐重置/发放事件（如 ±3 亿）主导，不是消耗）
function renderCreditSummary(accounts) {
  const acc = accounts || [];
  const credit = acc.filter(a => !isTokenUnit(a.kind));
  const token = acc.filter(a => isTokenUnit(a.kind));
  const now = new Date();
  const key = i => localYMD(new Date(now.getTime() - i * 86400 * 1000));

  // 积分族：逐日净变动
  const cDelta = {};
  credit.forEach(a => (a.daily || []).forEach(d => {
    if (!d.day) return;
    cDelta[d.day] = (cDelta[d.day] || 0) + Number(d.delta || 0);
  }));
  // Token 族：逐日真实 Token 消耗（总量含缓存命中输入，与 Token 趋势图口径一致）
  const tToken = {};
  token.forEach(a => (a.tokens || []).forEach(t => {
    if (!t.day) return;
    tToken[t.day] = (tToken[t.day] || 0) + Number(t.total || 0) + Number(t.cache_read || 0);
  }));

  const sumRange = (map, days) => {
    let s = 0;
    for (let i = 0; i < days; i++) s += map[key(i)] || 0;
    return s;
  };
  const setV = (id, v) => { const el = $(id); if (el) el.textContent = v; };
  const setS = (id, t) => { const el = $(id); if (el) el.textContent = t; };

  // 积分族（单位：积分）
  const cToday = credit.reduce((s, a) => s + Number(a.today || 0), 0);
  const c7 = sumRange(cDelta, 7), c14 = sumRange(cDelta, 14);
  setV("creditToday", fmtTotal(Math.round(cToday)));
  setV("credit7d", fmtTotal(Math.round(c7)));
  setV("credit14d", fmtTotal(Math.round(c14)));
  setS("creditTodaySub", `${key(0)} 当日`);
  setS("credit7dSub", `日均 ${fmtTotal(Math.round(c7 / 7))}/天`);
  setS("credit14dSub", `日均 ${fmtTotal(Math.round(c14 / 14))}/天`);
  const cTip = "积分族（workbuddy/traework/qoder/codebuddy/autoclaw）余额以积分/额度计，无法折算 token。正=消耗，负=签到/赠送入账。";
  ["creditToday", "credit7d", "credit14d"].forEach(id => { const el = $(id); if (el) el.title = cTip; });

  // Token 族（单位：token）
  const tToday = tToken[key(0)] || 0;
  const t7 = sumRange(tToken, 7), t14 = sumRange(tToken, 14);
  setV("tokenToday", fmtTokens(tToday));
  setV("token7d", fmtTokens(t7));
  setV("token14d", fmtTokens(t14));
  setS("tokenTodaySub", `${key(0)} 当日`);
  setS("token7dSub", `日均 ${fmtTokens(Math.round(t7 / 7))}/天`);
  setS("token14dSub", `日均 ${fmtTokens(Math.round(t14 / 14))}/天`);
  const tTip = "Token 族（zcode/atria）余额即 token，消耗取上游 usage 上报的真实 token 用量（非余额变化）。";
  ["tokenToday", "token7d", "token14d"].forEach(id => { const el = $(id); if (el) el.title = tTip; });
}

// 1.6 近 14 天积分净变动趋势（仅积分族；token 族余额 delta 被套餐重置污染，不纳入）
//     SVG 柱状图：正 delta 消耗向上、负 delta 入账向下（使用高端渐变色彩与柔和基准线）
function renderCreditTrend(accounts) {
  const wrap = $("creditTrendChartWrap");
  if (!wrap) return;

  const dayMap = {};
  (accounts || []).filter(a => !isTokenUnit(a.kind)).forEach(a => {
    (a.daily || []).forEach(d => {
      if (!d.day) return;
      const m = dayMap[d.day] || (dayMap[d.day] = { delta: 0, remain: 0 });
      m.delta += Number(d.delta || 0);
      m.remain += Number(d.remain || 0);
    });
  });

  const now = new Date();
  const days = [];
  for (let i = 13; i >= 0; i--) {
    const key = localYMD(new Date(now.getTime() - i * 86400 * 1000));
    const m = dayMap[key] || { delta: 0, remain: 0 };
    days.push({ day: key, label: key.slice(5), delta: m.delta, remain: m.remain });
  }

  const hasPos = days.some(d => d.delta > 0);
  const hasNeg = days.some(d => d.delta < 0);
  if (!hasPos && !hasNeg) {
    wrap.innerHTML = `<div class="empty-chart">暂无积分族按日数据，跑起业务后这里会显示积分净变动</div>`;
    return;
  }

  const maxPos = Math.max(...days.map(d => Math.max(0, d.delta)));
  const maxNeg = Math.max(...days.map(d => Math.max(0, -d.delta)));

  const W = 740, H = 220;
  const padLeft = 55, padRight = 15, padTop = 20, padBottom = 28;
  const chartW = W - padLeft - padRight;
  const chartH = H - padTop - padBottom;

  // 0 轴位置
  let zeroY = padTop + chartH, scalePos = 0, scaleNeg = 0;
  if (hasPos && hasNeg) {
    const total = maxPos + maxNeg || 1;
    zeroY = padTop + (maxPos / total) * chartH;
    scalePos = chartH / total;
    scaleNeg = chartH / total;
  } else if (hasPos) {
    scalePos = chartH / (maxPos || 1);
  } else {
    zeroY = padTop;
    scaleNeg = chartH / (maxNeg || 1);
  }
  const yOf = v => v >= 0 ? zeroY - v * scalePos : zeroY + (-v) * scaleNeg;

  const cGridLine = "rgba(255, 255, 255, 0.05)";
  const cLabel = cssVar("--chart-label") || "rgba(255, 255, 255, 0.45)";

  let grid = "";
  const ticks = [];
  if (hasPos) ticks.push(maxPos);
  if (hasNeg) ticks.push(-maxNeg);
  ticks.forEach(v => {
    const y = yOf(v);
    grid += `
      <line x1="${padLeft}" y1="${y}" x2="${W - padRight}" y2="${y}" stroke="${cGridLine}" stroke-width="1" />
      <text x="${padLeft - 8}" y="${y + 4}" text-anchor="end" font-size="10" fill="${cLabel}" font-family="var(--mono)">${fmtTotal(Math.round(Math.abs(v)))}</text>
    `;
  });

  const stepW = chartW / days.length;
  const barW = Math.max(16, Math.min(28, stepW * 0.58));
  let bars = "";
  days.forEach((d, i) => {
    const x = padLeft + i * stepW + (stepW - barW) / 2;
    const dTxt = d.delta >= 0
      ? `净消耗 ${fmtTotal(Math.round(d.delta))} 积分`
      : `净入账 ${fmtTotal(Math.round(-d.delta))} 积分`;
    const tip = `${d.day}\n${dTxt}\n当日末余额 ${fmtTotal(Math.round(d.remain))}`;
    let rect = "";
    if (d.delta > 0) {
      const h = Math.max(3, d.delta * scalePos);
      rect = `<rect x="${x}" y="${zeroY - h}" width="${barW}" height="${h}" fill="url(#gradCreditExpense)" rx="4" />`;
    } else if (d.delta < 0) {
      const h = Math.max(3, (-d.delta) * scaleNeg);
      rect = `<rect x="${x}" y="${zeroY}" width="${barW}" height="${h}" fill="url(#gradCreditIncome)" rx="4" />`;
    }
    bars += `
      <g class="chart-bar-group">
        <title>${esc(tip)}</title>
        ${rect}
        <text x="${x + barW / 2}" y="${H - 10}" text-anchor="middle" font-size="10" fill="${cLabel}" font-family="var(--mono)">${d.label}</text>
      </g>
    `;
  });

  wrap.innerHTML = `
    <svg viewBox="0 0 ${W} ${H}" style="width:100%; height:100%; display:block;" preserveAspectRatio="xMidYMid meet">
      <defs>
        <linearGradient id="gradCreditIncome" x1="0" y1="0" x2="0" y2="1">
          <stop offset="0%" stop-color="#34d399" stop-opacity="0.95" />
          <stop offset="100%" stop-color="#059669" stop-opacity="0.65" />
        </linearGradient>
        <linearGradient id="gradCreditExpense" x1="0" y1="0" x2="0" y2="1">
          <stop offset="0%" stop-color="#fb7185" stop-opacity="0.95" />
          <stop offset="100%" stop-color="#e11d48" stop-opacity="0.75" />
        </linearGradient>
      </defs>
      ${grid}
      <line x1="${padLeft}" y1="${zeroY}" x2="${W - padRight}" y2="${zeroY}" stroke="rgba(255, 255, 255, 0.12)" stroke-width="1.2" />
      ${bars}
    </svg>
  `;
}

// 统一按日 Token 用量对账汇总（统一趋势图与热力图的统计口径，彻底解决两图数据不一致问题）
function buildUnifiedDailyTokenMap(auditDaily, accounts) {
  const map = {};

  // 1. 优先以审计日志 auditDaily 为网关级全量事实源
  (auditDaily || []).forEach(d => {
    if (!d.day) return;
    const prompt = Number(d.prompt || 0);
    const comp = Number(d.completion || 0);
    const cache = Number(d.cache || 0);
    // 后端 audit 的 d.total 为 prompt + comp，包含 cache 的全口径消耗为 prompt + comp + cache
    const total = prompt + comp + cache;
    map[d.day] = { prompt, comp, cache, total };
  });

  // 2. 从 accounts.tokens 聚合各账号的按日明细（对同一天进行账号间累加求和）
  const acctDayMap = {};
  (accounts || []).forEach(a => {
    (a.tokens || []).forEach(t => {
      if (!t.day) return;
      const p = Number(t.prompt || 0);
      const c = Number(t.completion || 0);
      const k = Number(t.cache_read || 0) + Number(t.cache_write || 0);
      const tot = (t.total != null && Number(t.total) > 0) ? Number(t.total) : (p + c + k);
      if (!acctDayMap[t.day]) {
        acctDayMap[t.day] = { prompt: 0, comp: 0, cache: 0, total: 0 };
      }
      acctDayMap[t.day].prompt += p;
      acctDayMap[t.day].comp += c;
      acctDayMap[t.day].cache += k;
      acctDayMap[t.day].total += tot;
    });
  });

  // 3. 合并：若该日 audit 无数据（或 total 为 0），取 accounts 聚合；若两者皆有，取较大值以兼顾历史补录
  for (const [day, val] of Object.entries(acctDayMap)) {
    if (!map[day] || map[day].total === 0) {
      map[day] = val;
    } else if (val.total > map[day].total) {
      map[day] = val;
    }
  }

  return map;
}

// 2. 近 14 天 Token 每日消耗趋势（SVG 堆叠柱状图）
function renderTokenTrend(auditDaily, accounts) {
  const wrap = $("tokenTrendChartWrap");
  if (!wrap) return;

  const dayMap = buildUnifiedDailyTokenMap(auditDaily, accounts);

  // 生成近 14 天序列（本地时区日期，与后端 audit_daily.json 的日 key 对齐）
  const days = [];
  const now = new Date();
  for (let i = 13; i >= 0; i--) {
    const key = localYMD(new Date(now.getTime() - i * 86400 * 1000));
    const item = dayMap[key] || { prompt: 0, comp: 0, cache: 0, total: 0 };
    days.push({ day: key, label: key.slice(5), ...item });
  }

  const maxTotal = Math.max(...days.map(d => d.total), 1000);

  const W = 740, H = 220;
  const padLeft = 55, padRight = 15, padTop = 20, padBottom = 28;
  const chartW = W - padLeft - padRight;
  const chartH = H - padTop - padBottom;

  let gridLines = "";
  const cGrid = cssVar("--chart-grid"), cLabel = cssVar("--chart-label");
  const cP = cssVar("--chart-prompt"), cC = cssVar("--chart-comp"), cK = cssVar("--chart-cache");
  for (let step = 0; step <= 4; step++) {
    const yVal = Math.round((maxTotal / 4) * step);
    const y = padTop + chartH - (step / 4) * chartH;
    gridLines += `
      <line x1="${padLeft}" y1="${y}" x2="${W - padRight}" y2="${y}" stroke="${cGrid}" stroke-width="1" />
      <text x="${padLeft - 8}" y="${y + 4}" text-anchor="end" font-size="10" fill="${cLabel}" font-family="var(--mono)">${fmtTokens(yVal)}</text>
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

    const tip = `${d.day}\n总计消耗: ${fmtTokens(d.total)}\n输入: ${fmtTokens(d.prompt)}\n输出: ${fmtTokens(d.comp)}\n缓存: ${fmtTokens(d.cache)}`;

    bars += `
      <g class="chart-bar-group">
        <title>${esc(tip)}</title>
        <rect x="${x}" y="${pY}" width="${barW}" height="${pHeight}" fill="${cP}" rx="2" />
        <rect x="${x}" y="${cY}" width="${barW}" height="${cHeight}" fill="${cC}" rx="2" />
        <rect x="${x}" y="${kY}" width="${barW}" height="${kHeight}" fill="${cK}" rx="2" />
        <text x="${x + barW / 2}" y="${H - 10}" text-anchor="middle" font-size="10" fill="${cLabel}">${d.label}</text>
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

// 2.5 Token 日历热力图（GitHub 贡献图风格：与趋势图 100% 同口径对账）
function renderTokenHeatmap(auditDaily, accounts) {
  const wrap = $("tokenHeatmapWrap");
  if (!wrap) return;

  const dayMap = buildUnifiedDailyTokenMap(auditDaily, accounts);

  // 区间：从最早有数据的日子的所在周（周日）往后画到今天，前置最多 2 周空格做上下文，总长最多 26 周
  const WEEK_MAX = 26, LEAD_WEEKS = 2;
  const today = new Date();
  today.setHours(0, 0, 0, 0);

  let firstData = null;
  for (const day of Object.keys(dayMap)) {
    const d = new Date(day + "T00:00:00");
    if (!isNaN(d) && d <= today && (firstData === null || d < firstData)) firstData = d;
  }
  if (!firstData) {
    wrap.innerHTML = `<div class="heatmap-empty-hint">暂无按日数据，跑起业务后这里会长出热力图</div>`;
    return;
  }

  // 起点对齐到 firstData 所在周的周日
  const gridStart = new Date(firstData);
  gridStart.setDate(gridStart.getDate() - gridStart.getDay() - LEAD_WEEKS * 7);

  const totalDays = Math.round((today - gridStart) / 86400e3) + 1;
  const nWeeks = Math.min(WEEK_MAX, Math.ceil(totalDays / 7));

  const tokensOf = d => (dayMap[localYMD(d)] ? dayMap[localYMD(d)].total : 0);
  const allVals = Object.values(dayMap).map(v => v.total).filter(v => v > 0).sort((a, b) => a - b);
  const q = p => allVals.length ? allVals[Math.min(allVals.length - 1, Math.floor(allVals.length * p))] : 0;
  const t1 = Math.max(q(0.25), 1), t2 = Math.max(q(0.5), t1 + 1), t3 = Math.max(q(0.75), t2 + 1);
  const level = v => v <= 0 ? 0 : v < t1 ? 1 : v < t2 ? 2 : v < t3 ? 3 : 4;
  const fmtDay = d => `${localYMD(d)}\n${fmtTokens(tokensOf(d))} tokens`;

  let weeksHtml = "";
  const monthCols = new Array(nWeeks).fill(null); // 每列对应的月份标签（仅该列含当月 1 号时标注）
  for (let w = 0; w < nWeeks; w++) {
    let col = "";
    for (let dow = 0; dow < 7; dow++) {
      const d = new Date(gridStart);
      d.setDate(d.getDate() + w * 7 + dow);
      if (d > today) {
        col += `<span class="heatmap-cell" style="visibility:hidden"></span>`;
        continue;
      }
      if (d.getDate() === 1 && !monthCols[w]) monthCols[w] = (d.getMonth() + 1) + "月";
      const lv = level(tokensOf(d));
      col += `<span class="heatmap-cell lvl${lv}" title="${esc(fmtDay(d))}"></span>`;
    }
    weeksHtml += `<div class="heatmap-week">${col}</div>`;
  }

  // 月份标签行：与格子列一一对应的固定宽度槽位，物理上不会重叠
  const slots = monthCols.map(m => `<span class="heatmap-month-slot">${m ? esc(m) : ""}</span>`).join("");
  wrap.innerHTML = `
    <div class="heatmap-monthstrip">
      <span class="heatmap-month-spacer"></span>${slots}
    </div>
    <div class="heatmap-flex">
      <div class="heatmap-weekday-col">
        <span></span><span>一</span><span></span><span>三</span><span></span><span>五</span><span></span>
      </div>
      <div class="heatmap-weeks">${weeksHtml}</div>
    </div>
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
      const meta = CHANNEL_METAS[k] || { name: k };
      items.push({ kind: k, name: meta.name, color: chColor(k), tokens });
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
        <circle cx="${cx}" cy="${cy}" r="${r}" fill="transparent" stroke="${cssVar("--chart-track")}" stroke-width="26" />
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

// 4. 热门模型白嫖榜 TOP 5（按各模型独立折算的节省金额降序，条形堆叠=输入/缓存/输出）
function renderTopModels(models, topModels) {
  const box = $("topModelsList");
  if (!box) return;

  // 优先用 /api/token_usage 的 models（含每模型 prompt/completion/cache_read，可精确折算）；
  // 结构不足时退回 audit 的 top_models（仅有总量，按全局比例拆分）。
  let rows = [];
  const modelEntries = Object.entries(models || {});
  if (modelEntries.length > 0) {
    rows = modelEntries.map(([name, m]) => {
      const prompt = Number(m.prompt_tokens || 0);
      const comp = Number(m.completion_tokens || 0);
      const cache = Number(m.cache_read_tokens || 0);
      const usd = priceUSD(name, prompt, comp, cache);
      return { model: name, prompt, comp, cache, total: Number(m.total_tokens || 0), usd };
    });
  } else if (Array.isArray(topModels) && topModels.length > 0) {
    const tot = topModels.reduce((s, m) => s + Number(m.total_tokens || 0), 0) || 1;
    rows = topModels.map(m => {
      const total = Number(m.total_tokens || 0);
      const prompt = total * 0.9, comp = total * 0.1, cache = 0;
      return { model: m.model, prompt, comp, cache, total, usd: priceUSD(m.model, prompt, comp, cache) };
    });
  }

  if (rows.length === 0) {
    box.innerHTML = `<div class="empty-hint">暂无模型调用数据</div>`;
    return;
  }

  rows.sort((a, b) => b.usd - a.usd);
  const top5 = rows.slice(0, 5);
  const badges = ["top1", "top2", "top3", "", ""];
  const maxUsd = (top5[0] && top5[0].usd) ? top5[0].usd : 1;

  box.innerHTML = top5.map((m, idx) => {
    const pct = Math.max(8, Math.round((m.usd / maxUsd) * 100));
    const denom = m.prompt + m.cache + m.comp || 1;
    const seg = (v, cls) =>
      v > 0 ? `<i class="rank-seg ${cls}" style="width:${(v / denom) * 100}%"></i>` : "";
    const pBar = seg(m.prompt, "seg-p") + seg(m.cache, "seg-cr") + seg(m.comp, "seg-c");
    const savingsHtml = m.usd > 0.0001
      ? `<span class="rank-save" title="按该模型官方 API 单价独立折算的等值节省">省 ¥${(m.usd * USD_CNY).toFixed(2)}</span>`
      : "";
    return `
      <div class="rank-item">
        <span class="rank-num ${badges[idx] || ""}">${idx + 1}</span>
        <div class="rank-info">
          <div class="rank-header">
            <span class="rank-name" title="${esc(m.model)}">${esc(m.model)}</span>
            <span class="rank-val">${fmtTokens(m.total)}${savingsHtml}</span>
          </div>
          <div class="rank-bar-bg">
            <div class="rank-bar-fill" style="width:${pct}%">${pBar}</div>
          </div>
        </div>
      </div>
    `;
  }).join("");
}

// 5. 账号余额与到期榜（按渠道真实计量单位标注 token / 积分，明确区分全额到期与部分临期套餐，避免过期恐慌）
function renderLowBalanceAccounts(accounts) {
  const box = $("lowBalanceList");
  if (!box) return;
  const all = (accounts || []).filter(a => Number(a.remain || 0) > 0 || Number(a.expire_at || 0) > 0);
  if (all.length === 0) {
    box.innerHTML = `<div class="empty-hint">暂无账号余额数据</div>`;
    return;
  }

  const now = Math.floor(Date.now() / 1000);

  // 预处理每个账号的临期详情：从后端字段或本地套餐明细缓存提取
  const enriched = all.map(a => {
    const detail = getExpiringDetail(a);
    const expireAt = detail.expireAt || Number(a.expire_at || 0);
    return {
      ...a,
      expire_at: expireAt,
      expiring_remain: detail.expRemain,
      expiring_name: detail.expName,
    };
  });

  // 排序：最快到期的优先（长期有效视为最不紧迫）；同日到期则余额少者优先
  const rank = a => (a.expire_at && a.expire_at > 0 && a.expire_at < 4102444800) ? a.expire_at : Infinity;
  const sortFn = (a, b) => {
    const ra = rank(a), rb = rank(b);
    if (ra !== rb) return ra - rb;
    return Number(a.remain || 0) - Number(b.remain || 0);
  };

  const list = enriched.sort(sortFn);
  const badges = ["top1", "top2", "top3", "", ""];

  box.innerHTML = list.slice(0, 8).map((a, idx) => {
    const gm = groupMeta(a.kind);
    const exp = formatExpire(a.expire_at);
    const remain = Math.max(0, Number(a.remain || 0));
    const total = Number(a.total || 0);
    const isTok = isTokenUnit(a.kind);
    const unitName = isTok ? "token" : "积分";

    // 判断是否为「部分临期」：临期额度 > 0 且严格小于总剩余
    const expRemain = Number(a.expiring_remain || 0);
    const hasExp = a.expire_at > 0 && a.expire_at < 4102444800;
    const isPartial = hasExp && expRemain > 0 && expRemain < remain;
    const safeRemain = isPartial ? Math.max(0, remain - expRemain) : (hasExp ? 0 : remain);

    // 格式化输出字符串
    const totalVal = isTok ? `${fmtTokens(remain)} token` : `${fmtTotal(remain)} 积分`;
    const expVal = isTok ? fmtTokens(expRemain) : fmtTotal(expRemain);
    const safeVal = isTok ? fmtTokens(safeRemain) : fmtTotal(safeRemain);

    // 进度条百分比（按总剩余占自身总额）
    const pct = Math.max(3, Math.min(100, Math.round(total > 0 ? (remain / total) * 100 : 100)));

    // 警示样式判定：若只是少量积分临期（<30%），进度条不报警（避免误导恐慌），仅当大部分临期或全额到期且<=7天时标红
    const expRatio = (remain > 0 && expRemain > 0) ? (expRemain / remain) : (hasExp ? 1 : 0);
    const urgent = hasExp && (a.expire_at - now) <= 7 * 86400 && (!isPartial || expRatio >= 0.3);
    const fillCls = urgent ? "background:linear-gradient(90deg, var(--danger), var(--warn))" : "";

    // 临期状态标贴与元信息
    let metaTagsHtml = "";
    if (isPartial) {
      metaTagsHtml = `
        <span class="tag ${exp.cls} exp-part-badge" onclick="showCreditDetail(event, '${esc(a.uid)}')" title="${esc(exp.full)} · 仅此套餐临期（${expRemain} ${unitName}），其余 ${safeVal} ${unitName} 仍有效；点击查看全部套餐明细" style="cursor:pointer;">
          ${I.alert} 仅 <b>${expVal}</b> ${unitName} ${esc(exp.text)} <span class="exp-subname">(${esc(shortPkgName(a.expiring_name))})</span>
        </span>
        <span class="tag expire-perm" title="其余套餐长期有效或更晚到期">${I.check} ${safeVal} 有效</span>
      `;
    } else if (hasExp) {
      metaTagsHtml = `
        <span class="tag ${exp.cls}" onclick="showCreditDetail(event, '${esc(a.uid)}')" title="${esc(exp.full)} · 点击查看套餐明细" style="cursor:pointer;">
          ${I.clock} 全部 ${esc(exp.text)}
        </span>
      `;
    } else {
      metaTagsHtml = `
        <span class="tag expire-perm" onclick="showCreditDetail(event, '${esc(a.uid)}')" title="长期有效 · 点击查看套餐明细" style="cursor:pointer;">
          ${I.check} 长期有效
        </span>
      `;
    }

    return `
      <div class="rank-item" title="${esc(a.uid)}">
        <span class="rank-num ${badges[idx] || ""}">${idx + 1}</span>
        <div class="rank-info">
          <div class="rank-header">
            <span class="rank-name"><span class="badge ${gm.cls}">${gm.name}</span> ${esc(a.nickname || shortUid(a.uid))}</span>
            <span class="rank-val" style="display:inline-flex;flex-direction:column;align-items:flex-end;gap:1px;">
              <span>${totalVal}</span>
              ${isPartial ? `<span style="font-size:11px;font-weight:400;color:var(--text-muted);">${safeVal} 长期有效</span>` : ""}
            </span>
          </div>
          <div class="rank-bar-bg">
            <div class="rank-bar-fill" style="width:${pct}%; ${fillCls}"></div>
          </div>
          <div class="rank-meta">
            ${metaTagsHtml}
            <code class="prod-uid" style="cursor:pointer;" onclick="showCreditDetail(event, '${esc(a.uid)}')" title="点击查看套餐明细">${esc(shortUid(a.uid))}</code>
          </div>
        </div>
      </div>`;
  }).join("");
}

// 5.5 近 14 天每日白嫖节省金额走势（每日 Token 消耗 × priceUSD 逐日折算为人民币，SVG 柱状图 + 14 天均线）
function renderSavingsTrend(auditDaily, tokenModels) {
  const wrap = $("savingsTrendChartWrap");
  if (!wrap) return;

  const dayMap = {};
  (auditDaily || []).forEach(d => {
    if (!d.day) return;
    dayMap[d.day] = {
      prompt: Number(d.prompt || 0),
      comp: Number(d.completion || 0),
      cache: Number(d.cache || 0),
    };
  });

  // 统一统计口径：提取真实用户模型分布的加权单价，避免硬编码高价 FALLBACK 造成 14 天金额远超总看板
  let wPromptCost = 0, totPrompt = 0;
  let wCompCost = 0, totComp = 0;
  let wCacheCost = 0, totCache = 0;
  Object.entries(tokenModels || {}).forEach(([name, m]) => {
    const p = modelPrice(name);
    const pr = Number(m.prompt_tokens || 0);
    const co = Number(m.completion_tokens || 0);
    const ca = Number(m.cache_read_tokens || 0);
    wPromptCost += (pr / 1e6) * p.in;
    wCompCost += (co / 1e6) * p.out;
    wCacheCost += (ca / 1e6) * p.cache;
    totPrompt += pr;
    totComp += co;
    totCache += ca;
  });

  const effInRate = totPrompt > 0 ? (wPromptCost / totPrompt) * 1e6 : 0.15;
  const effOutRate = totComp > 0 ? (wCompCost / totComp) * 1e6 : 0.60;
  const effCacheRate = totCache > 0 ? (wCacheCost / totCache) * 1e6 : 0.03;

  const calcDailyUSD = (prompt, comp, cache) => {
    let c = Math.max(0, Math.min(cache, prompt));
    if (prompt > 0 && c / prompt < EST_CACHE_MIN_RATE) c = prompt * EST_CACHE_HIT;
    const plain = prompt - c;
    return (plain * effInRate + comp * effOutRate + c * effCacheRate) / 1e6;
  };

  const now = new Date();
  const days = [];
  let sumCny = 0;
  for (let i = 13; i >= 0; i--) {
    const key = localYMD(new Date(now.getTime() - i * 86400 * 1000));
    const m = dayMap[key] || { prompt: 0, comp: 0, cache: 0 };
    const usd = calcDailyUSD(m.prompt, m.comp, m.cache);
    const cny = usd * USD_CNY;
    sumCny += cny;
    days.push({ day: key, label: key.slice(5), usd, cny, tokens: m.prompt + m.comp + m.cache });
  }

  const anyData = days.some(d => d.cny > 0.01);
  if (!anyData) {
    wrap.innerHTML = `<div class="empty-chart">暂无按日 Token 数据，跑起业务后这里会显示每日白嫖节省金额（折合人民币）</div>`;
    return;
  }

  const avgCny = sumCny / 14;
  const maxCny = Math.max(...days.map(d => d.cny), avgCny * 1.3, 10);

  const W = 740, H = 220;
  const padLeft = 60, padRight = 15, padTop = 24, padBottom = 28;
  const chartW = W - padLeft - padRight;
  const chartH = H - padTop - padBottom;

  const cGridLine = "rgba(255, 255, 255, 0.05)";
  const cLabel = cssVar("--chart-label") || "rgba(255, 255, 255, 0.45)";
  const cAvg = "#f59e0b"; // 均线琥珀金

  const stepW = chartW / days.length;
  const barW = Math.max(16, Math.min(28, stepW * 0.58));
  const yOf = v => padTop + chartH - (v / maxCny) * chartH;

  let grid = "";
  // 3 条极淡实线，精简虚线干扰
  [0, 0.5, 1].forEach(pct => {
    const v = maxCny * pct;
    const y = yOf(v);
    grid += `
      <line x1="${padLeft}" y1="${y}" x2="${W - padRight}" y2="${y}" stroke="${cGridLine}" stroke-width="1" />
      <text x="${padLeft - 8}" y="${y + 4}" text-anchor="end" font-size="10" fill="${cLabel}" font-family="var(--mono)">¥${v.toFixed(1)}</text>
    `;
  });

  let bars = "";
  days.forEach((d, i) => {
    const x = padLeft + i * stepW + (stepW - barW) / 2;
    const barH = (d.cny / maxCny) * chartH;
    const y = padTop + chartH - barH;
    const tip = `${d.day}\n今日白嫖节省: ¥${d.cny.toFixed(2)} (折合 $${d.usd.toFixed(2)})\n当日 Token: ${fmtTokens(d.tokens)}`;
    bars += `
      <g class="chart-bar-group">
        <title>${esc(tip)}</title>
        <rect x="${x}" y="${y}" width="${barW}" height="${Math.max(3, barH)}" fill="url(#gradSavingsBar)" rx="4" />
        <text x="${x + barW / 2}" y="${H - 10}" text-anchor="middle" font-size="10" fill="${cLabel}" font-family="var(--mono)">${d.label}</text>
      </g>
    `;
  });

  // 14 天均线
  const avgY = yOf(avgCny);
  const avgLine = `
    <g>
      <title>14 天日均节省: ¥${avgCny.toFixed(2)}</title>
      <line x1="${padLeft}" y1="${avgY}" x2="${W - padRight}" y2="${avgY}" stroke="${cAvg}" stroke-dasharray="4,4" stroke-width="1.2" opacity="0.85" />
      <text x="${W - padRight - 4}" y="${avgY - 6}" text-anchor="end" font-size="10" fill="${cAvg}" font-weight="600">14天日均: ¥${avgCny.toFixed(2)}</text>
    </g>
  `;

  wrap.innerHTML = `
    <div style="display:flex;justify-content:space-between;padding:0 8px 6px;font-size:11px;color:var(--muted);">
      <span>近 14 天累计节省: <b style="color:var(--ok);font-size:12px;">¥${sumCny.toFixed(2)}</b> (约 $${(sumCny / USD_CNY).toFixed(2)})</span>
      <span>14 天日均: <b style="color:${cAvg};font-size:12px;">¥${avgCny.toFixed(2)} / 天</b></span>
    </div>
    <svg viewBox="0 0 ${W} ${H}" style="width:100%; height:100%; display:block;" preserveAspectRatio="xMidYMid meet">
      <defs>
        <linearGradient id="gradSavingsBar" x1="0" y1="0" x2="0" y2="1">
          <stop offset="0%" stop-color="#38bdf8" stop-opacity="0.95" />
          <stop offset="100%" stop-color="#10b981" stop-opacity="0.75" />
        </linearGradient>
      </defs>
      ${grid}
      ${bars}
      ${avgLine}
    </svg>
  `;
}

// 5.6 资产续航估算（Token 族算 token 续航天数；积分族单列净变动，不与 token 混算）
function renderRunway() {
  const el = $("assetRunway");
  if (!el) return;

  const accounts = _lastAccounts || [];
  const now = new Date();
  const key = i => localYMD(new Date(now.getTime() - i * 86400 * 1000));

  // Token 族（zcode/atria，余额即 token）：余额 ÷ 近 7 日 Token 日均消耗 → 预计天数
  const tokenAcc = accounts.filter(a => isTokenUnit(a.kind));
  const tokenBalance = tokenAcc.reduce((s, a) => s + Math.max(0, Number(a.remain || 0)), 0);
  const dayTok = {};
  tokenAcc.forEach(a => (a.tokens || []).forEach(t => {
    if (!t.day) return;
    dayTok[t.day] = (dayTok[t.day] || 0) + Number(t.total || 0) + Number(t.cache_read || 0);
  }));
  let tok7 = 0;
  for (let i = 0; i < 7; i++) tok7 += dayTok[key(i)] || 0;
  const avg = tok7 / 7;

  // 积分族（workbuddy/traework/qoder/codebuddy/autoclaw）：积分余额与净变动，无法折算 token，不参与天数换算
  const creditAcc = accounts.filter(a => !isTokenUnit(a.kind));
  const creditBalance = creditAcc.reduce((s, a) => s + Math.max(0, Number(a.remain || 0)), 0);
  const cDelta = {};
  creditAcc.forEach(a => (a.daily || []).forEach(d => {
    if (!d.day) return;
    cDelta[d.day] = (cDelta[d.day] || 0) + Number(d.delta || 0);
  }));
  let credit7 = 0;
  for (let i = 0; i < 7; i++) credit7 += cDelta[key(i)] || 0;

  const ic = `<span class="runway-ic">${I.clock}</span>`;
  const lines = [];
  if (tokenBalance > 0 || avg > 0) {
    if (avg <= 0.5) {
      lines.push(`<div class="asset-runway">${ic}<b>Token 族</b>：现有 <b>${fmtTokens(tokenBalance)}</b> token，近 7 日消耗极低，可长期续航</div>`);
    } else {
      const days = Math.floor(tokenBalance / avg);
      lines.push(`<div class="asset-runway">${ic}<b>Token 族</b>：现有 <b>${fmtTokens(tokenBalance)}</b> token，近 7 日日均耗 <b>${fmtTokens(Math.round(avg))}</b>，预计续航约 <b>${days}</b> 天</div>`);
    }
  }
  if (creditBalance > 0 || credit7 !== 0) {
    const sign = credit7 >= 0 ? "净消耗" : "净入账";
    lines.push(`<div class="asset-runway">${ic}<b>积分族</b>：现有 <b>${fmtTotal(creditBalance)}</b> 积分，近 7 日${sign} <b>${fmtTotal(Math.abs(Math.round(credit7)))}</b>（含签到，无法折算 token）</div>`);
  }
  el.innerHTML = lines.length ? lines.join("") : `<div class="asset-runway">${ic}暂无足够的余额 / 消耗数据用于续航估算</div>`;
}

// 6. 账号 Token 生产力榜单（卡片流可视化：排名徽章 + 占比条形 + 关键数字右对齐）
function renderAcctProductivity(accounts) {
  const box = $("acctProductivityList");
  if (!box) return;
  if (!accounts || accounts.length === 0) {
    box.innerHTML = `<div class="empty-hint">暂无账号生产力数据</div>`;
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
  const grand = list.reduce((s, a) => s + a.sumTotal, 0) || 1;
  const max = list[0].sumTotal || 1;
  const topUid = list[0].uid;
  const medals = ["🥇", "🥈", "🥉"];

  box.innerHTML = list.map((a, idx) => {
    const gm = groupMeta(a.kind);
    const exp = formatExpire(a.expire_at);
    const stateAcct = (state && state.accounts ? state.accounts : []).find(s => s.uid === a.uid && s.group === a.kind);
    const isNext = stateAcct && stateAcct.is_next;
    const bridgeDown = stateAcct && stateAcct.bridge_down;
    const sharePct = Math.round((a.sumTotal / grand) * 100);
    const barPct = Math.max(2, Math.round((a.sumTotal / max) * 100));
    const rankBadge = idx < 3 ? `<span class="prod-rank">${medals[idx]}</span>` : `<span class="prod-rank plain">${idx + 1}</span>`;
    const statusTag = bridgeDown
      ? `<span class="tag danger" title="该账号本地桥未启动，请求打到它会失败">${I.alert} 桥未启动</span>`
      : isNext
        ? `<span class="tag next-pick" title="下一个请求会优先调度它">${I.target} 优先 #1</span>`
        : (stateAcct && stateAcct.disabled ? `<span class="tag danger">已停用</span>` : "");
    const hitChip = a.hitRate !== null
      ? `<span class="prod-chip" title="缓存命中率 ${a.hitRate}%"><span style="color:var(--chart-cache)">◈</span> ${a.hitRate}%</span>`
      : "";

    return `
      <div class="prod-item ${a.uid === topUid ? "is-top" : ""}" title="${esc(a.uid)}">
        ${rankBadge}
        <div class="prod-main">
          <div class="prod-head">
            <span class="badge ${gm.cls}">${gm.name}</span>
            <b class="prod-name">${esc(a.nickname || shortUid(a.uid))}</b>
            <code class="prod-uid">${esc(shortUid(a.uid))}</code>
            ${statusTag}
            <span class="tag ${exp.cls}" title="最早到期时间">${I.clock} ${esc(exp.text)}</span>
            <span class="prod-share" title="占全池累计产出的比例">占池 ${sharePct}%</span>
          </div>
          <div class="prod-bar-bg">
            <div class="prod-bar-fill" style="width:${barPct}%"></div>
          </div>
        </div>
        <div class="prod-nums">
          <span class="prod-num-main" title="累计产出">${fmtTokens(a.sumTotal)}</span>
          <span class="prod-num-sub">今日 ${fmtTokens(a.today || 0)} ${hitChip}</span>
        </div>
      </div>
    `;
  }).join("");
}

// ============================================================
// 成长中心可视化面板
// ============================================================

// travel/lottery/tasks 字段值形如 "ok: ..." | "skip: ..." | "error: ..." | "无凭证"，
// 按前缀映射状态标签（与后端 GrowthResult 约定一致）。
function growthStatusTag(v) {
  if (!v) return `<span class="tag neutral">未执行</span>`;
  const s = String(v);
  if (s.startsWith("ok")) return `<span class="tag ok">成功</span>`;
  if (s.startsWith("skip") || s.includes("无可") || s.includes("已在路上") || s.includes("已领取") || s.includes("已签")) return `<span class="tag neutral">跳过</span>`;
  if (s.startsWith("error") || s.startsWith("fail")) return `<span class="tag bad">失败</span>`;
  return `<span class="tag neutral" title="${esc(s)}">${esc(s.length > 6 ? s.slice(0, 6) + "…" : s)}</span>`;
}

function growthBadgeCls(channel) {
  const gm = groupMeta(channel || "workbuddy");
  return gm.cls;
}

function fmtGrowthTime(ts) {
  if (!ts) return "-";
  return new Date(ts * 1000).toLocaleString("zh-CN", {
    year: "numeric", month: "2-digit", day: "2-digit",
    hour: "2-digit", minute: "2-digit",
  });
}

function renderGrowthTimes() {
  const box = $("growthTimesBox");
  if (!box) return;
  const times = (state && state.growth_times) || [];
  box.innerHTML = times.map((t) =>
    `<span class="time-chip" title="点击删除" onclick="delGrowthTime('${t}')">${t}${I.x}</span>`).join("");
  if (!times.length) box.innerHTML = `<span class="tag neutral">暂无配置</span>`;
  $("nextGrowth").textContent = (state && state.next_growth) || "-";
}

// 仿签到 addTime：加入当前时刻，保存后由 loadState 回显。
async function addGrowthTime() {
  const times = ((state && state.growth_times) || []).slice();
  const now = new Date();
  const next = `${String(now.getHours()).padStart(2, "0")}:${String(now.getMinutes()).padStart(2, "0")}`;
  if (times.includes(next)) { toast("该时间已存在"); return; }
  times.push(next);
  await saveGrowthTimes(times.sort());
}

async function delGrowthTime(t) {
  await saveGrowthTimes(((state && state.growth_times) || []).filter((x) => x !== t));
}

// 保存后手动刷新今日状态等面板数据（loadState 只重绘账号/时间）。
async function saveGrowthTimes(times) {
  $("btnSaveGrowthTimes").disabled = true;
  try {
    await api("/api/config/growth_times", { times });
    toast("成长中心时间已更新");
    await loadState();
  } catch (e) {
    toast(e.message);
  } finally {
    $("btnSaveGrowthTimes").disabled = false;
  }
}

function runGrowthNow() {
  confirmDialog("确定立即对所有 WorkBuddy 账号执行成长中心（旅行/盲盒/任务领奖）？", async () => {
    $("btnRunGrowthNow").disabled = true;
    $("btnRunGrowthNow").textContent = "执行中…";
    try {
      await api("/api/growth/run_now", {});
      toast("批量执行已启动，稍后刷新查看结果");
      // 轮询两次：执行需数秒，先延迟后刷
      setTimeout(() => { loadGrowthToday(); loadGrowthRecords(); }, 5000);
      setTimeout(() => { loadGrowthToday(); loadGrowthRecords(); }, 12000);
    } catch (e) {
      toast(e.message);
    } finally {
      $("btnRunGrowthNow").disabled = false;
      $("btnRunGrowthNow").textContent = "批量运行";
    }
  });
}

async function loadGrowthToday() {
  const wrap = $("growthTodaySummary");
  if (!wrap) return;
  try {
    renderGrowthTodaySummary(await api("/api/growth/summary"));
  } catch (e) {
    wrap.innerHTML = `<div class="empty-hint">加载失败：${esc(e.message)}</div>`;
  }
}

// 后端 GetSummary 返回 total_credits/coupon_count/task_count/lottery_count/travel_count/record_count。
function renderGrowthTodaySummary(summary) {
  const wrap = $("growthTodaySummary");
  if (!wrap) return;
  if (!summary.record_count) {
    wrap.innerHTML = `<div class="empty-hint">今日暂无成长中心执行记录</div>`;
    return;
  }
  const stats = [
    { label: "执行账号次数", val: summary.record_count, icon: I.activity, cls: "ok" },
    { label: "获得积分", val: fmtTokens(summary.total_credits || 0), sub: "今日累计", icon: I.coins, cls: "ok" },
    { label: "优惠券", val: summary.coupon_count || 0, sub: "张", icon: I.gift, cls: "ok" },
    { label: "旅行", val: summary.travel_count || 0, sub: "次推进", icon: I.globe, cls: "skip" },
    { label: "盲盒/抽奖", val: summary.lottery_count || 0, sub: "次", icon: I.shuffle, cls: "skip" },
    { label: "任务领奖", val: summary.task_count || 0, sub: "项", icon: I.zap, cls: "skip" },
  ];
  // 容器 #growthTodaySummary 本身即 .growth-summary 网格，直接填充卡片即可
  wrap.innerHTML = stats.map((s) => `
    <div class="growth-stat-card">
      <div class="gsc-icon ${s.cls}">${s.icon}</div>
      <div class="gsc-label">${s.label}</div>
      <div class="gsc-val">${s.val}</div>
      ${s.sub ? `<div class="gsc-sub">${s.sub}</div>` : ""}
    </div>`).join("");
}

async function loadGrowthRecords() {
  const limit = parseInt(($("selGrowthLimit") && $("selGrowthLimit").value) || "20", 10);
  const tbody = $("growthRecordsBody");
  if (!tbody) return;
  tbody.innerHTML = `<tr><td colspan="8" style="text-align:center; color:var(--muted); padding:20px;">加载中…</td></tr>`;
  try {
    const res = await api(`/api/growth/records?limit=${limit}`);
    renderGrowthRecords(res.records || []);
  } catch (e) {
    tbody.innerHTML = `<tr><td colspan="8" style="text-align:center; color:var(--danger); padding:20px;">加载失败：${esc(e.message)}</td></tr>`;
  }
}

function renderGrowthRecords(records) {
  const tbody = $("growthRecordsBody");
  if (!tbody) return;
  if (!records.length) {
    tbody.innerHTML = `<tr><td colspan="8" style="text-align:center; color:var(--muted); padding:20px;">暂无记录，执行一次成长中心后这里会显示结果</td></tr>`;
    return;
  }
  tbody.innerHTML = records.map((r) => {
    const hasDetail = r.task_details && r.task_details.length;
    const detailBtn = hasDetail
      ? `<button class="btn small" onclick='showGrowthDetail(${esc(JSON.stringify(r.task_details))})'>详情</button>`
      : `<span class="tag neutral">—</span>`;
    return `
      <tr>
        <td>${fmtGrowthTime(r.timestamp)}</td>
        <td>${esc(r.nickname || shortUid(r.uid))}</td>
        <td><span class="badge ${growthBadgeCls(r.channel)}">${groupMeta(r.channel || "workbuddy").name}</span></td>
        <td>${growthStatusTag(r.travel)}</td>
        <td>${growthStatusTag(r.lottery)}</td>
        <td>${growthStatusTag(r.tasks)}</td>
        <td><b style="color:var(--primary)">${fmtTokens(r.total_credits || 0)}</b></td>
        <td>${detailBtn}</td>
      </tr>`;
  }).join("");
}

// 任务奖励明细弹层（复用 confirmDialog 的 overlay 模式）。
function showGrowthDetail(details) {
  $("growthDetailList").innerHTML = details.map((d) => `
    <div class="grd-item">
      <span class="grd-name">${esc(d.name || "任务")}</span>
      <span class="grd-reward">
        ${d.credits > 0 ? `<b class="grd-credits">+${fmtTokens(d.credits)} 积分</b>` : ""}
        ${d.coupon_name ? `<span class="grd-coupon">${esc(d.coupon_name)}</span>` : ""}
        ${!d.credits && !d.coupon_name ? `<span class="tag neutral">无奖励</span>` : ""}
      </span>
    </div>`).join("");
  $("growthDetailOverlay").classList.remove("hidden");
}

function closeGrowthDetail() {
  $("growthDetailOverlay").classList.add("hidden");
}

function initGrowthTab() {
  renderGrowthTimes();
  loadGrowthCheckin();
  loadClaimStatus();
  loadGrowthAccounts();
  loadGrowthToday();
  loadGrowthRecords();
}

// ---------- 成长中心 · 参与账号 ----------
// 后端 GrowthAccounts 返回 WorkBuddy 全部账号 + last（最近一次执行记录，可能为 null）。
async function loadGrowthAccounts() {
  const wrap = $("growthAccountsBox");
  if (!wrap) return;
  try {
    const res = await api("/api/growth/accounts");
    renderGrowthAccounts(res.accounts || []);
  } catch (e) {
    wrap.innerHTML = `<div class="empty-hint">加载失败：${esc(e.message)}</div>`;
  }
}

function renderGrowthAccounts(accounts) {
  const wrap = $("growthAccountsBox");
  if (!wrap) return;
  if (!accounts.length) {
    wrap.innerHTML = `<div class="empty-hint">没有参与成长中心的账号（仅 WorkBuddy 渠道账号参与），添加账号后自动出现在这里</div>`;
    return;
  }
  wrap.innerHTML = accounts.map((a) => {
    const last = a.last;
    let status;
    if (a.disabled) {
      status = `<span class="tag bad">已停用 · 不参与</span>`;
    } else if (!last) {
      status = `<span class="tag neutral">未执行过 · 等待定时执行</span>`;
    } else {
      status = [
        `<span class="ga-step" title="${esc(last.travel || "")}">${I.globe}${growthStatusTag(last.travel)}</span>`,
        `<span class="ga-step" title="${esc(last.lottery || "")}">${I.shuffle}${growthStatusTag(last.lottery)}</span>`,
        `<span class="ga-step" title="${esc(last.tasks || "")}">${I.zap}${growthStatusTag(last.tasks)}</span>`,
      ].join("");
    }
    const runBtn = a.disabled ? "" :
      `<button class="btn small gac-run" onclick="runGrowthAccount('${esc(a.uid)}')">立即执行</button>`;
    return `
      <div class="growth-acct-card${a.disabled ? " is-disabled" : ""}">
        <div class="gac-head">
          <span class="gac-name" title="${esc(a.uid)}">${esc(a.nickname || shortUid(a.uid))}</span>
          ${a.cooling ? `<span class="tag warn">冷却中</span>` : ""}
        </div>
        <div class="gac-status">${status}</div>
        ${last ? `
        <div class="gac-meta">
          <span class="gac-credits">+${fmtTokens(last.total_credits || 0)} 积分</span>
          <span class="gac-time" title="上次执行时间">${fmtGrowthTime(last.timestamp)}</span>
        </div>` : `<div class="gac-meta"><span class="gac-empty">尚无执行结果</span></div>`}
        ${runBtn}
      </div>`;
  }).join("");
}

// 单账号立即执行：完成后刷新账号卡与记录。
async function runGrowthAccount(uid) {
  const btn = document.querySelector(`.gac-run[onclick*="${uid}"]`);
  if (btn) { btn.disabled = true; btn.textContent = "执行中…"; }
  try {
    const res = await api("/api/account/growth", { uid });
    const n = (res.task_details || []).length;
    toast(`执行完成：任务领奖 ${n} 项${res.total_credits ? `，+${fmtTokens(res.total_credits)} 积分` : ""}`);
    loadGrowthAccounts();
    loadGrowthToday();
    loadGrowthRecords();
  } catch (e) {
    toast(e.message);
    if (btn) { btn.disabled = false; btn.textContent = "立即执行"; }
  }
}

// ============================================================
// 本地桥管理（豆包桥 + zcode 实例 + 应用服务 + 自动化 + 外部托管）
// ============================================================

// 分组元数据（与后端 GroupNames 一致）
const BRIDGE_GROUPS = [
  { key: "bridge", name: "模型桥", desc: "网关依赖的本地模型桥，停了对应渠道就断" },
  { key: "apps", name: "应用服务", desc: "对话台与验证码助手等配套 Web 服务" },
  { key: "auto", name: "自动化与隧道", desc: "公网隧道、计划任务探针（外部定时器）" },
  { key: "external", name: "外部托管应用", desc: "开机自启动的常驻应用，面板可探测/拉起，不支持停止" },
];

function bridgeDot(st) {
  if (st.online) return `<span class="bridge-dot on" title="在线"></span>`;
  if (st.running) return `<span class="bridge-dot mid" title="已拉起，端口未就绪"></span>`;
  return `<span class="bridge-dot off" title="未运行"></span>`;
}

function bridgeCardHTML(st) {
  const stateTag = st.online
    ? `<span class="tag ok">在线</span>`
    : st.running ? `<span class="tag warn" title="进程已拉起，端口/接口尚未就绪">已拉起</span>` : `<span class="tag neutral">未运行</span>`;
  const roTag = st.no_stop ? `<span class="tag neutral" title="外部/自启动托管，面板不提供停止">只读</span>` : "";
  // 自启标记：由 ZeroGate 启动时统一拉起（取代原注册表 Run 里的 .bat，不再弹 cmd 窗口）
  const autoTag = st.autostart
    ? `<span class="tag blue" title="ZeroGate 启动时自动拉起（隐藏窗口，无 cmd 弹窗）">随网关自启</span>` : "";
  const detail = esc(st.detail || st.error || "");
  const lastAct = st.last_action ? `<div class="br-last">最近操作：${esc(st.last_action)} · ${esc(st.last_action_at || "")}</div>` : "";
  // 已在运行（含进程在但接口未就绪）时不再给「启动」：避免与「停止」并列的
  // 矛盾操作，也防止重复拉起第二个进程；要重启就先停止再启动。
  const startBtn = (st.online || st.running) ? "" :
    `<button class="btn small primary" onclick="bridgeStart(this, '${esc(st.id)}')">启动</button>`;
  // 可停止 = 非外部托管，且（本面板拉起在跑，或端口型服务在线——后者即使 daemon
  // 重启丢了句柄，后端也能按监听端口定位 PID 兜底杀掉）。
  const stopBtn = (!st.no_stop && (st.running || (st.online && st.port)))
    ? `<button class="btn small danger" onclick="bridgeStop(this, '${esc(st.id)}')">停止</button>`
    : "";
  return `
    <div class="bridge-card${st.online ? " is-online" : ""}">
      <div class="br-head">
        ${bridgeDot(st)}
        <span class="br-name">${esc(st.name)}</span>
        ${st.port ? `<code class="br-port">:${st.port}</code>` : ""}
        ${stateTag}${autoTag}${roTag}
      </div>
      <div class="br-desc">${esc(st.desc || "")}</div>
      ${detail ? `<div class="br-detail">${detail}</div>` : ""}
      ${lastAct}
      ${(startBtn || stopBtn) ? `<div class="br-ops">${startBtn}${stopBtn}</div>` : ""}
    </div>`;
}

function renderBridges(list) {
  const wrap = $("bridgeList");
  if (!wrap) return;

  const isSaturday = (new Date()).getDay() === 6;
  const satBanner = isSaturday
    ? `
      <div class="bridge-claim-banner active">
        <div class="bcb-icon">🎁</div>
        <div class="bcb-content">
          <div class="bcb-title">今日周六 · ZCode 周末计划免费 3 亿 Token 领取日！</div>
          <div class="bcb-desc">请前往 ZCode 控制台领取本周 3 亿免费 Token，并启动下方「ZCode 桥 (端口 8000)」无缝注入网关。</div>
        </div>
        <div class="bcb-actions">
          <button class="btn small primary" onclick="bridgeStart(null, 'zcode')">一键启动 ZCode 桥</button>
        </div>
      </div>
    `
    : `
      <div class="bridge-claim-banner">
        <div class="bcb-icon">💡</div>
        <div class="bcb-content">
          <div class="bcb-title">ZCode 周末 3 亿 Token 计划提醒</div>
          <div class="bcb-desc">每逢周六 ZCode 开放免费领取 300,000,000 Token 周末 Build 额度。当前非周六，可在周六开放时一键注入。</div>
        </div>
      </div>
    `;

  let html = satBanner;
  for (const g of BRIDGE_GROUPS) {
    const items = list.filter((x) => x.group === g.key);
    if (!items.length) continue;
    const onN = items.filter((x) => x.online).length;
    html += `
    <div class="bridge-group">
      <div class="bg-head">
        <span class="bg-name">${esc(g.name)}</span>
        <span class="bg-count">${onN}/${items.length} 在线</span>
        <span class="bg-desc">${esc(g.desc)}</span>
      </div>
      <div class="bridge-list">${items.map(bridgeCardHTML).join("")}</div>
    </div>`;
  }
  wrap.innerHTML = html || `<div class="empty-hint">没有可托管的服务</div>`;
}

async function loadBridges() {
  const wrap = $("bridgeList");
  if (!wrap) return;
  try {
    const res = await api("/api/bridges");
    renderBridges(res.bridges || []);
  } catch (e) {
    wrap.innerHTML = `<div class="empty-hint">加载失败：${esc(e.message)}</div>`;
  }
}

async function bridgeStart(btn, id) {
  if (typeof btn === "string") { id = btn; btn = null; }
  if (btn) {
    btn.classList.add("is-loading");
    btn.disabled = true;
    btn.textContent = "启动中…";
  }
  try {
    const r = await api("/api/bridges/start", { id });
    if (r.error) toast(`${r.status && r.status.name ? r.status.name : id}：${r.error}`);
    else if (r.status && r.status.online) toast(`${r.status.name} 已在线`);
    else toast(`${r.status && r.status.name ? r.status.name : id} 启动指令已发出，等待就绪`);
  } catch (e) { toast(e.message); }
  await loadBridges();
}

function bridgeStop(btn, id) {
  if (typeof btn === "string") { id = btn; btn = null; }
  confirmDialog("确定停止该服务？停止后依赖它的功能将不可用。", async () => {
    if (btn) {
      btn.classList.add("is-loading");
      btn.disabled = true;
      btn.textContent = "停止中…";
    }
    try {
      const r = await api("/api/bridges/stop", { id });
      toast(r.error || `${r.status && r.status.name ? r.status.name : id} 已停止`);
    } catch (e) { toast(e.message); }
    await loadBridges();
  });
}

async function bridgeStartAll() {
  const btn = $("btnBridgeStartAll");
  btn.disabled = true;
  btn.textContent = "启动中…";
  try {
    await api("/api/bridges/start_all", {});
    toast("一键全启完成，稍后自动刷新状态");
  } catch (e) { toast(e.message); }
  finally {
    btn.disabled = false;
    btn.innerHTML = `${I.zap}一键全启`;
  }
  setTimeout(loadBridges, 1500);
  setTimeout(loadBridges, 6000);
}

// ============================================================
// 成长中心 · 每日签到卡片（签到功能并入成长中心页）
// ============================================================

function growthCheckinCardHTML(a) {
  const gm = groupMeta(a.group);
  const canCheckin = gm.checkin || a.group === "workbuddy" || a.group === "traework" || a.group === "autoclaw";
  let tag;
  if (!canCheckin) tag = `<span class="tag neutral">无签到</span>`;
  else if (isTodayChecked(a)) tag = `<span class="tag ok">今日已签</span>`;
  else if (a.last_checkin_at) tag = `<span class="tag ${a.last_checkin_ok ? "ok" : "bad"}">${a.last_checkin_ok ? "签到成功" : "签到失败"}</span>`;
  else tag = `<span class="tag neutral">未签到</span>`;
  const btn = (canCheckin && !a.disabled)
    ? `<button class="btn small gac-run" onclick="checkin('${esc(a.uid)}')">签到</button>` : "";
  return `
    <div class="growth-acct-card${a.disabled ? " is-disabled" : ""}">
      <div class="gac-head">
        <span class="badge ${gm.cls}">${esc(gm.name)}</span>
        <span class="gac-name" title="${esc(a.uid)}">${esc(a.nickname || shortUid(a.uid))}</span>
      </div>
      <div class="gac-status">${tag}${a.credits ? `<span class="gac-credits">${a.credits} 分</span>` : ""}</div>
      ${btn}
    </div>`;
}

function loadGrowthCheckin() {
  const wrap = $("growthCheckinBox");
  if (!wrap) return;
  const list = (state && state.accounts || []).filter((a) => {
    const g = a.group;
    return g === "workbuddy" || g === "traework" || g === "autoclaw" || g === "qoder" || g === "codebuddy" || g === "zcode";
  });
  wrap.innerHTML = list.length
    ? list.map(growthCheckinCardHTML).join("")
    : `<div class="empty-hint">还没有账号，去「账号矩阵」添加</div>`;
}

// ============================================================
// 成长中心 · ZCode 周末包领取可视化
// ============================================================

function claimRecTag(rec) {
  if (rec.ok) return `<span class="tag ok">已领取</span>`;
  if (rec.failed) return `<span class="tag bad">失败</span>`;
  return `<span class="tag neutral">无可领</span>`;
}

function renderClaimStatus(rep) {
  const box = $("claimStatusBox");
  if (!box) return;
  if (!rep) {
    box.innerHTML = `<div class="empty-hint">还没有领取记录。自动窗口：周五/周六 19:00-19:15；也可点「立即领取」手动触发。</div>`;
    return;
  }
  const head = `
    <div class="claim-summary">
      <div class="cs-item"><span class="cs-label">执行时间</span><b>${esc(rep.at || "-")}</b></div>
      <div class="cs-item"><span class="cs-label">触发方式</span><b>${rep.trigger === "manual" ? "手动" : "自动"}</b></div>
      <div class="cs-item"><span class="cs-label">已领取</span><b style="color:var(--ok)">${rep.claimed || 0}</b></div>
      <div class="cs-item"><span class="cs-label">无可领</span><b>${rep.no_claim || 0}</b></div>
      <div class="cs-item"><span class="cs-label">失败</span><b style="color:${(rep.failed || 0) > 0 ? "var(--danger)" : "inherit"}">${rep.failed || 0}</b></div>
    </div>`;
  const rows = (rep.instances || []).map((rec) => `
    <div class="db-row ${rec.ok ? "ok" : rec.failed ? "expired" : ""}">
      <span class="db-uid">${esc(rec.instance)}</span>
      <span class="db-detail" title="${esc(rec.output || "")}">${esc(rec.output || "-")}</span>
      <span class="db-flag">${claimRecTag(rec)} <span class="muted" style="font-size:11px">${esc(rec.at || "")}</span></span>
    </div>`).join("");
  box.innerHTML = head + `<div class="db-list" style="margin-top:8px;">${rows || `<div class="empty-hint">本轮没有遍历到任何实例</div>`}</div>`;
}

async function loadClaimStatus() {
  const box = $("claimStatusBox");
  if (!box) return;
  try {
    const res = await api("/api/zcode/claim_status");
    renderClaimStatus(res.report);
  } catch (e) {
    box.innerHTML = `<div class="empty-hint">加载失败：${esc(e.message)}</div>`;
  }
}

async function runClaimNow() {
  const btn = $("btnClaimNow");
  btn.disabled = true;
  btn.textContent = "执行中…";
  try {
    await api("/api/zcode/claim_now", {});
    toast("领取已启动（每实例最多 90 秒），稍后自动刷新结果");
    setTimeout(loadClaimStatus, 3000);
    setTimeout(loadClaimStatus, 12000);
    setTimeout(loadClaimStatus, 30000);
  } catch (e) {
    toast(e.message);
  } finally {
    btn.disabled = false;
    btn.innerHTML = `${I.zap}立即领取`;
  }
}

// ============================================================
// Atria 官方 Key 集中管理
// ============================================================

function openAtriaDialog() {
  $("atriaKey").value = "";
  $("atriaName").value = "";
  $("atriaBatch").value = "";
  $("atriaMsg").textContent = "";
  $("atriaOverlay").classList.remove("hidden");
  loadAtriaList();
}
function closeAtriaDialog() { $("atriaOverlay").classList.add("hidden"); }

async function loadAtriaList() {
  const tbody = $("atriaListBody");
  if (!tbody) return;
  try {
    const res = await api("/api/atria/list");
    const list = res.accounts || [];
    if (!list.length) {
      tbody.innerHTML = `<tr><td colspan="5" style="text-align:center; color:var(--muted); padding:14px;">暂无 Atria 账号，粘贴 atr_ Key 添加</td></tr>`;
      return;
    }
    tbody.innerHTML = list.map((a) => `
      <tr>
        <td>${esc(a.nickname || "")}</td>
        <td><code style="font-size:11px;">${esc(shortUid(a.uid))}</code></td>
        <td><code style="font-size:11px; color:var(--muted);">${esc(a.key || "")}</code></td>
        <td>${a.disabled ? `<span class="tag danger">已停用</span>` : `<span class="tag ok">启用</span>`}</td>
        <td>
          <button class="btn small" onclick="toggleDisable('${esc(a.uid)}', ${a.disabled})">${a.disabled ? "启用" : "停用"}</button>
          <button class="btn small danger" onclick="removeAcct('${esc(a.uid)}')">删除</button>
        </td>
      </tr>`).join("");
  } catch (e) {
    tbody.innerHTML = `<tr><td colspan="5" style="text-align:center; color:var(--danger); padding:14px;">${esc(e.message)}</td></tr>`;
  }
}

async function addAtriaKey() {
  const key = $("atriaKey").value.trim();
  if (!key) { toast("请粘贴 Atria API Key"); return; }
  $("btnAtriaAdd").disabled = true;
  try {
    const r = await api("/api/atria/add", { key, nickname: $("atriaName").value.trim() });
    $("atriaMsg").textContent = `已添加：${r.nickname || r.uid}`;
    $("atriaKey").value = "";
    $("atriaName").value = "";
    toast("Atria Key 已添加入池");
    loadAtriaList();
    loadState();
  } catch (e) {
    $("atriaMsg").textContent = "失败：" + e.message;
  } finally {
    $("btnAtriaAdd").disabled = false;
  }
}

async function addAtriaBatch() {
  const text = $("atriaBatch").value.trim();
  if (!text) { toast("请先粘贴 Key 列表"); return; }
  $("btnAtriaBatch").disabled = true;
  try {
    const r = await api("/api/atria/add_batch", { keys: text });
    const errs = (r.results || []).filter((x) => x.status === "error");
    $("atriaMsg").textContent = `新增 ${r.added} 个${errs.length ? `，失败 ${errs.length} 个` : ""}`;
    toast(`批量导入完成：新增 ${r.added} 个`);
    $("atriaBatch").value = "";
    loadAtriaList();
    loadState();
  } catch (e) {
    $("atriaMsg").textContent = "失败：" + e.message;
  } finally {
    $("btnAtriaBatch").disabled = false;
  }
}