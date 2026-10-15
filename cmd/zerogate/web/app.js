// ============================================================
// ZeroGate WebUI - Application Entry & Orchestrator (app.js)
// 现代化架构中枢：零外部打包依赖，负责状态同步、Tab导航、响应式抽屉与启动装配
// ============================================================

"use strict";

(function () {
  // ---------- 全局状态与大屏标记 ----------
  let state = null;
  window.state = state;

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

    if (typeof window.renderAccounts === "function") window.renderAccounts();
    if (typeof window.renderTimes === "function") window.renderTimes();
    if (typeof window.renderGrowthTimes === "function") window.renderGrowthTimes();
    if (typeof window.renderAddMenu === "function") window.renderAddMenu();

    if ($("chkAutostart")) $("chkAutostart").checked = !!state.autostart;
  }
  window.render = render;

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
        if (typeof window.loadTokenAnalytics === "function") window.loadTokenAnalytics();
        if (typeof window.loadAssets === "function") window.loadAssets();
      }
      else if (name === "logs") {
        if (typeof window.loadAuditLogs === "function") window.loadAuditLogs();
      }
      else if (name === "clients") {
        if (typeof window.loadClients === "function") window.loadClients();
      }
      else if (name === "router") {
        if (typeof window.loadRedirects === "function") window.loadRedirects();
      }
      else if (name === "growth") {
        if (typeof window.initGrowthTab === "function") window.initGrowthTab();
      }
      else if (name === "bridges") {
        if (typeof window.loadBridges === "function") window.loadBridges();
      }
      else if (name === "models") {
        if (typeof window.loadFees === "function") window.loadFees();
      }
      else if (name === "settings") {
        if (typeof window.loadSettingsView === "function") window.loadSettingsView();
      }
    }

    tabs.forEach(t => t.addEventListener("click", () => switchTab(t.dataset.tab)));
    dockBtns.forEach(b => b.addEventListener("click", () => switchTab(b.dataset.tab)));
    sidebarBtns.forEach(b => b.addEventListener("click", () => switchTab(b.dataset.tab)));

    switchTabFn = switchTab;
    window.switchTabFn = switchTab;
    switchTab(savedTab);

    if (typeof window.initWallpaperSystem === "function") window.initWallpaperSystem();
    if (typeof window.initProtocolPills === "function") window.initProtocolPills();
    initSidebar();
    initMobileDrawer();
    initLayoutMode();
  }
  window.initTabs = initTabs;

  // ---------- 移动端抽屉与侧栏折叠 ----------
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

  // ---------- 全局弹窗键盘与焦点交互增强 (Esc关闭/Enter提交/背景点击) ----------
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
    // 账号添加下拉菜单
    if ($("btnAddAcct")) $("btnAddAcct").onclick = (e) => { e.stopPropagation(); if (typeof window.toggleAddMenu === "function") window.toggleAddMenu(); };
    if ($("addAcctMenu")) {
      $("addAcctMenu").addEventListener("click", (e) => {
        const item = e.target.closest(".dropdown-item");
        if (!item) return;
        if (typeof window.toggleAddMenu === "function") window.toggleAddMenu(false);
        if (typeof window.promptLogin === "function") window.promptLogin(item.dataset.channel);
      });
    }
    document.addEventListener("click", (e) => {
      if (!e.target.closest("#addAcctDrop") && typeof window.toggleAddMenu === "function") window.toggleAddMenu(false);
    });

    // 账号搜索与过滤
    if ($("acctSearch")) {
      $("acctSearch").addEventListener("input", (e) => {
        if (typeof window.setAcctSearch === "function") window.setAcctSearch(e.target.value.trim().toLowerCase());
        if (typeof window.renderAccounts === "function") window.renderAccounts();
      });
    }

    document.querySelectorAll("#acctFilter .chip").forEach(c => {
      c.addEventListener("click", () => {
        if (typeof window.setAcctFilter === "function") window.setAcctFilter(c.dataset.filter);
        document.querySelectorAll("#acctFilter .chip").forEach(x => x.classList.toggle("active", x === c));
        if (typeof window.renderAccounts === "function") window.renderAccounts();
      });
    });

    if ($("btnExpandAll")) $("btnExpandAll").onclick = () => { if (typeof window.setAllGroups === "function") window.setAllGroups(true); };
    if ($("btnCollapseAll")) $("btnCollapseAll").onclick = () => { if (typeof window.setAllGroups === "function") window.setAllGroups(false); };
    if ($("btnCheckinAll")) $("btnCheckinAll").onclick = () => { if (typeof window.openCheckinDialog === "function") window.openCheckinDialog(); };
    if ($("btnRefreshAll")) $("btnRefreshAll").onclick = () => { if (typeof window.refreshAll === "function") window.refreshAll(); };
    if ($("btnAddTime")) $("btnAddTime").onclick = () => { if (typeof window.addTime === "function") window.addTime(); };

    // 批量签到弹层
    if ($("btnCiStart")) $("btnCiStart").onclick = () => { if (typeof window.runBatchCheckin === "function") window.runBatchCheckin(); };
    if ($("btnCiClose")) $("btnCiClose").onclick = () => { if (typeof window.closeCheckinDialog === "function") window.closeCheckinDialog(); };
    if ($("ciSelAll")) $("ciSelAll").onclick = () => { if (typeof window.ciSetAll === "function") window.ciSetAll(true); };
    if ($("ciSelNone")) $("ciSelNone").onclick = () => { if (typeof window.ciSetAll === "function") window.ciSetAll(false); };
    if ($("ciInvert")) $("ciInvert").onclick = () => { if (typeof window.ciInvertSel === "function") window.ciInvertSel(); };
    if ($("ciOnlyUnchecked")) $("ciOnlyUnchecked").onclick = () => { if (typeof window.ciOnlyUnchecked === "function") window.ciOnlyUnchecked(); };
    if ($("ciSkipExpired")) $("ciSkipExpired").onclick = () => { if (typeof window.ciSkipExpired === "function") window.ciSkipExpired(); };

    // 豆包保活弹层
    if ($("btnDoubao")) $("btnDoubao").onclick = () => { if (typeof window.openDoubaoDialog === "function") window.openDoubaoDialog(); };
    if ($("btnDbRenew")) $("btnDbRenew").onclick = () => { if (typeof window.runDoubaoRenew === "function") window.runDoubaoRenew(); };
    if ($("btnDbClose")) $("btnDbClose").onclick = () => { if (typeof window.closeDoubaoDialog === "function") window.closeDoubaoDialog(); };
    if ($("btnCopyUrl")) $("btnCopyUrl").onclick = () => { if (typeof window.copyUrl === "function") window.copyUrl(); };
    if ($("btnCancelLogin")) $("btnCancelLogin").onclick = () => { if (typeof window.cancelLogin === "function") window.cancelLogin(); };
    if ($("btnRefreshFees") && typeof window.refreshFees === "function") $("btnRefreshFees").onclick = window.refreshFees;
    if ($("chkAutostart")) $("chkAutostart").onchange = () => { if (typeof window.toggleAutostart === "function") window.toggleAutostart(); };

    // 成长中心与签到
    if ($("btnAddGrowthTime") && typeof window.addGrowthTime === "function") $("btnAddGrowthTime").onclick = window.addGrowthTime;
    if ($("btnSaveGrowthTimes") && typeof window.saveGrowthTimes === "function") $("btnSaveGrowthTimes").onclick = () => window.saveGrowthTimes((state.growth_times || []).slice());
    if ($("btnRunGrowthNow") && typeof window.runGrowthNow === "function") $("btnRunGrowthNow").onclick = window.runGrowthNow;
    if ($("btnRefreshGrowthToday") && typeof window.loadGrowthToday === "function") $("btnRefreshGrowthToday").onclick = window.loadGrowthToday;
    if ($("btnRefreshGrowthAccounts") && typeof window.loadGrowthAccounts === "function") $("btnRefreshGrowthAccounts").onclick = window.loadGrowthAccounts;
    if ($("btnRefreshGrowthRecords") && typeof window.loadGrowthRecords === "function") $("btnRefreshGrowthRecords").onclick = window.loadGrowthRecords;
    if ($("selGrowthLimit") && typeof window.loadGrowthRecords === "function") $("selGrowthLimit").onchange = window.loadGrowthRecords;
    if ($("btnGrowthDetailClose") && typeof window.closeGrowthDetail === "function") $("btnGrowthDetailClose").onclick = window.closeGrowthDetail;
    if ($("growthDetailOverlay") && typeof window.closeGrowthDetail === "function") $("growthDetailOverlay").onclick = (e) => { if (e.target === $("growthDetailOverlay")) window.closeGrowthDetail(); };

    if ($("btnGrowthBatchCheckin")) $("btnGrowthBatchCheckin").onclick = () => { if (typeof window.openCheckinDialog === "function") window.openCheckinDialog(); };
    if ($("btnClaimRefresh") && typeof window.loadClaimStatus === "function") $("btnClaimRefresh").onclick = window.loadClaimStatus;
    if ($("btnClaimNow") && typeof window.runClaimNow === "function") $("btnClaimNow").onclick = window.runClaimNow;

    // 本地桥
    if ($("btnBridgeRefresh") && typeof window.loadBridges === "function") $("btnBridgeRefresh").onclick = window.loadBridges;
    if ($("btnBridgeStartAll") && typeof window.bridgeStartAll === "function") $("btnBridgeStartAll").onclick = window.bridgeStartAll;

    // Atria Key 管理
    if ($("btnAtriaAdd") && typeof window.addAtriaKey === "function") $("btnAtriaAdd").onclick = window.addAtriaKey;
    if ($("btnAtriaBatch") && typeof window.addAtriaBatch === "function") $("btnAtriaBatch").onclick = window.addAtriaBatch;
    if ($("btnAtriaClose") && typeof window.closeAtriaDialog === "function") $("btnAtriaClose").onclick = window.closeAtriaDialog;
    if ($("atriaOverlay") && typeof window.closeAtriaDialog === "function") $("atriaOverlay").onclick = (e) => { if (e.target === $("atriaOverlay")) window.closeAtriaDialog(); };
    if ($("atriaKey") && typeof window.addAtriaKey === "function") $("atriaKey").addEventListener("keydown", (e) => { if (e.key === "Enter") window.addAtriaKey(); });

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
    if ($("btnEditApiConfig")) $("btnEditApiConfig").onclick = (e) => { e.stopPropagation(); if (typeof window.openApiConfig === "function") window.openApiConfig(); };
    if ($("btnEditApiKey")) $("btnEditApiKey").onclick = (e) => { e.stopPropagation(); if (typeof window.openApiKey === "function") window.openApiKey(); };

    if ($("btnHelp")) $("btnHelp").onclick = () => { if (typeof window.openHelp === "function") window.openHelp(); };
    if ($("btnAbout")) $("btnAbout").onclick = () => { if (typeof window.openAbout === "function") window.openAbout(); };
    if ($("btnHelpClose")) $("btnHelpClose").onclick = () => { if (typeof window.closeHelp === "function") window.closeHelp(); };
    if ($("btnAboutClose")) $("btnAboutClose").onclick = () => { if (typeof window.closeAbout === "function") window.closeAbout(); };

    // 登录确认与认证通道选择
    if ($("btnLoginConfirm")) $("btnLoginConfirm").onclick = () => { if (typeof window.confirmLogin === "function") window.confirmLogin(); };
    if ($("btnLoginConfirmCancel")) $("btnLoginConfirmCancel").onclick = () => $("loginConfirmOverlay").classList.add("hidden");
    if ($("btnZcZai")) $("btnZcZai").onclick = () => { if (typeof window.pickZcodeProvider === "function") window.pickZcodeProvider($("btnZcZai")); };
    if ($("btnZcBigmodel")) $("btnZcBigmodel").onclick = () => { if (typeof window.pickZcodeProvider === "function") window.pickZcodeProvider($("btnZcBigmodel")); };
    if ($("aclSend")) $("aclSend").onclick = () => { if (typeof window.aclSendCode === "function") void window.aclSendCode(); };
    if ($("aclConfirm")) $("aclConfirm").onclick = () => { if (typeof window.aclConfirm === "function") void window.aclConfirm(); };
    if ($("aclCancel")) $("aclCancel").onclick = () => { if (typeof window.closeAutoclawDialog === "function") window.closeAutoclawDialog(); };
    if ($("wbtCancel")) $("wbtCancel").onclick = () => { if (typeof window.closeWbtDialog === "function") window.closeWbtDialog(); };
    if ($("wbtConfirm")) $("wbtConfirm").onclick = () => { if (typeof window.wbtImport === "function") void window.wbtImport(); };
    if ($("loginConfirmOverlay")) $("loginConfirmOverlay").onclick = (e) => { if (e.target === $("loginConfirmOverlay")) $("loginConfirmOverlay").classList.add("hidden"); };

    if ($("btnApiSave")) $("btnApiSave").onclick = () => { if (typeof window.saveApiConfig === "function") window.saveApiConfig(); };
    if ($("btnApiCancel")) $("btnApiCancel").onclick = () => { if (typeof window.closeApiConfig === "function") window.closeApiConfig(); };
    if ($("selHost")) $("selHost").onchange = () => {
      if ($("customHostRow")) $("customHostRow").classList.toggle("hidden", $("selHost").value !== "__custom__");
    };

    if ($("btnKeySave")) $("btnKeySave").onclick = () => { if (typeof window.saveApiKey === "function") window.saveApiKey(); };
    if ($("btnKeyCancel")) $("btnKeyCancel").onclick = () => { if (typeof window.closeApiKey === "function") window.closeApiKey(); };
    if ($("keyInput")) $("keyInput").addEventListener("keydown", (e) => { if (e.key === "Enter" && typeof window.saveApiKey === "function") window.saveApiKey(); });

    // 路由与客户端 Tab 功能绑定
    if (typeof window.initAuditLogsEvents === "function") window.initAuditLogsEvents();
    if ($("btnCreateClient") && typeof window.createClient === "function") $("btnCreateClient").onclick = window.createClient;
    if ($("inClientName") && typeof window.createClient === "function") $("inClientName").addEventListener("keydown", (e) => { if (e.key === "Enter") window.createClient(); });
    if ($("btnAddRedirect") && typeof window.saveRedirect === "function") $("btnAddRedirect").onclick = window.saveRedirect;
    if ($("btnResetRedirects") && typeof window.resetRedirects === "function") $("btnResetRedirects").onclick = window.resetRedirects;
    if ($("btnRefreshTokens") && typeof window.loadTokenAnalytics === "function") $("btnRefreshTokens").onclick = window.loadTokenAnalytics;
    if ($("btnRefreshAssets") && typeof window.loadAssets === "function") $("btnRefreshAssets").onclick = window.loadAssets;

    initModalEnhancements();
  }

  // ---------- 应用程序统一启动入口 ----------
  async function init() {
    bind();
    initTabs();
    await loadState();
    if (typeof window.loadFees === "function") window.loadFees();
    if (typeof window.loadDashboard === "function") window.loadDashboard();
    if (typeof window.setupAuditLogsPolling === "function") window.setupAuditLogsPolling();
  }

  document.addEventListener("DOMContentLoaded", init);
})();