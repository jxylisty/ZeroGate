// ============================================================
// ZeroGate WebUI - Application Entry & Controller (app.js)
// 现代化模块化入口：零外部构建依赖，负责状态装配、Tab导航、弹窗控制与视图交互
// ============================================================

"use strict";

// ---------- 全局状态 ----------
let state = null;
window.state = state;

// 只读大屏模式标记（config.json -> listen_dashboard）
let readOnly = false;
let switchTabFn = null;

// ---------- 数据加载与状态同步 ----------
async function loadState() {
  try {
    state = await api("/api/state");
    window.state = state;
  } catch (e) {
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
    window.state = state;
  }
  render();
  if (readOnly) applyReadOnlyMode();
}
window.loadState = loadState;

// 只读大屏模式：仅保留 Token 资产大屏 + 调用流水
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

// 核心视图刷新调度
function render() {
  if (!state) return;

  const accounts = state.accounts || [];
  const activeCount = accounts.filter(a => !a.disabled && !a.cooling).length;

  if ($("activeCount")) $("activeCount").textContent = activeCount;
  if ($("totalCount")) $("totalCount").textContent = accounts.length;

  const host = state.listen_host || "127.0.0.1";
  const port = state.listen_port || 7863;
  const dispHost = (host === "0.0.0.0" || host === "" || host === "::") ? "127.0.0.1" : host;

  if ($("apiAddrVal")) $("apiAddrVal").textContent = `http://${dispHost}:${port}/v1`;
  if ($("apiKeyVal")) $("apiKeyVal").textContent = state.api_key || "（无鉴权）";
  if ($("claudeAddrVal")) $("claudeAddrVal").textContent = `http://${dispHost}:${port}`;

  if (typeof renderAccounts === "function") renderAccounts();
  renderTimes();
  if (typeof renderGrowthTimes === "function") renderGrowthTimes();
  if (typeof renderAddMenu === "function") renderAddMenu();

  if ($("chkAutostart")) $("chkAutostart").checked = !!state.autostart;
}
window.render = render;

// ---------- 定时签到时间渲染 ----------
function renderTimes() {
  const box = $("checkinTimesBox");
  if (!box) return;
  const times = (state && state.checkin_times) || [];
  box.innerHTML = times.map(t =>
    `<span class="time-chip" title="点击删除" onclick="delTime('${t}')">${t}${I.x}</span>`).join("");
  if (!times.length) box.innerHTML = `<span class="tag neutral">暂无配置</span>`;
  if ($("nextCheckin")) $("nextCheckin").textContent = (state && state.next_checkin) || "-";
}
window.renderTimes = renderTimes;

function delTime(t) {
  const times = ((state && state.checkin_times) || []).filter(x => x !== t);
  saveTimes(times);
}
window.delTime = delTime;

function addTime() {
  const times = (((state && state.checkin_times) || []).slice());
  const now = new Date();
  const next = `${String(now.getHours()).padStart(2, "0")}:${String(now.getMinutes()).padStart(2, "0")}`;
  if (!times.includes(next)) times.push(next);
  saveTimes(times.sort());
}
window.addTime = addTime;

async function saveTimes(times) {
  try {
    await api("/api/config/checkin_times", { times });
    toast("签到时间已更新");
    loadState();
  } catch (e) { toast(e.message); }
}
window.saveTimes = saveTimes;

// ---------- 开机自启 ----------
async function toggleAutostart() {
  try {
    await api("/api/config/autostart", { on: $("chkAutostart").checked });
    toast("开机自启设置已保存");
  } catch (e) {
    toast(e.message);
    loadState();
  }
}
window.toggleAutostart = toggleAutostart;

// ---------- 通用确认框与帮助弹层 ----------
function confirmDialog(msg, onOk) {
  if ($("confirmMsg")) $("confirmMsg").textContent = msg;
  if ($("confirmOverlay")) $("confirmOverlay").classList.remove("hidden");
  if ($("btnConfirmOk")) {
    $("btnConfirmOk").onclick = () => {
      $("confirmOverlay").classList.add("hidden");
      if (typeof onOk === "function") onOk();
    };
  }
  if ($("btnConfirmCancel")) {
    $("btnConfirmCancel").onclick = () => $("confirmOverlay").classList.add("hidden");
  }
}
window.confirmDialog = confirmDialog;

function openHelp() { if ($("helpOverlay")) $("helpOverlay").classList.remove("hidden"); }
function closeHelp() { if ($("helpOverlay")) $("helpOverlay").classList.add("hidden"); }
function openAbout() { if ($("aboutOverlay")) $("aboutOverlay").classList.remove("hidden"); }
function closeAbout() { if ($("aboutOverlay")) $("aboutOverlay").classList.add("hidden"); }
window.openHelp = openHelp;
window.closeHelp = closeHelp;
window.openAbout = openAbout;
window.closeAbout = closeAbout;

// ---------- API 监听与 Key 配置弹层 ----------
function openApiConfig() {
  if (!state) return;
  if ($("inPort")) $("inPort").value = state.listen_port || 7863;
  const hostVal = state.listen_host === "127.0.0.1" ? "127.0.0.1"
    : (state.listen_host === "0.0.0.0" || state.listen_host === "" || state.listen_host === "::") ? "0.0.0.0"
    : "__custom__";
  if ($("selHost")) $("selHost").value = hostVal;
  if (hostVal === "__custom__") {
    if ($("inHost")) $("inHost").value = state.listen_host;
    if ($("customHostRow")) $("customHostRow").classList.remove("hidden");
  } else {
    if ($("customHostRow")) $("customHostRow").classList.add("hidden");
  }
  if ($("apiConfigOverlay")) $("apiConfigOverlay").classList.remove("hidden");
}
function closeApiConfig() { if ($("apiConfigOverlay")) $("apiConfigOverlay").classList.add("hidden"); }

async function saveApiConfig() {
  let host = $("selHost") ? $("selHost").value : "127.0.0.1";
  if (host === "__custom__") host = ($("inHost") ? $("inHost").value.trim() : "") || "127.0.0.1";
  const port = parseInt($("inPort") ? $("inPort").value : "7863", 10) || 7863;
  try {
    await api("/api/config/listen", { host, port });
    toast("监听已切换（重启后完全生效）");
    closeApiConfig();
    loadState();
  } catch (e) { toast(e.message); }
}

function openApiKey() {
  if (!state) return;
  if ($("keyInput")) {
    $("keyInput").value = state.api_key || "";
    $("keyInput").focus();
  }
  if ($("apiKeyOverlay")) $("apiKeyOverlay").classList.remove("hidden");
}
function closeApiKey() { if ($("apiKeyOverlay")) $("apiKeyOverlay").classList.add("hidden"); }

async function saveApiKey() {
  const key = $("keyInput") ? $("keyInput").value.trim() : "";
  try {
    await api("/api/config/api_key", { key });
    closeApiKey();
    toast("API-Key 已更新");
    loadState();
  } catch (e) { toast(e.message); }
}

window.openApiConfig = openApiConfig;
window.closeApiConfig = closeApiConfig;
window.saveApiConfig = saveApiConfig;
window.openApiKey = openApiKey;
window.closeApiKey = closeApiKey;
window.saveApiKey = saveApiKey;

// ---------- 登录与鉴权绑定 ----------
let pendingChannel = null;
let pendingProvider = "zai";

function pickZcodeProvider(btn) {
  pendingProvider = btn.dataset.provider;
  document.querySelectorAll("#zcodeProviderRow .btn").forEach((b) => b.classList.remove("primary"));
  btn.classList.add("primary");
}
window.pickZcodeProvider = pickZcodeProvider;

function openWbtDialog(channel) {
  if ($("wbtTitle")) $("wbtTitle").textContent = "添加 " + groupMeta(channel).name + " 账号（粘贴 token）";
  if ($("wbtToken")) $("wbtToken").value = "";
  if ($("wbtRefresh")) $("wbtRefresh").value = "";
  if ($("wbtName")) $("wbtName").value = "";
  if ($("wbtMsg")) $("wbtMsg").textContent = "";
  if ($("wbtokenOverlay")) $("wbtokenOverlay").classList.remove("hidden");
}
function closeWbtDialog() { if ($("wbtokenOverlay")) $("wbtokenOverlay").classList.add("hidden"); }

async function wbtImport() {
  const token = $("wbtToken") ? $("wbtToken").value.trim() : "";
  if (!token) { toast("请粘贴 accessToken"); return; }
  const domain = "codebuddy.ai";
  const btn = $("wbtConfirm");
  if (btn) btn.disabled = true;
  try {
    const r = await api("/api/wbintl/add", {
      domain,
      token,
      refresh_token: $("wbtRefresh") ? $("wbtRefresh").value.trim() : "",
      nickname: $("wbtName") ? $("wbtName").value.trim() : ""
    });
    closeWbtDialog();
    toast("导入成功：" + (r.nickname || r.uid));
    await loadState();
    if (typeof refreshFees === "function") refreshFees();
  } catch (e) {
    if ($("wbtMsg")) $("wbtMsg").textContent = "导入失败：" + e.message;
  } finally {
    if (btn) btn.disabled = false;
  }
}
window.openWbtDialog = openWbtDialog;
window.closeWbtDialog = closeWbtDialog;
window.wbtImport = wbtImport;

async function openAutoclawDialog() {
  pendingChannel = null;
  if ($("aclPhone")) $("aclPhone").value = "";
  if ($("aclCode")) $("aclCode").value = "";
  if ($("aclMsg")) $("aclMsg").textContent = "";
  if ($("autoclawOverlay")) $("autoclawOverlay").classList.remove("hidden");
  if ($("aclPhone")) $("aclPhone").focus();
}
function closeAutoclawDialog() { if ($("autoclawOverlay")) $("autoclawOverlay").classList.add("hidden"); }

async function aclSendCode() {
  const phone = $("aclPhone") ? $("aclPhone").value.trim() : "";
  if (!phone) { toast("请先输入手机号"); return; }
  const btn = $("aclSend");
  if (btn) btn.disabled = true;
  try {
    await api("/api/autoclaw/send_code", { phone });
    if ($("aclMsg")) $("aclMsg").textContent = "验证码已发送至 " + phone + "，请查收短信";
    if ($("aclCode")) $("aclCode").focus();
  } catch (e) {
    if ($("aclMsg")) $("aclMsg").textContent = "发送失败：" + e.message;
  } finally {
    if (btn) btn.disabled = false;
  }
}

async function aclConfirm() {
  const phone = $("aclPhone") ? $("aclPhone").value.trim() : "";
  const code = $("aclCode") ? $("aclCode").value.trim() : "";
  if (!phone || !code) { toast("请输入手机号和验证码"); return; }
  const btn = $("aclConfirm");
  if (btn) btn.disabled = true;
  try {
    const r = await api("/api/autoclaw/login", { phone, code });
    closeAutoclawDialog();
    toast("AutoClaw 登录成功：" + (r.nickname || r.uid));
    await loadState();
    if (typeof refreshFees === "function") refreshFees();
  } catch (e) {
    if ($("aclMsg")) $("aclMsg").textContent = "登录失败：" + e.message;
  } finally {
    if (btn) btn.disabled = false;
  }
}
window.openAutoclawDialog = openAutoclawDialog;
window.closeAutoclawDialog = closeAutoclawDialog;
window.aclSendCode = aclSendCode;
window.aclConfirm = aclConfirm;

function promptLogin(channel) {
  pendingChannel = channel;
  const name = groupMeta(channel).name;
  if (channel === "autoclaw") { openAutoclawDialog(); return; }
  if (channel === "atria") { if (typeof openAtriaDialog === "function") openAtriaDialog(); return; }
  const row = $("zcodeProviderRow");
  if (channel === "zcode") {
    pendingProvider = "zai";
    if (row) {
      row.classList.remove("hidden");
      row.querySelectorAll(".btn").forEach((b) => b.classList.toggle("primary", b.dataset.provider === "zai"));
    }
  } else if (row) {
    row.classList.add("hidden");
  }
  if ($("lcTitle")) $("lcTitle").textContent = "添加 " + name + " 账号";
  if ($("lcMsg")) {
    if (channel === "codebuddy") {
      $("lcMsg").textContent = `点击「登录${name}」将打开浏览器窗口，请按指示登录 ${name} 账号（国际版），完成后关闭浏览器窗口即可。`;
    } else {
      $("lcMsg").textContent = `点击「登录${name}」将打开浏览器窗口，请按照指示正常登录${name}账号，登录成功后关闭浏览器窗口即可。`;
    }
  }
  if ($("btnLoginConfirm")) $("btnLoginConfirm").textContent = "登录" + name;
  if ($("loginConfirmOverlay")) $("loginConfirmOverlay").classList.remove("hidden");
}
window.promptLogin = promptLogin;

function confirmLogin() {
  if ($("loginConfirmOverlay")) $("loginConfirmOverlay").classList.add("hidden");
  if (pendingChannel) startLogin(pendingChannel);
}
window.confirmLogin = confirmLogin;

async function startLogin(channel) {
  try {
    const r = await api("/api/login/start", { channel, provider: channel === "zcode" ? pendingProvider : undefined });
    const url = r.auth_url;
    if (!url) { toast("无法获取登录链接"); return; }
    if ($("loginTitle")) $("loginTitle").textContent = "添加 " + groupMeta(channel).name + " 账号";
    if ($("loginOverlay")) $("loginOverlay").classList.remove("hidden");
    if ($("btnCopyUrl")) $("btnCopyUrl").dataset.url = url;
    if (channel === "zcode") {
      if ($("loginMsg")) $("loginMsg").textContent = "浏览器已自动打开授权页，完成授权后返回即可（若未打开请点「复制链接」手动打开）";
    } else {
      if ($("loginMsg")) $("loginMsg").textContent = "请在浏览器新窗口中完成登录…";
      window.open(url, "_blank", "noopener,noreferrer");
    }
    startLoginPoll();
  } catch (e) {
    toast(e.message);
  }
}
window.startLogin = startLogin;

async function cancelLogin() {
  try {
    await api("/api/login/cancel", {});
    stopLoginPoll();
    if ($("loginOverlay")) $("loginOverlay").classList.add("hidden");
    toast("登录已取消");
  } catch (e) { toast(e.message); }
}
window.cancelLogin = cancelLogin;

function copyUrl() {
  const url = $("btnCopyUrl") ? $("btnCopyUrl").dataset.url : "";
  if (!url) { toast("暂无链接"); return; }
  navigator.clipboard.writeText(url).then(() => toast("链接已复制")).catch(() => toast("复制失败，请手动复制"));
}
window.copyUrl = copyUrl;

let loginPoll = null;
function startLoginPoll() {
  stopLoginPoll();
  loginPoll = setInterval(async () => {
    try {
      const st = await api("/api/state");
      if (!st.login_busy) {
        stopLoginPoll();
        if ($("loginOverlay")) $("loginOverlay").classList.add("hidden");
        toast("登录完成，正在同步账号…");
        await loadState();
        if (typeof refreshFees === "function") refreshFees();
      }
    } catch (e) { /* 忽略轮询网络抖动 */ }
  }, 3000);
}
function stopLoginPoll() {
  if (loginPoll) { clearInterval(loginPoll); loginPoll = null; }
}

// ---------- 批量签到弹窗交互 ----------
const ciSel = new Set();
let ciRun = false;

function parseMD(s) {
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
window.isTodayChecked = isTodayChecked;

function ciExpired(a) {
  if (a.disabled) return true;
  const t = parseMD(a.until);
  return !!t && t.getTime() <= Date.now();
}

function openCheckinDialog() {
  const accounts = (state && state.accounts) || [];
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
  if ($("ciList")) $("ciList").innerHTML = html;
  if ($("ciResults")) $("ciResults").innerHTML = "";
  if ($("ciProgress")) $("ciProgress").classList.add("hidden");
  if ($("btnCiStart")) $("btnCiStart").disabled = false;
  ciRun = false;
  updateCiCount();
  if ($("checkinOverlay")) $("checkinOverlay").classList.remove("hidden");
}
window.openCheckinDialog = openCheckinDialog;

function closeCheckinDialog() {
  if ($("checkinOverlay")) $("checkinOverlay").classList.add("hidden");
  ciRun = false;
}
window.closeCheckinDialog = closeCheckinDialog;

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
window.ciRowChg = ciRowChg;

function ciSyncChecks() {
  document.querySelectorAll("#ciList input[type=checkbox]").forEach((c) => {
    if (!c.disabled) c.checked = ciSel.has(c.dataset.uid);
  });
  updateCiCount();
}
function updateCiCount() {
  const n = document.querySelectorAll("#ciList input[type=checkbox]").length;
  if ($("ciCount")) $("ciCount").textContent = `已选 ${ciSel.size} / ${n}`;
}
function ciSetAll(on) {
  document.querySelectorAll("#ciList input[type=checkbox]").forEach((c) => {
    if (c.disabled) return;
    c.checked = on;
    if (on) ciSel.add(c.dataset.uid); else ciSel.delete(c.dataset.uid);
  });
  updateCiCount();
}
window.ciSetAll = ciSetAll;

function ciInvertSel() {
  document.querySelectorAll("#ciList input[type=checkbox]").forEach((c) => {
    if (c.disabled) return;
    c.checked = !c.checked;
    if (c.checked) ciSel.add(c.dataset.uid); else ciSel.delete(c.dataset.uid);
  });
  updateCiCount();
}
window.ciInvertSel = ciInvertSel;

function ciOnlyUnchecked() {
  for (const a of (state && state.accounts) || []) if (isTodayChecked(a)) ciSel.delete(a.uid);
  ciSyncChecks();
}
window.ciOnlyUnchecked = ciOnlyUnchecked;

function ciSkipExpired() {
  for (const a of (state && state.accounts) || []) if (ciExpired(a)) ciSel.delete(a.uid);
  ciSyncChecks();
}
window.ciSkipExpired = ciSkipExpired;

async function runBatchCheckin() {
  if (ciRun) return;
  const uids = [...ciSel];
  if (!uids.length) { toast("请先选择要签到的账号"); return; }
  ciRun = true;
  if ($("btnCiStart")) $("btnCiStart").disabled = true;
  if ($("ciProgress")) $("ciProgress").classList.remove("hidden");
  const box = $("ciResults");
  if (box) box.innerHTML = "";
  const total = uids.length;
  let ok = 0, already = 0, fail = 0;
  const nameOf = (uid) => {
    const a = (state && state.accounts ? state.accounts : []).find((x) => x.uid === uid);
    return esc((a && (a.nickname || shortUid(uid))) || shortUid(uid));
  };
  for (let i = 0; i < total; i++) {
    const uid = uids[i];
    if ($("ciStatus")) $("ciStatus").textContent = `正在签到 ${i + 1}/${total} …`;
    if ($("ciBar")) $("ciBar").style.width = `${(i / total) * 100}%`;
    let res;
    try { res = await api("/api/account/checkin", { uid }); }
    catch (e) { res = { uid, ok: false, msg: e.message }; }
    const kind = res.ok ? "ok" : ((res.msg || "").includes("已签") ? "already" : "fail");
    if (kind === "ok") ok++; else if (kind === "already") already++; else fail++;
    if (box) {
      box.insertAdjacentHTML("beforeend",
        `<div class="ci-result ${kind}"><span class="ci-rname">${nameOf(uid)}</span><span class="ci-rmsg">${esc(res.msg || "")}${res.ok && res.has_remain !== false ? (res.remain !== undefined ? `（剩余 ${res.remain}）` : "") : ""}</span></div>`);
      box.scrollTop = box.scrollHeight;
    }
    if ($("ciBar")) $("ciBar").style.width = `${((i + 1) / total) * 100}%`;
  }
  if ($("ciStatus")) $("ciStatus").textContent = `完成：成功 ${ok} · 今日已签 ${already} · 失败 ${fail}`;
  ciRun = false;
  if ($("btnCiStart")) $("btnCiStart").disabled = false;
  loadState();
}
window.runBatchCheckin = runBatchCheckin;

// ---------- 豆包保活续期 ----------
let dbBusy = false;
function renderDoubao(res) {
  const list = res.accounts || [];
  const sum = [];
  if (res.finished_at) sum.push(`执行时间 ${esc(res.finished_at)}`);
  if (list.length) sum.push(`有效 ${res.ok} · 失效 ${res.expired} · 异常 ${res.error}`);
  if ($("dbSummary")) $("dbSummary").textContent = sum.join("　");
  if (!list.length) {
    if ($("dbList")) $("dbList").innerHTML = `<div class="db-row error"><span class="db-uid">—</span><span class="db-detail">${esc(res.note || "暂无记录")}</span></div>`;
    return;
  }
  if ($("dbList")) {
    $("dbList").innerHTML = list.map((a) => {
      const flag = a.renewed ? `<span class="db-flag" style="color:var(--ok)">已续期</span>`
        : a.status === "ok" ? `<span class="db-flag" style="color:var(--ok)">有效</span>`
        : a.status === "expired" ? `<span class="db-flag" style="color:var(--danger)">已失效</span>`
        : `<span class="db-flag" style="color:var(--warn)">异常</span>`;
      const expire = a.sid_expire_at ? ` · 到期 ${esc(a.sid_expire_at)}` : "";
      return `<div class="db-row ${a.status}"><span class="db-uid">${esc(shortUid(a.user_id))}</span><span class="db-detail">${esc(a.detail)}${expire}</span>${flag}</div>`;
    }).join("");
  }
}

async function openDoubaoDialog() {
  if ($("doubaoOverlay")) $("doubaoOverlay").classList.remove("hidden");
  if ($("dbSummary")) $("dbSummary").textContent = "";
  if ($("dbList")) $("dbList").innerHTML = `<div class="db-row error"><span class="db-detail">读取上次结果…</span></div>`;
  try {
    renderDoubao(await api("/api/doubao/renew_result"));
  } catch (e) {
    if ($("dbList")) $("dbList").innerHTML = `<div class="db-row error"><span class="db-detail">${esc(e.message)}</span></div>`;
  }
}
window.openDoubaoDialog = openDoubaoDialog;

async function runDoubaoRenew() {
  if (dbBusy) return;
  dbBusy = true;
  const btn = $("btnDbRenew");
  if (btn) { btn.disabled = true; btn.textContent = "保活中…"; }
  try {
    renderDoubao(await api("/api/doubao/renew", {}));
  } catch (e) {
    if ($("dbSummary")) $("dbSummary").textContent = "";
    if ($("dbList")) $("dbList").innerHTML = `<div class="db-row error"><span class="db-detail">${esc(e.message)}</span></div>`;
  } finally {
    dbBusy = false;
    if (btn) { btn.disabled = false; btn.textContent = "立即保活"; }
  }
}
window.runDoubaoRenew = runDoubaoRenew;

function closeDoubaoDialog() { if ($("doubaoOverlay")) $("doubaoOverlay").classList.add("hidden"); }
window.closeDoubaoDialog = closeDoubaoDialog;

async function refreshAll() {
  const btn = $("btnRefreshAll");
  if (!btn || btn.disabled) return;
  btn.disabled = true;
  btn.classList.add("is-loading");
  const oldHTML = btn.innerHTML;
  btn.innerHTML = `${I.refresh} <span>刷新中…</span>`;
  try {
    const r = await api("/api/account/refresh_all", {});
    toast(r.busy ? "已有刷新任务进行中" : `积分刷新完成：成功 ${r.ok} / 失败 ${r.failed}`);
    loadState();
  } catch (e) {
    toast(e.message);
  } finally {
    btn.disabled = false;
    btn.classList.remove("is-loading");
    btn.innerHTML = oldHTML;
  }
}
window.refreshAll = refreshAll;

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
        return `<span class="badge ${meta.cls}">${meta.name}: ${channels[k]} 个账号</span>`;
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
window.loadDashboard = loadDashboard;

// ---------- Tab 导航控制系统 ----------
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

    if (window.innerWidth <= 768) {
      window.scrollTo({ top: 0, behavior: "smooth" });
    }
    const activeDock = document.querySelector(`.dock-item-btn[data-tab="${name}"]`);
    if (activeDock && typeof activeDock.scrollIntoView === "function") {
      activeDock.scrollIntoView({ behavior: "smooth", inline: "center", block: "nearest" });
    }

    if (name === "dashboard") loadDashboard();
    else if (name === "usage") {
      if (typeof loadTokenAnalytics === "function") loadTokenAnalytics();
      if (typeof loadAssets === "function") loadAssets();
    }
    else if (name === "logs") {
      if (typeof loadAuditLogs === "function") loadAuditLogs();
    }
    else if (name === "clients") {
      if (typeof loadClients === "function") loadClients();
    }
    else if (name === "router") {
      if (typeof loadRedirects === "function") loadRedirects();
    }
    else if (name === "growth") {
      if (typeof initGrowthTab === "function") initGrowthTab();
    }
    else if (name === "bridges") {
      if (typeof loadBridges === "function") loadBridges();
    }
    else if (name === "models") {
      if (typeof loadFees === "function") loadFees();
    }
    else if (name === "settings") loadSettingsView();
  }

  tabs.forEach(t => t.addEventListener("click", () => switchTab(t.dataset.tab)));
  dockBtns.forEach(b => b.addEventListener("click", () => switchTab(b.dataset.tab)));
  sidebarBtns.forEach(b => b.addEventListener("click", () => switchTab(b.dataset.tab)));

  switchTabFn = switchTab;
  window.switchTabFn = switchTab;
  switchTab(savedTab);

  initWallpaperSystem();
  initProtocolPills();
  initSidebar();
  initMobileDrawer();
  initLayoutMode();
}
window.initTabs = initTabs;

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
      if (sidebar && sidebar.classList.contains("drawer-open")) closeDrawer();
      else openDrawer();
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

  document.querySelectorAll(".sidebar-item-btn").forEach(btn => {
    btn.addEventListener("click", () => {
      if (window.innerWidth <= 768) closeDrawer();
    });
  });

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
  if (isCollapsed) sidebar.classList.add("sidebar-collapsed");
  updateToggleUI(isCollapsed);

  if (toggleBtn) {
    toggleBtn.onclick = (e) => {
      if (e) { e.preventDefault(); e.stopPropagation(); }
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

  const curTheme = document.body.classList.contains("light-theme") ? "light" : "dark";
  const themeChips = document.querySelectorAll("#settingThemeSwitch .chip");
  themeChips.forEach(chip => {
    chip.classList.toggle("active", chip.dataset.theme === curTheme);
    chip.onclick = () => {
      const theme = chip.dataset.theme;
      if (theme === "light") document.body.classList.add("light-theme");
      else document.body.classList.remove("light-theme");
      localStorage.setItem("zg_theme", theme);
      themeChips.forEach(c => c.classList.toggle("active", c.dataset.theme === theme));
      document.dispatchEvent(new CustomEvent("zg:themechange", { detail: { theme } }));
      toast(`已切换为${theme === "light" ? "浅色" : "深色二次元"}模式`);
    };
  });

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

  if (state) {
    if ($("cfgListenHost") && !$("cfgListenHost").value) $("cfgListenHost").value = state.listen_host || "127.0.0.1";
    if ($("cfgListenPort") && !$("cfgListenPort").value) $("cfgListenPort").value = state.listen_port || 7863;
    if ($("cfgApiKey") && !$("cfgApiKey").value) $("cfgApiKey").value = state.api_key || "WildWorkAPI";
    if ($("chkAutostart")) $("chkAutostart").checked = !!state.autostart;
    if ($("sysEnvDesc")) $("sysEnvDesc").textContent = `ZeroGate Core v2.0.1 · 多渠道聚合中枢 · ${state.listen_host || "127.0.0.1"}:${state.listen_port || 7863}`;
  }

  if ($("btnSaveListenCfg")) {
    $("btnSaveListenCfg").onclick = async () => {
      const host = ($("cfgListenHost") ? $("cfgListenHost").value : "").trim() || "127.0.0.1";
      const port = parseInt($("cfgListenPort") ? $("cfgListenPort").value : "7863", 10) || 7863;
      try {
        await api("/api/config/listen", { host, port });
        toast("监听配置已保存（端点改动在重启后生效）");
        await loadState();
      } catch (e) { toast(e.message); }
    };
  }

  if ($("btnSaveApiKeyCfg")) {
    $("btnSaveApiKeyCfg").onclick = async () => {
      const key = ($("cfgApiKey") ? $("cfgApiKey").value : "").trim();
      if (!key) { toast("API Key 不能为空"); return; }
      try {
        await api("/api/config/api_key", { key });
        toast("主 API-Key 已保存");
        await loadState();
      } catch (e) { toast(e.message); }
    };
  }

  if ($("btnRefreshFeesCache")) {
    $("btnRefreshFeesCache").onclick = async () => {
      if (typeof loadFees === "function") await loadFees();
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
window.loadSettingsView = loadSettingsView;

// ---------- 壁纸系统与控制面板 ----------
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

  const savedWp = localStorage.getItem("zg_wallpaper");
  const cur = savedWp || "./wallpapers/cyberpunk.jpg";
  setBg(cur);

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

  document.querySelectorAll(".wp-thumb").forEach(th => {
    th.onclick = () => {
      const wp = th.dataset.wp;
      setBg(wp);
      toast("已更换二次元全景壁纸 ✨");
      if (wpCard) wpCard.classList.remove("open");
    };
  });

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
      const v = ($("inpCustomWp") ? $("inpCustomWp").value : "").trim();
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

// ---------- 全局弹窗键盘与焦点交互增强 ----------
function initModalEnhancements() {
  document.addEventListener("keydown", (e) => {
    if (e.key === "Escape") {
      const visibleOverlays = Array.from(document.querySelectorAll(".overlay:not(.hidden)"));
      if (visibleOverlays.length > 0) {
        const topOverlay = visibleOverlays[visibleOverlays.length - 1];
        topOverlay.classList.add("hidden");
        e.stopPropagation();
      }
    }
  });

  const observer = new MutationObserver((mutations) => {
    for (const m of mutations) {
      if (m.type === "attributes" && m.attributeName === "class") {
        const target = m.target;
        if (target.classList.contains("overlay") && !target.classList.contains("hidden")) {
          const firstInput = target.querySelector("input:not([type=hidden]):not([disabled]), textarea:not([disabled])");
          if (firstInput) {
            setTimeout(() => firstInput.focus(), 80);
          }
        }
      }
    }
  });

  document.querySelectorAll(".overlay").forEach((ov) => {
    observer.observe(ov, { attributes: true });

    ov.addEventListener("click", (e) => {
      if (e.target === ov) {
        ov.classList.add("hidden");
      }
    });

    ov.addEventListener("keydown", (e) => {
      if (e.key === "Enter" && (e.target.tagName === "INPUT" || e.target.tagName === "TEXTAREA")) {
        if (e.target.tagName === "TEXTAREA" && !e.ctrlKey && !e.metaKey) return;
        const submitBtn = ov.querySelector(".btn.primary:not([disabled]), #btnConfirmOk, #aclConfirm, #wbtConfirm");
        if (submitBtn && submitBtn !== e.target) {
          e.preventDefault();
          submitBtn.click();
        }
      }
    });
  });
}

// ---------- 全局事件绑定 ----------
function bind() {
  if ($("btnAddAcct")) $("btnAddAcct").onclick = (e) => { e.stopPropagation(); toggleAddMenu(); };
  if ($("addAcctMenu")) {
    $("addAcctMenu").addEventListener("click", (e) => {
      const item = e.target.closest(".dropdown-item");
      if (!item) return;
      toggleAddMenu(false);
      promptLogin(item.dataset.channel);
    });
  }
  document.addEventListener("click", (e) => {
    if (!e.target.closest("#addAcctDrop") && typeof toggleAddMenu === "function") toggleAddMenu(false);
  });

  if ($("acctSearch")) {
    $("acctSearch").addEventListener("input", (e) => {
      if (typeof window.setAcctSearch === "function") window.setAcctSearch(e.target.value.trim().toLowerCase());
      if (typeof renderAccounts === "function") renderAccounts();
    });
  }

  document.querySelectorAll("#acctFilter .chip").forEach(c => {
    c.addEventListener("click", () => {
      if (typeof window.setAcctFilter === "function") window.setAcctFilter(c.dataset.filter);
      document.querySelectorAll("#acctFilter .chip").forEach(x => x.classList.toggle("active", x === c));
      if (typeof renderAccounts === "function") renderAccounts();
    });
  });

  if ($("btnExpandAll")) $("btnExpandAll").onclick = () => { if (typeof setAllGroups === "function") setAllGroups(true); };
  if ($("btnCollapseAll")) $("btnCollapseAll").onclick = () => { if (typeof setAllGroups === "function") setAllGroups(false); };
  if ($("btnCheckinAll")) $("btnCheckinAll").onclick = openCheckinDialog;
  if ($("btnRefreshAll")) $("btnRefreshAll").onclick = refreshAll;
  if ($("btnAddTime")) $("btnAddTime").onclick = addTime;

  // 批量签到弹层
  if ($("btnCiStart")) $("btnCiStart").onclick = runBatchCheckin;
  if ($("btnCiClose")) $("btnCiClose").onclick = closeCheckinDialog;
  if ($("ciSelAll")) $("ciSelAll").onclick = () => ciSetAll(true);
  if ($("ciSelNone")) $("ciSelNone").onclick = () => ciSetAll(false);
  if ($("ciInvert")) $("ciInvert").onclick = ciInvertSel;
  if ($("ciOnlyUnchecked")) $("ciOnlyUnchecked").onclick = ciOnlyUnchecked;
  if ($("ciSkipExpired")) $("ciSkipExpired").onclick = ciSkipExpired;
  if ($("checkinOverlay")) $("checkinOverlay").onclick = (e) => { if (e.target === $("checkinOverlay")) closeCheckinDialog(); };

  // 豆包保活弹层
  if ($("btnDoubao")) $("btnDoubao").onclick = openDoubaoDialog;
  if ($("btnDbRenew")) $("btnDbRenew").onclick = runDoubaoRenew;
  if ($("btnDbClose")) $("btnDbClose").onclick = closeDoubaoDialog;
  if ($("doubaoOverlay")) $("doubaoOverlay").onclick = (e) => { if (e.target === $("doubaoOverlay")) closeDoubaoDialog(); };
  if ($("btnCopyUrl")) $("btnCopyUrl").onclick = copyUrl;
  if ($("btnCancelLogin")) $("btnCancelLogin").onclick = cancelLogin;
  if ($("btnRefreshFees") && typeof refreshFees === "function") $("btnRefreshFees").onclick = refreshFees;
  if ($("chkAutostart")) $("chkAutostart").onchange = toggleAutostart;

  // 成长中心与签到
  if ($("btnAddGrowthTime") && typeof addGrowthTime === "function") $("btnAddGrowthTime").onclick = addGrowthTime;
  if ($("btnSaveGrowthTimes") && typeof saveGrowthTimes === "function") $("btnSaveGrowthTimes").onclick = () => saveGrowthTimes((state.growth_times || []).slice());
  if ($("btnRunGrowthNow") && typeof runGrowthNow === "function") $("btnRunGrowthNow").onclick = runGrowthNow;
  if ($("btnRefreshGrowthToday") && typeof loadGrowthToday === "function") $("btnRefreshGrowthToday").onclick = loadGrowthToday;
  if ($("btnRefreshGrowthAccounts") && typeof loadGrowthAccounts === "function") $("btnRefreshGrowthAccounts").onclick = loadGrowthAccounts;
  if ($("btnRefreshGrowthRecords") && typeof loadGrowthRecords === "function") $("btnRefreshGrowthRecords").onclick = loadGrowthRecords;
  if ($("selGrowthLimit") && typeof loadGrowthRecords === "function") $("selGrowthLimit").onchange = loadGrowthRecords;
  if ($("btnGrowthDetailClose") && typeof closeGrowthDetail === "function") $("btnGrowthDetailClose").onclick = closeGrowthDetail;
  if ($("growthDetailOverlay") && typeof closeGrowthDetail === "function") $("growthDetailOverlay").onclick = (e) => { if (e.target === $("growthDetailOverlay")) closeGrowthDetail(); };

  if ($("btnGrowthBatchCheckin")) $("btnGrowthBatchCheckin").onclick = openCheckinDialog;
  if ($("btnClaimRefresh") && typeof loadClaimStatus === "function") $("btnClaimRefresh").onclick = loadClaimStatus;
  if ($("btnClaimNow") && typeof runClaimNow === "function") $("btnClaimNow").onclick = runClaimNow;

  // 本地桥
  if ($("btnBridgeRefresh") && typeof loadBridges === "function") $("btnBridgeRefresh").onclick = loadBridges;
  if ($("btnBridgeStartAll") && typeof bridgeStartAll === "function") $("btnBridgeStartAll").onclick = bridgeStartAll;

  // Atria Key 管理
  if ($("btnAtriaAdd") && typeof addAtriaKey === "function") $("btnAtriaAdd").onclick = addAtriaKey;
  if ($("btnAtriaBatch") && typeof addAtriaBatch === "function") $("btnAtriaBatch").onclick = addAtriaBatch;
  if ($("btnAtriaClose") && typeof closeAtriaDialog === "function") $("btnAtriaClose").onclick = closeAtriaDialog;
  if ($("atriaOverlay") && typeof closeAtriaDialog === "function") $("atriaOverlay").onclick = (e) => { if (e.target === $("atriaOverlay")) closeAtriaDialog(); };
  if ($("atriaKey") && typeof addAtriaKey === "function") $("atriaKey").addEventListener("keydown", (e) => { if (e.key === "Enter") addAtriaKey(); });

  // 顶栏复制与修改配置
  if ($("apiAddr")) {
    $("apiAddr").onclick = (e) => {
      e.stopPropagation();
      const v = $("apiAddr").querySelector(".val").textContent;
      copyText(v, "OpenAI 接口地址");
    };
  }
  if ($("apiKeyDisplay")) {
    $("apiKeyDisplay").onclick = (e) => {
      e.stopPropagation();
      const v = $("apiKeyDisplay").querySelector(".val").textContent;
      if (v === "（无鉴权）") { toast("当前未设置 API-Key"); return; }
      copyText(v, "API-Key");
    };
  }
  if ($("btnEditApiConfig")) $("btnEditApiConfig").onclick = (e) => { e.stopPropagation(); openApiConfig(); };
  if ($("btnEditApiKey")) $("btnEditApiKey").onclick = (e) => { e.stopPropagation(); openApiKey(); };

  if ($("btnHelp")) $("btnHelp").onclick = openHelp;
  if ($("btnAbout")) $("btnAbout").onclick = openAbout;
  if ($("btnHelpClose")) $("btnHelpClose").onclick = closeHelp;
  if ($("btnAboutClose")) $("btnAboutClose").onclick = closeAbout;

  // 登录确认与认证通道选择
  if ($("btnLoginConfirm")) $("btnLoginConfirm").onclick = confirmLogin;
  if ($("btnLoginConfirmCancel")) $("btnLoginConfirmCancel").onclick = () => $("loginConfirmOverlay").classList.add("hidden");
  if ($("btnZcZai")) $("btnZcZai").onclick = () => pickZcodeProvider($("btnZcZai"));
  if ($("btnZcBigmodel")) $("btnZcBigmodel").onclick = () => pickZcodeProvider($("btnZcBigmodel"));
  if ($("aclSend")) $("aclSend").onclick = () => void aclSendCode();
  if ($("aclConfirm")) $("aclConfirm").onclick = () => void aclConfirm();
  if ($("aclCancel")) $("aclCancel").onclick = closeAutoclawDialog;
  if ($("wbtCancel")) $("wbtCancel").onclick = closeWbtDialog;
  if ($("wbtConfirm")) $("wbtConfirm").onclick = () => void wbtImport();
  if ($("loginConfirmOverlay")) $("loginConfirmOverlay").onclick = (e) => { if (e.target === $("loginConfirmOverlay")) $("loginConfirmOverlay").classList.add("hidden"); };

  if ($("btnApiSave")) $("btnApiSave").onclick = saveApiConfig;
  if ($("btnApiCancel")) $("btnApiCancel").onclick = closeApiConfig;
  if ($("selHost")) $("selHost").onchange = () => {
    if ($("customHostRow")) $("customHostRow").classList.toggle("hidden", $("selHost").value !== "__custom__");
  };

  if ($("btnKeySave")) $("btnKeySave").onclick = saveApiKey;
  if ($("btnKeyCancel")) $("btnKeyCancel").onclick = closeApiKey;
  if ($("keyInput")) $("keyInput").addEventListener("keydown", (e) => { if (e.key === "Enter") saveApiKey(); });

  if ($("apiConfigOverlay")) $("apiConfigOverlay").onclick = (e) => { if (e.target === $("apiConfigOverlay")) closeApiConfig(); };
  if ($("apiKeyOverlay")) $("apiKeyOverlay").onclick = (e) => { if (e.target === $("apiKeyOverlay")) closeApiKey(); };
  if ($("helpOverlay")) $("helpOverlay").onclick = (e) => { if (e.target === $("helpOverlay")) closeHelp(); };
  if ($("aboutOverlay")) $("aboutOverlay").onclick = (e) => { if (e.target === $("aboutOverlay")) closeAbout(); };

  // 路由与客户端 Tab 功能绑定
  if (typeof initAuditLogsEvents === "function") initAuditLogsEvents();
  if ($("btnCreateClient") && typeof createClient === "function") $("btnCreateClient").onclick = createClient;
  if ($("inClientName") && typeof createClient === "function") $("inClientName").addEventListener("keydown", (e) => { if (e.key === "Enter") createClient(); });
  if ($("btnAddRedirect") && typeof saveRedirect === "function") $("btnAddRedirect").onclick = saveRedirect;
  if ($("btnResetRedirects") && typeof resetRedirects === "function") $("btnResetRedirects").onclick = resetRedirects;
  if ($("btnRefreshTokens") && typeof loadTokenAnalytics === "function") $("btnRefreshTokens").onclick = loadTokenAnalytics;
  if ($("btnRefreshAssets") && typeof loadAssets === "function") $("btnRefreshAssets").onclick = loadAssets;

  initModalEnhancements();
}

// ---------- 应用程序统一启动入口 ----------
async function init() {
  bind();
  initTabs();
  await loadState();
  if (typeof loadFees === "function") loadFees();
  if (typeof loadDashboard === "function") loadDashboard();
}

document.addEventListener("DOMContentLoaded", init);