// ZeroGate — 实时调用审计流水模块 (modules/audit.js)
// 包含调用日志环形缓冲区、智能滚动跟随、错误一键复制、AI诊断报告及详情抽屉
(function () {
  "use strict";

  const MAX_LOGS = 250; // 固定环形缓冲区最大容量，挂机防内存膨胀
  let allAuditLogs = [];
  let auditTypeFilter = "all"; // "all" | "error"
  let auditLogsTimer = null;
  let currentDetailLog = null;
  let isUserScrolling = false;

  // 1. 积分与消耗计算提取
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

  // 2. 消耗单元格渲染
  function renderLogCostCell(l) {
    const meta = window.CHANNEL_METAS ? window.CHANNEL_METAS[l.channel] : null;
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

  // 3. 模型与路由单元格渲染
  function renderLogModelCell(l) {
    const chanMeta = groupMeta(l.channel);
    const chanBadge = `<span class="badge ${chanMeta.cls}" style="margin-right:3px;">${esc(l.channel || "未知")}</span>`;
    const targetModel = esc(l.model_target || l.model_req || "-");
    const modelCode = `<span class="audit-model-code">${targetModel}</span>`;
    const streamTag = l.stream ? `<span class="tag neutral" style="font-size:9px; padding:1px 4px; margin-left:2px;">流</span>` : "";
    const uidBadge = l.uid ? `<span class="audit-uid-badge" title="${esc(l.uid)}">${esc(shortUid(l.uid))}</span>` : "";

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

  // 4. 状态与异常单元格渲染
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

  // 5. 渲染表格（带智能滚动记忆）
  function renderAuditLogsTable() {
    const tbody = $("auditLogsBody");
    if (!tbody) return;

    // 智能滚动记忆：记录渲染前的滚动位置
    const scrollContainer = tbody.closest(".table-responsive, .table-wrap") || tbody.parentElement;
    const prevScrollTop = scrollContainer ? scrollContainer.scrollTop : 0;
    const isNearTop = prevScrollTop <= 35;

    // 绑定滚动监听（防重复绑定）
    if (scrollContainer && !scrollContainer.dataset.scrollBound) {
      scrollContainer.dataset.scrollBound = "true";
      scrollContainer.addEventListener("scroll", () => {
        isUserScrolling = scrollContainer.scrollTop > 40;
      }, { passive: true });
    }

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
      tbody.innerHTML = `<tr><td colspan="7" style="text-align:center; color:var(--muted); padding:30px;"><div class="empty-hint" style="margin:10px auto; max-width:280px;">${I.scroll || ""}<span>未发现符合条件的调用记录</span></div></td></tr>`;
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

    // 智能滚动恢复：若在顶部则保持吸顶，若在历史区则保持用户滚动位置
    if (scrollContainer) {
      if (isNearTop) {
        scrollContainer.scrollTop = 0;
      } else {
        scrollContainer.scrollTop = prevScrollTop;
      }
    }
  }

  // 6. 下拉选项动态更新（带防失焦保护）
  function updateAuditChannelSelect() {
    const sel = $("selAuditChannel");
    if (!sel) return;
    // 防失焦保护：如果当前下拉框正在聚焦编辑，不暴力重构
    if (document.activeElement === sel) return;

    const curVal = sel.value;
    const channels = new Set();
    allAuditLogs.forEach(l => { if (l.channel) channels.add(l.channel); });
    Object.keys(window.CHANNEL_METAS || {}).forEach(c => channels.add(c));

    const sorted = Array.from(channels).sort();
    sel.innerHTML = `<option value="">全部渠道</option>` + sorted.map(c => {
      const name = (window.CHANNEL_METAS && window.CHANNEL_METAS[c] && window.CHANNEL_METAS[c].name) || c;
      const selAttr = c === curVal ? " selected" : "";
      return `<option value="${esc(c)}"${selAttr}>${esc(name)}</option>`;
    }).join("");
  }

  let lastLogsSignature = "";

  // 7. 拉取流水数据（带固定环形缓冲区与防抖拦截）
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
      let fetched = data.logs || [];
      // 环形缓冲区限制
      if (fetched.length > MAX_LOGS) {
        fetched = fetched.slice(0, MAX_LOGS);
      }

      // 生成特征指纹，比对是否有数据变化
      const curSignature = fetched.length ? `${fetched.length}:${fetched[0].id || ""}:${fetched[0].status || ""}` : "empty";
      const hasChanged = curSignature !== lastLogsSignature;

      // 文本选择保护：如果用户正用鼠标选中表格内的文字（准备复制排查），不强行刷新打断选区
      const selection = window.getSelection();
      const isSelectingInTable = selection && selection.toString().trim().length > 0 &&
        $("auditLogsBody") && $("auditLogsBody").contains(selection.anchorNode);

      if (!isManual && !hasChanged) {
        // 无新记录，跳过重绘
        return;
      }
      if (!isManual && isSelectingInTable) {
        // 用户正在选中文本复制，暂缓本次重绘
        return;
      }

      lastLogsSignature = curSignature;
      allAuditLogs = fetched;

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
        }, 300);
      }
    }
  }

  // 8. 错误复制与报告生成
  function copyAuditErrorById(id, evt) {
    if (evt) evt.stopPropagation();
    const item = allAuditLogs.find(l => l.id === id);
    if (!item) { toast("未找到该条记录"); return; }
    const errContent = item.error || `HTTP ${item.status} 错误`;
    copyText(errContent, "报错信息");
  }

  function buildAuditReport(l) {
    const time = l.time ? l.time.replace("T", " ").replace(/\+.*/, "") : "-";
    const errBody = l.error || "(无具体上游报错体，返回状态码 " + l.status + ")";
    const meta = window.CHANNEL_METAS ? window.CHANNEL_METAS[l.channel] : null;
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

  // 9. 详情抽屉
  function openAuditDetail(id) {
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
    const meta = window.CHANNEL_METAS ? window.CHANNEL_METAS[item.channel] : null;
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
  }

  function closeAuditDetail() {
    const overlay = $("auditDetailOverlay");
    if (overlay) overlay.classList.add("hidden");
    currentDetailLog = null;
  }

  // 10. 事件与轮询管理
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

  // 全局暴露供 HTML 与其他模块调用
  window.loadAuditLogs = loadAuditLogs;
  window.renderAuditLogsTable = renderAuditLogsTable;
  window.initAuditLogsEvents = initAuditLogsEvents;
  window.setupAuditLogsPolling = setupAuditLogsPolling;
  window.openAuditDetail = openAuditDetail;
  window.closeAuditDetail = closeAuditDetail;
  window.copyAuditErrorById = copyAuditErrorById;
  window.copyLatestError = copyLatestError;
})();
