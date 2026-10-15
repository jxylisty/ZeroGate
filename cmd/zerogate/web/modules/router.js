// ============================================================
// ZeroGate WebUI - Router & Services Module (客户端 Key、模型路由、渠道模型与本地桥)
// ============================================================

(function () {
  "use strict";

  // ============================================================
  // 1. 客户端 Key 管理
  // ============================================================

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

  async function toggleClient(key, enabled) {
    try {
      await api("/api/clients/toggle", { key, enabled });
      toast(enabled ? "已启用" : "已停用");
    } catch (e) {
      toast("操作失败：" + e.message);
      loadClients();
    }
  }

  function deleteClient(key) {
    confirmDialog("确定删除该客户端 Key？删除后使用该 Key 的客户端将无法调用", async () => {
      try {
        await api("/api/clients/delete", { key });
        toast("已删除");
        loadClients();
      } catch (e) {
        toast("删除失败：" + e.message);
      }
    });
  }

  async function createClient() {
    const input = $("inClientName");
    if (!input) return;
    const name = input.value.trim();
    if (!name) {
      toast("请输入客户端标签（如 Cursor）");
      return;
    }
    try {
      const k = await api("/api/clients/create", { name });
      input.value = "";
      toast("客户端 Key 创建成功");
      copyText(k.key, "新生成的 Key");
      loadClients();
    } catch (e) {
      toast("创建失败：" + e.message);
    }
  }

  // ============================================================
  // 2. 模型路由别名与重定向规则管理
  // ============================================================

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

  function editRedirect(name) {
    const cands = currentRedirects[name] || [];
    if ($("inAliasName")) $("inAliasName").value = name;
    if ($("inAliasTargets")) $("inAliasTargets").value = cands.join(", ");
    if ($("inAliasName")) $("inAliasName").focus();
  }

  async function deleteRedirect(name) {
    delete currentRedirects[name];
    try {
      await api("/api/router/redirects", { redirects: currentRedirects });
      toast("规则已删除");
      renderRedirects();
    } catch (e) {
      toast("保存失败：" + e.message);
    }
  }

  async function saveRedirect() {
    const inName = $("inAliasName");
    const inTargets = $("inAliasTargets");
    if (!inName || !inTargets) return;
    const name = inName.value.trim();
    const rawTargets = inTargets.value.trim();
    if (!name || !rawTargets) {
      toast("请填写标准模型名和候选渠道");
      return;
    }
    const targets = rawTargets.split(",").map(s => s.trim()).filter(Boolean);
    currentRedirects[name] = targets;
    try {
      await api("/api/router/redirects", { redirects: currentRedirects });
      toast("规则已保存");
      inName.value = "";
      inTargets.value = "";
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

  // ============================================================
  // 3. 渠道模型与费率
  // ============================================================

  const feeThemes = {
    traework:  ["#059669", "#10b981"],
    codebuddy: ["#4f46e5", "#6366f1"],
    autoclaw:  ["#7c3aed", "#8b5cf6"],
    qoder:     ["#d97706", "#f59e0b"],
    zcode:     ["#0891b2", "#06b6d4"],
    atria:     ["#0d9488", "#14b8a6"],
    glmflash:  ["#65a30d", "#a3e635"],
    groq:      ["#f59e0b", "#fbbf24"],
    workbuddy: ["#2563eb", "#3b82f6"],
  };

  async function loadFees() {
    try {
      const fees = await api("/api/fees");
      renderFees(fees);
    } catch (e) { /* 费率接口失败不阻塞 */ }
  }

  async function refreshFees() {
    const btn = $("btnRefreshFees");
    if (btn) btn.disabled = true;
    try {
      await api("/api/fees/refresh", {});
      const fees = await api("/api/fees");
      renderFees(fees);
      toast("费率已刷新");
    } catch (e) {
      toast(e.message);
    } finally {
      if (btn) btn.disabled = false;
    }
  }

  function copyId(id) {
    copyText(id, "模型 ID");
  }

  function renderFees(fees) {
    const box = $("feesBox");
    if (!box) return;
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
        if (!m.model || m.model.includes("/")) continue;
        const rate = m.rate ? Number(m.rate) : 0;
        let badgeClass = "free";
        if (rate > 0 && rate <= 0.15) badgeClass = "low";
        else if (rate > 0.15 && rate <= 0.5) badgeClass = "normal";
        else if (rate > 0.5) badgeClass = "high";

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

  // ============================================================
  // 4. 本地桥管理（模型桥 + 应用服务 + 自动化 + 外部托管）
  // ============================================================

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
    const autoTag = st.autostart
      ? `<span class="tag blue" title="ZeroGate 启动时自动拉起（隐藏窗口，无 cmd 弹窗）">随网关自启</span>` : "";
    const detail = esc(st.detail || st.error || "");
    const lastAct = st.last_action ? `<div class="br-last">最近操作：${esc(st.last_action)} · ${esc(st.last_action_at || "")}</div>` : "";
    const startBtn = (st.online || st.running) ? "" :
      `<button class="btn small primary" onclick="bridgeStart(this, '${esc(st.id)}')">启动</button>`;
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
    if (!btn) return;
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

  // 挂载全局方法
  window.loadClients = loadClients;
  window.toggleClient = toggleClient;
  window.deleteClient = deleteClient;
  window.createClient = createClient;

  window.loadRedirects = loadRedirects;
  window.renderRedirects = renderRedirects;
  window.editRedirect = editRedirect;
  window.deleteRedirect = deleteRedirect;
  window.saveRedirect = saveRedirect;
  window.resetRedirects = resetRedirects;

  window.feeThemes = feeThemes;
  window.loadFees = loadFees;
  window.refreshFees = refreshFees;
  window.renderFees = renderFees;
  window.copyId = copyId;

  window.BRIDGE_GROUPS = BRIDGE_GROUPS;
  window.bridgeDot = bridgeDot;
  window.bridgeCardHTML = bridgeCardHTML;
  window.renderBridges = renderBridges;
  window.loadBridges = loadBridges;
  window.bridgeStart = bridgeStart;
  window.bridgeStop = bridgeStop;
  window.bridgeStartAll = bridgeStartAll;

})();
