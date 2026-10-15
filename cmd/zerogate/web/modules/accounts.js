// ZeroGate — 账号与调度矩阵模块 (modules/accounts.js)
// 包含账号卡片渲染、套餐明细浮层、单账号操作、批量签到、豆包保活、成长中心及登录导入弹窗
(function () {
  "use strict";

  // 1. 积分明细 Tooltip
  let detailTimer = null;
  let detailCache = {};
  window.detailCache = detailCache;
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

  window.addEventListener("scroll", () => closeCreditTip(), { passive: true });
  document.addEventListener("pointerdown", (e) => {
    const tip = $("creditTip");
    if (tip && tip.style.display !== "none" && !tip.contains(e.target) && !e.target.closest(".acct-credits") && !e.target.closest(".exp-part-badge")) {
      closeCreditTip();
    }
  });

  // 2. 账号矩阵手风琴与模式分区配置
  const MODE_SECTIONS = [
    { key: "proxy",  name: "反代账号", desc: "网关持有网页版/客户端凭证，模拟官方客户端转发" },
    { key: "bridge", name: "桥接账号", desc: "转发到本地 zcode-proxy 实例，网关不直接持有凭证" },
    { key: "direct", name: "直连账号", desc: "官方 API Key 直连，非反代，独立管理" },
  ];
  const MODE_ORDER = { proxy: 0, bridge: 1, direct: 2 };
  const CHANNEL_ORDER = [
    "workbuddy", "codebuddy", "traework", "qoder", "autoclaw", "copilot", "doubao", "gptgemini",
    "zcode", "atria", "glmflash", "groq", "tierflow",
  ];

  function saveGroupOpen(gkey, open) {
    try {
      const cur = JSON.parse(localStorage.getItem("zg_group_open") || "{}");
      cur[gkey] = !!open;
      localStorage.setItem("zg_group_open", JSON.stringify(cur));
    } catch (_) {}
  }

  function getGroupOpen(gkey, def = true) {
    try {
      const cur = JSON.parse(localStorage.getItem("zg_group_open") || "{}");
      if (typeof cur[gkey] === "boolean") return cur[gkey];
    } catch (_) {}
    return def;
  }

  // 3. 账号卡片 HTML 渲染
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
      ? `<span class="tag next-pick" title="本渠道第一顺位：下一次请求将优先调用此账号">${I.target} 优先调度</span>`
      : "";
    const bridgeDownTag = a.bridge_down
      ? `<span class="tag danger" title="该账号对应的本地桥进程未启动（端口连不上）">${I.alert} 桥未启动</span>`
      : "";

    const disabledClass = a.disabled ? " disabled" : "";
    const disableIcon = a.disabled ? I.play : I.pause;
    const disableTitle = a.disabled ? "启用" : "停用";
    const checkinBtn = noCheckin
      ? `<span class="icon-op off" title="${groupName} 无签到活动" onclick="return false">${I.check}</span>`
      : `<span class="icon-op" title="签到" onclick="checkin('${a.uid}', event)">${I.check}</span>`;
    const growthBtn = a.group === "workbuddy"
      ? `<span class="icon-op" title="成长中心（旅行/盲盒/任务领奖）" onclick="growth('${a.uid}', event)">${I.gift}</span>`
      : "";

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
          <span class="icon-op" title="刷新积分" onclick="refreshOne('${a.uid}', event)">${I.refresh}</span>
          <span class="icon-op warn" title="${disableTitle}" onclick="toggleDisable('${a.uid}',${a.disabled})">${disableIcon}</span>
          <span class="icon-op danger" title="删除账号" onclick="removeAcct('${a.uid}')">${I.x}</span>
        </div>
      </div>
      <div class="acct-uid">UID: ${esc(shortUid(a.uid))}</div>
      <div class="acct-mid">
        ${(() => {
          if (isTokAcct) {
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
            if (isTokAcct) return `<span class="tag ratio-tag" title="官方按 Token 计量，1 积分 = 1 Token">${I.coins} 1分 = 1 Token</span>`;
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
      <div class="acct-cooling-reason${a.cooling ? "" : " hidden"}">${esc(a.reason || "")}</div>
      <div class="acct-bot">
        <span class="status-dot ${a.cooling ? "cooling" : "ok"}"></span>
        <span class="status-text">${a.cooling ? "冷却中 · " + esc(a.until || "") : "就绪"}</span>
      </div>
    </div>`;
  }

  function renderAccounts(accounts) {
    const wrap = $("acctGroups");
    if (!wrap) return;

    if (!accounts || accounts.length === 0) {
      wrap.innerHTML = `<div class="empty-hint">暂无账号，请点击右上角「+ 添加账号」接入</div>`;
      return;
    }

    const byMode = { proxy: {}, bridge: {}, direct: {} };
    for (const a of accounts) {
      const m = (a.mode && byMode[a.mode]) ? a.mode : "proxy";
      const g = a.group || "unknown";
      if (!byMode[m][g]) byMode[m][g] = [];
      byMode[m][g].push(a);
    }

    let html = "";
    for (const sec of MODE_SECTIONS) {
      const modeGroups = byMode[sec.key] || {};
      const chKeys = Object.keys(modeGroups);
      if (chKeys.length === 0) continue;

      let modeTotal = 0;
      for (const k of chKeys) modeTotal += modeGroups[k].length;

      html += `<div class="mode-section" data-mode="${sec.key}">
        <div class="mode-section-head">
          <div class="mode-section-title">
            <span class="mode-badge ${sec.key}">${sec.name}</span>
            <span class="mode-count">${modeTotal} 个账号</span>
          </div>
          <div class="mode-section-desc">${sec.desc}</div>
        </div>`;

      chKeys.sort((a, b) => {
        const ia = CHANNEL_ORDER.indexOf(a);
        const ib = CHANNEL_ORDER.indexOf(b);
        if (ia === -1 && ib === -1) return a.localeCompare(b);
        if (ia === -1) return 1;
        if (ib === -1) return -1;
        return ia - ib;
      });

      for (const g of chKeys) {
        const list = modeGroups[g];
        const gm = groupMeta(g);
        const gkey = `${sec.key}:${g}`;
        const isOpen = getGroupOpen(gkey, true);

        html += `<details class="acct-group" data-gkey="${gkey}" ${isOpen ? "open" : ""}>
          <summary class="acct-group-head">
            <span class="acct-group-title">
              <span class="badge ${gm.cls}">${gm.name}</span>
              <span class="acct-group-count">(${list.length})</span>
            </span>
            <span class="acct-group-arrow">${I.chevron}</span>
          </summary>
          <div class="acct-cards">${list.map(acctCardHTML).join("")}</div>
        </details>`;
      }
      html += `</div>`;
    }
    wrap.innerHTML = html;

    wrap.querySelectorAll("details.acct-group").forEach(d => {
      d.addEventListener("toggle", () => saveGroupOpen(d.dataset.gkey, d.open));
    });
  }

  // 4. 账号基础操作
  async function checkin(uid, e) {
    const ev = e || window.event;
    const target = ev ? (ev.currentTarget || (ev.target && ev.target.closest(".icon-op")) || ev.target) : null;
    if (target && target.classList.contains("is-loading")) return;
    setElLoading(target, true);
    try {
      const r = await api("/api/account/checkin", { uid });
      toast(r.ok ? `签到成功：${r.msg}（剩余 ${r.remain}）` : `签到：${r.msg}`);
      if (window.loadState) window.loadState();
    } catch (err) {
      toast(err.message);
    } finally {
      setElLoading(target, false);
    }
  }

  async function refreshOne(uid, e) {
    const ev = e || window.event;
    const target = ev ? (ev.currentTarget || (ev.target && ev.target.closest(".icon-op")) || ev.target) : null;
    if (target && target.classList.contains("is-loading")) return;
    setElLoading(target, true);
    try {
      const r = await api("/api/account/refresh", { uid });
      toast(`刷新成功：剩余积分 ${r.remain}`);
      if (window.loadState) window.loadState();
    } catch (err) {
      toast(err.message);
    } finally {
      setElLoading(target, false);
    }
  }

  async function growth(uid, e) {
    const ev = e || window.event;
    const target = ev ? (ev.currentTarget || (ev.target && ev.target.closest(".icon-op")) || ev.target) : null;
    if (target && target.classList.contains("is-loading")) return;
    setElLoading(target, true);
    try {
      const r = await api("/api/account/growth", { uid });
      const parts = [];
      if (r.travel) parts.push("旅行：" + r.travel);
      if (r.lottery) parts.push("盲盒：" + r.lottery);
      if (r.tasks) parts.push("任务：" + r.tasks);
      toast(parts.join(" · ") || "成长中心执行完成");
      if (window.loadState) window.loadState();
    } catch (err) {
      toast(err.message);
    } finally {
      setElLoading(target, false);
    }
  }

  async function toggleDisable(uid, currentlyDisabled) {
    const action = currentlyDisabled ? "启用" : "停用";
    confirmDialog(`确定${action}该账号？${currentlyDisabled ? "" : "停用后路由不会分配给该账号。"}`, async () => {
      try {
        await api("/api/account/disable", { uid, disabled: !currentlyDisabled });
        toast(`账号已${action}`);
        if (window.loadState) window.loadState();
      } catch (e) { toast(e.message); }
    });
  }

  function removeAcct(uid) {
    confirmDialog("确定删除该账号？删除后需重新登录。", async () => {
      try {
        await api("/api/account/remove", { uid });
        toast("账号已删除");
        if (window.loadState) window.loadState();
      } catch (e) { toast(e.message); }
    });
  }

  // 5. 添加账号下拉菜单
  const ADD_CHANNELS = [
    { key: "workbuddy", name: "WorkBuddy (国内版)", desc: "扫码登录，个人版免 token", hot: true, action: "login", group: "wb" },
    { key: "codebuddy", name: "CodeBuddy (国际版)", desc: "粘贴 accessToken，按倍率计费", action: "wbt", group: "cb" },
    { key: "traework",  name: "Trae 国际版",        desc: "字节旗下 AI IDE，扫码登录", hot: true, action: "login", group: "trae" },
    { key: "qoder",     name: "Qoder (腾讯)",       desc: "微信扫码登录，每日有送分", hot: true, action: "login", group: "qoder" },
    { key: "autoclaw",  name: "AutoClaw",           desc: "短信验证码登录，支持 GLM-5 系列", action: "autoclaw", group: "ac" },
    { key: "atria",     name: "Atria 官方 Key",     desc: "直连 api.atria-asi.ai，不走桥", action: "atria", group: "atria" },
  ];

  function renderAddMenu() {
    const el = $("addMenu");
    if (!el) return;
    el.innerHTML = ADD_CHANNELS.map(c => `
      <div class="add-menu-item" onclick="triggerAdd('${c.key}','${c.action}')">
        <span class="badge ${c.group}">${c.name}</span>
        <div class="add-menu-info">
          <div class="add-menu-name">${c.name} ${c.hot ? '<span class="tag next-pick" style="font-size:10px;padding:1px 5px;">推荐</span>' : ""}</div>
          <div class="add-menu-desc">${c.desc}</div>
        </div>
      </div>
    `).join("");
  }

  function toggleAddMenu(e) {
    if (e) e.stopPropagation();
    const m = $("addMenu");
    if (m) m.classList.toggle("open");
  }

  function triggerAdd(channel, action) {
    const m = $("addMenu");
    if (m) m.classList.remove("open");
    if (action === "login") openLogin(channel);
    else if (action === "wbt") openWbtDialog(channel);
    else if (action === "autoclaw") openAutoclawDialog();
    else if (action === "atria") openAtriaDialog();
  }

  // 全局导出挂载
  window.showCreditDetail = showCreditDetail;
  window.hideCreditDetail = hideCreditDetail;
  window.closeCreditTip = closeCreditTip;
  window.acctCardHTML = acctCardHTML;
  window.renderAccounts = renderAccounts;
  window.checkin = checkin;
  window.refreshOne = refreshOne;
  window.growth = growth;
  window.toggleDisable = toggleDisable;
  window.removeAcct = removeAcct;
  window.renderAddMenu = renderAddMenu;
  window.toggleAddMenu = toggleAddMenu;
  window.triggerAdd = triggerAdd;
})();
