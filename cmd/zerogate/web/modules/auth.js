// ============================================================
// ZeroGate WebUI - 认证与账号流程模块 (modules/auth.js)
// 包含各渠道登录弹窗、OAuth/Token导入、手机验证码、批量签到与豆包保活续期
// ============================================================

"use strict";

(function () {
  // ---------- 登录与通道状态 ----------
  let pendingChannel = null;
  let pendingProvider = "zai";
  let loginPoll = null;

  function pickZcodeProvider(btn) {
    pendingProvider = btn.dataset.provider;
    document.querySelectorAll("#zcodeProviderRow .btn").forEach((b) => b.classList.remove("primary"));
    btn.classList.add("primary");
  }

  // ---------- CodeBuddy / WBIntl Token 导入 ----------
  function openWbtDialog(channel) {
    if ($("wbtTitle")) $("wbtTitle").textContent = "添加 " + groupMeta(channel).name + " 账号（粘贴 token）";
    if ($("wbtToken")) $("wbtToken").value = "";
    if ($("wbtRefresh")) $("wbtRefresh").value = "";
    if ($("wbtName")) $("wbtName").value = "";
    if ($("wbtMsg")) $("wbtMsg").textContent = "";
    if ($("wbtokenOverlay")) $("wbtokenOverlay").classList.remove("hidden");
    setTimeout(() => { if ($("wbtToken")) $("wbtToken").focus(); }, 80);
  }

  function closeWbtDialog() {
    if ($("wbtokenOverlay")) $("wbtokenOverlay").classList.add("hidden");
  }

  async function wbtImport() {
    const token = $("wbtToken") ? $("wbtToken").value.trim() : "";
    if (!token) { toast("请粘贴 accessToken"); return; }
    const domain = "codebuddy.ai";
    const btn = $("wbtConfirm");
    if (btn) {
      btn.disabled = true;
      btn.classList.add("is-loading");
    }
    try {
      const r = await api("/api/wbintl/add", {
        domain,
        token,
        refresh_token: $("wbtRefresh") ? $("wbtRefresh").value.trim() : "",
        nickname: $("wbtName") ? $("wbtName").value.trim() : ""
      });
      closeWbtDialog();
      toast("导入成功：" + (r.nickname || r.uid));
      if (typeof window.loadState === "function") await window.loadState();
      if (typeof window.refreshFees === "function") window.refreshFees();
    } catch (e) {
      if ($("wbtMsg")) $("wbtMsg").textContent = "导入失败：" + e.message;
    } finally {
      if (btn) {
        btn.disabled = false;
        btn.classList.remove("is-loading");
      }
    }
  }

  // ---------- AutoClaw 手机验证码登录 ----------
  async function openAutoclawDialog() {
    pendingChannel = null;
    if ($("aclPhone")) $("aclPhone").value = "";
    if ($("aclCode")) $("aclCode").value = "";
    if ($("aclMsg")) $("aclMsg").textContent = "";
    if ($("autoclawOverlay")) $("autoclawOverlay").classList.remove("hidden");
    setTimeout(() => { if ($("aclPhone")) $("aclPhone").focus(); }, 80);
  }

  function closeAutoclawDialog() {
    if ($("autoclawOverlay")) $("autoclawOverlay").classList.add("hidden");
  }

  async function aclSendCode() {
    const phone = $("aclPhone") ? $("aclPhone").value.trim() : "";
    if (!phone) { toast("请先输入手机号"); return; }
    const btn = $("aclSend");
    if (btn) {
      btn.disabled = true;
      btn.classList.add("is-loading");
    }
    try {
      await api("/api/autoclaw/send_code", { phone });
      if ($("aclMsg")) $("aclMsg").textContent = "验证码已发送至 " + phone + "，请查收短信";
      if ($("aclCode")) $("aclCode").focus();
    } catch (e) {
      if ($("aclMsg")) $("aclMsg").textContent = "发送失败：" + e.message;
    } finally {
      if (btn) {
        btn.disabled = false;
        btn.classList.remove("is-loading");
      }
    }
  }

  async function aclConfirm() {
    const phone = $("aclPhone") ? $("aclPhone").value.trim() : "";
    const code = $("aclCode") ? $("aclCode").value.trim() : "";
    if (!phone || !code) { toast("请输入手机号和验证码"); return; }
    const btn = $("aclConfirm");
    if (btn) {
      btn.disabled = true;
      btn.classList.add("is-loading");
    }
    try {
      const r = await api("/api/autoclaw/login", { phone, code });
      closeAutoclawDialog();
      toast("AutoClaw 登录成功：" + (r.nickname || r.uid));
      if (typeof window.loadState === "function") await window.loadState();
      if (typeof window.refreshFees === "function") window.refreshFees();
    } catch (e) {
      if ($("aclMsg")) $("aclMsg").textContent = "登录失败：" + e.message;
    } finally {
      if (btn) {
        btn.disabled = false;
        btn.classList.remove("is-loading");
      }
    }
  }

  // ---------- 通用浏览器登录启动与轮询 ----------
  function promptLogin(channel) {
    pendingChannel = channel;
    const name = groupMeta(channel).name;
    if (channel === "autoclaw") { openAutoclawDialog(); return; }
    if (channel === "atria") { if (typeof window.openAtriaDialog === "function") window.openAtriaDialog(); return; }
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

  function confirmLogin() {
    if ($("loginConfirmOverlay")) $("loginConfirmOverlay").classList.add("hidden");
    if (pendingChannel) startLogin(pendingChannel);
  }

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

  async function cancelLogin() {
    try {
      await api("/api/login/cancel", {});
      stopLoginPoll();
      if ($("loginOverlay")) $("loginOverlay").classList.add("hidden");
      toast("登录已取消");
    } catch (e) { toast(e.message); }
  }

  function copyUrl() {
    const url = $("btnCopyUrl") ? $("btnCopyUrl").dataset.url : "";
    if (!url) { toast("暂无链接"); return; }
    navigator.clipboard.writeText(url).then(() => toast("链接已复制")).catch(() => toast("复制失败，请手动复制"));
  }

  function startLoginPoll() {
    stopLoginPoll();
    loginPoll = setInterval(async () => {
      try {
        const st = await api("/api/state");
        if (!st.login_busy) {
          stopLoginPoll();
          if ($("loginOverlay")) $("loginOverlay").classList.add("hidden");
          toast("登录完成，正在同步账号…");
          if (typeof window.loadState === "function") await window.loadState();
          if (typeof window.refreshFees === "function") window.refreshFees();
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

  function ciExpired(a) {
    if (a.disabled) return true;
    const t = parseMD(a.until);
    return !!t && t.getTime() <= Date.now();
  }

  function openCheckinDialog() {
    const accounts = (window.state && window.state.accounts) || [];
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

  function closeCheckinDialog() {
    if ($("checkinOverlay")) $("checkinOverlay").classList.add("hidden");
    ciRun = false;
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

  function ciInvertSel() {
    document.querySelectorAll("#ciList input[type=checkbox]").forEach((c) => {
      if (c.disabled) return;
      c.checked = !c.checked;
      if (c.checked) ciSel.add(c.dataset.uid); else ciSel.delete(c.dataset.uid);
    });
    updateCiCount();
  }

  function ciOnlyUnchecked() {
    for (const a of (window.state && window.state.accounts) || []) if (isTodayChecked(a)) ciSel.delete(a.uid);
    ciSyncChecks();
  }

  function ciSkipExpired() {
    for (const a of (window.state && window.state.accounts) || []) if (ciExpired(a)) ciSel.delete(a.uid);
    ciSyncChecks();
  }

  async function runBatchCheckin() {
    if (ciRun) return;
    const uids = [...ciSel];
    if (!uids.length) { toast("请先选择要签到的账号"); return; }
    ciRun = true;
    const btn = $("btnCiStart");
    if (btn) {
      btn.disabled = true;
      btn.classList.add("is-loading");
    }
    if ($("ciProgress")) $("ciProgress").classList.remove("hidden");
    const box = $("ciResults");
    if (box) box.innerHTML = "";
    const total = uids.length;
    let ok = 0, already = 0, fail = 0;
    const nameOf = (uid) => {
      const a = (window.state && window.state.accounts ? window.state.accounts : []).find((x) => x.uid === uid);
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
    if (btn) {
      btn.disabled = false;
      btn.classList.remove("is-loading");
    }
    if (typeof window.loadState === "function") window.loadState();
  }

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

  async function runDoubaoRenew() {
    if (dbBusy) return;
    dbBusy = true;
    const btn = $("btnDbRenew");
    if (btn) {
      btn.disabled = true;
      btn.classList.add("is-loading");
      btn.textContent = "保活中…";
    }
    try {
      renderDoubao(await api("/api/doubao/renew", {}));
    } catch (e) {
      if ($("dbSummary")) $("dbSummary").textContent = "";
      if ($("dbList")) $("dbList").innerHTML = `<div class="db-row error"><span class="db-detail">${esc(e.message)}</span></div>`;
    } finally {
      dbBusy = false;
      if (btn) {
        btn.disabled = false;
        btn.classList.remove("is-loading");
        btn.textContent = "立即保活";
      }
    }
  }

  function closeDoubaoDialog() {
    if ($("doubaoOverlay")) $("doubaoOverlay").classList.add("hidden");
  }

  // ---------- 刷新全部账号积分 ----------
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
      if (typeof window.loadState === "function") window.loadState();
    } catch (e) {
      toast(e.message);
    } finally {
      btn.disabled = false;
      btn.classList.remove("is-loading");
      btn.innerHTML = oldHTML;
    }
  }

  // ---------- 导出到全局命名空间 ----------
  window.pickZcodeProvider = pickZcodeProvider;
  window.openWbtDialog = openWbtDialog;
  window.closeWbtDialog = closeWbtDialog;
  window.wbtImport = wbtImport;
  window.openAutoclawDialog = openAutoclawDialog;
  window.closeAutoclawDialog = closeAutoclawDialog;
  window.aclSendCode = aclSendCode;
  window.aclConfirm = aclConfirm;
  window.promptLogin = promptLogin;
  window.confirmLogin = confirmLogin;
  window.startLogin = startLogin;
  window.cancelLogin = cancelLogin;
  window.copyUrl = copyUrl;
  window.startLoginPoll = startLoginPoll;
  window.stopLoginPoll = stopLoginPoll;
  window.isTodayChecked = isTodayChecked;
  window.openCheckinDialog = openCheckinDialog;
  window.closeCheckinDialog = closeCheckinDialog;
  window.ciRowChg = ciRowChg;
  window.ciSetAll = ciSetAll;
  window.ciInvertSel = ciInvertSel;
  window.ciOnlyUnchecked = ciOnlyUnchecked;
  window.ciSkipExpired = ciSkipExpired;
  window.runBatchCheckin = runBatchCheckin;
  window.openDoubaoDialog = openDoubaoDialog;
  window.runDoubaoRenew = runDoubaoRenew;
  window.closeDoubaoDialog = closeDoubaoDialog;
  window.refreshAll = refreshAll;
})();
