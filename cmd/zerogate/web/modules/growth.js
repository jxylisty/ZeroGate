// ============================================================
// ZeroGate WebUI - Growth & Bonus Module (成长中心、每日签到、周末领奖与 Atria 密钥库)
// ============================================================

(function () {
  "use strict";

  // ============================================================
  // 1. 成长中心可视化面板
  // ============================================================

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
    const times = (window.state && window.state.growth_times) || [];
    box.innerHTML = times.map((t) =>
      `<span class="time-chip" title="点击删除" onclick="delGrowthTime('${t}')">${t}${I.x}</span>`).join("");
    if (!times.length) box.innerHTML = `<span class="tag neutral">暂无配置</span>`;
    if ($("nextGrowth")) $("nextGrowth").textContent = (window.state && window.state.next_growth) || "-";
  }

  async function addGrowthTime() {
    const times = ((window.state && window.state.growth_times) || []).slice();
    const now = new Date();
    const next = `${String(now.getHours()).padStart(2, "0")}:${String(now.getMinutes()).padStart(2, "0")}`;
    if (times.includes(next)) { toast("该时间已存在"); return; }
    times.push(next);
    await saveGrowthTimes(times.sort());
  }

  async function delGrowthTime(t) {
    await saveGrowthTimes(((window.state && window.state.growth_times) || []).filter((x) => x !== t));
  }

  async function saveGrowthTimes(times) {
    const btn = $("btnSaveGrowthTimes");
    if (btn) btn.disabled = true;
    try {
      await api("/api/config/growth_times", { times });
      toast("成长中心时间已更新");
      if (typeof window.loadState === "function") await window.loadState();
    } catch (e) {
      toast(e.message);
    } finally {
      if (btn) btn.disabled = false;
    }
  }

  function runGrowthNow() {
    confirmDialog("确定立即对所有 WorkBuddy 账号执行成长中心（旅行/盲盒/任务领奖）？", async () => {
      const btn = $("btnRunGrowthNow");
      if (btn) {
        btn.disabled = true;
        btn.textContent = "执行中…";
      }
      try {
        await api("/api/growth/run_now", {});
        toast("批量执行已启动，稍后刷新查看结果");
        setTimeout(() => { loadGrowthToday(); loadGrowthRecords(); }, 5000);
        setTimeout(() => { loadGrowthToday(); loadGrowthRecords(); }, 12000);
      } catch (e) {
        toast(e.message);
      } finally {
        if (btn) {
          btn.disabled = false;
          btn.textContent = "批量运行";
        }
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

  function showGrowthDetail(details) {
    const list = $("growthDetailList");
    if (list) {
      list.innerHTML = details.map((d) => `
        <div class="grd-item">
          <span class="grd-name">${esc(d.name || "任务")}</span>
          <span class="grd-reward">
            ${d.credits > 0 ? `<b class="grd-credits">+${fmtTokens(d.credits)} 积分</b>` : ""}
            ${d.coupon_name ? `<span class="grd-coupon">${esc(d.coupon_name)}</span>` : ""}
            ${!d.credits && !d.coupon_name ? `<span class="tag neutral">无奖励</span>` : ""}
          </span>
        </div>`).join("");
    }
    const overlay = $("growthDetailOverlay");
    if (overlay) overlay.classList.remove("hidden");
  }

  function closeGrowthDetail() {
    const overlay = $("growthDetailOverlay");
    if (overlay) overlay.classList.add("hidden");
  }

  function initGrowthTab() {
    renderGrowthTimes();
    loadGrowthCheckin();
    loadClaimStatus();
    loadGrowthAccounts();
    loadGrowthToday();
    loadGrowthRecords();
  }

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
  // 2. 每日签到卡片
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
    const list = ((window.state && window.state.accounts) || []).filter((a) => {
      const g = a.group;
      return g === "workbuddy" || g === "traework" || g === "autoclaw" || g === "qoder" || g === "codebuddy" || g === "zcode";
    });
    wrap.innerHTML = list.length
      ? list.map(growthCheckinCardHTML).join("")
      : `<div class="empty-hint">还没有账号，去「账号矩阵」添加</div>`;
  }

  // ============================================================
  // 3. ZCode 周末包领取可视化
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
    if (btn) {
      btn.disabled = true;
      btn.textContent = "执行中…";
    }
    try {
      await api("/api/zcode/claim_now", {});
      toast("领取已启动（每实例最多 90 秒），稍后自动刷新结果");
      setTimeout(loadClaimStatus, 3000);
      setTimeout(loadClaimStatus, 12000);
      setTimeout(loadClaimStatus, 30000);
    } catch (e) {
      toast(e.message);
    } finally {
      if (btn) {
        btn.disabled = false;
        btn.innerHTML = `${I.zap}立即领取`;
      }
    }
  }

  // ============================================================
  // 4. Atria 官方 Key 集中管理
  // ============================================================

  function openAtriaDialog() {
    if ($("atriaKey")) $("atriaKey").value = "";
    if ($("atriaName")) $("atriaName").value = "";
    if ($("atriaBatch")) $("atriaBatch").value = "";
    if ($("atriaMsg")) $("atriaMsg").textContent = "";
    if ($("atriaOverlay")) $("atriaOverlay").classList.remove("hidden");
    loadAtriaList();
  }

  function closeAtriaDialog() {
    if ($("atriaOverlay")) $("atriaOverlay").classList.add("hidden");
  }

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
    const key = $("atriaKey") ? $("atriaKey").value.trim() : "";
    if (!key) { toast("请粘贴 Atria API Key"); return; }
    const btn = $("btnAtriaAdd");
    if (btn) btn.disabled = true;
    try {
      const r = await api("/api/atria/add", { key, nickname: $("atriaName") ? $("atriaName").value.trim() : "" });
      if ($("atriaMsg")) $("atriaMsg").textContent = `已添加：${r.nickname || r.uid}`;
      if ($("atriaKey")) $("atriaKey").value = "";
      if ($("atriaName")) $("atriaName").value = "";
      toast("Atria Key 已添加入池");
      loadAtriaList();
      if (typeof window.loadState === "function") window.loadState();
    } catch (e) {
      if ($("atriaMsg")) $("atriaMsg").textContent = "失败：" + e.message;
    } finally {
      if (btn) btn.disabled = false;
    }
  }

  async function addAtriaBatch() {
    const text = $("atriaBatch") ? $("atriaBatch").value.trim() : "";
    if (!text) { toast("请先粘贴 Key 列表"); return; }
    const btn = $("btnAtriaBatch");
    if (btn) btn.disabled = true;
    try {
      const r = await api("/api/atria/add_batch", { keys: text });
      const errs = (r.results || []).filter((x) => x.status === "error");
      if ($("atriaMsg")) $("atriaMsg").textContent = `新增 ${r.added} 个${errs.length ? `，失败 ${errs.length} 个` : ""}`;
      toast(`批量导入完成：新增 ${r.added} 个`);
      if ($("atriaBatch")) $("atriaBatch").value = "";
      loadAtriaList();
      if (typeof window.loadState === "function") window.loadState();
    } catch (e) {
      if ($("atriaMsg")) $("atriaMsg").textContent = "失败：" + e.message;
    } finally {
      if (btn) btn.disabled = false;
    }
  }

  // 挂载全局方法
  window.growthStatusTag = growthStatusTag;
  window.growthBadgeCls = growthBadgeCls;
  window.fmtGrowthTime = fmtGrowthTime;
  window.renderGrowthTimes = renderGrowthTimes;
  window.addGrowthTime = addGrowthTime;
  window.delGrowthTime = delGrowthTime;
  window.saveGrowthTimes = saveGrowthTimes;
  window.runGrowthNow = runGrowthNow;
  window.loadGrowthToday = loadGrowthToday;
  window.renderGrowthTodaySummary = renderGrowthTodaySummary;
  window.loadGrowthRecords = loadGrowthRecords;
  window.renderGrowthRecords = renderGrowthRecords;
  window.showGrowthDetail = showGrowthDetail;
  window.closeGrowthDetail = closeGrowthDetail;
  window.initGrowthTab = initGrowthTab;
  window.loadGrowthAccounts = loadGrowthAccounts;
  window.renderGrowthAccounts = renderGrowthAccounts;
  window.runGrowthAccount = runGrowthAccount;

  window.growthCheckinCardHTML = growthCheckinCardHTML;
  window.loadGrowthCheckin = loadGrowthCheckin;

  window.claimRecTag = claimRecTag;
  window.renderClaimStatus = renderClaimStatus;
  window.loadClaimStatus = loadClaimStatus;
  window.runClaimNow = runClaimNow;

  window.openAtriaDialog = openAtriaDialog;
  window.closeAtriaDialog = closeAtriaDialog;
  window.loadAtriaList = loadAtriaList;
  window.addAtriaKey = addAtriaKey;
  window.addAtriaBatch = addAtriaBatch;

})();
