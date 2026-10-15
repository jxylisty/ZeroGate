// ============================================================
// ZeroGate WebUI - 系统设置与壁纸个性化模块 (modules/settings.js)
// 包含壁纸切换/遮罩模糊度调节、端点复制药丸、布局切换、系统配置与帮助弹层
// ============================================================

"use strict";

(function () {
  // ---------- 定时签到时间渲染 ----------
  function renderTimes() {
    const box = $("checkinTimesBox");
    if (!box) return;
    const times = (window.state && window.state.checkin_times) || [];
    box.innerHTML = times.map(t =>
      `<span class="time-chip" title="点击删除" onclick="delTime('${t}')">${t}${I.x}</span>`).join("");
    if (!times.length) box.innerHTML = `<span class="tag neutral">暂无配置</span>`;
    if ($("nextCheckin")) $("nextCheckin").textContent = (window.state && window.state.next_checkin) || "-";
  }

  function delTime(t) {
    const times = ((window.state && window.state.checkin_times) || []).filter(x => x !== t);
    saveTimes(times);
  }

  function addTime() {
    const times = (((window.state && window.state.checkin_times) || []).slice());
    const now = new Date();
    const next = `${String(now.getHours()).padStart(2, "0")}:${String(now.getMinutes()).padStart(2, "0")}`;
    if (!times.includes(next)) times.push(next);
    saveTimes(times.sort());
  }

  async function saveTimes(times) {
    try {
      await api("/api/config/checkin_times", { times });
      toast("签到时间已更新");
      if (typeof window.loadState === "function") window.loadState();
    } catch (e) { toast(e.message); }
  }

  // ---------- 开机自启 ----------
  async function toggleAutostart() {
    try {
      await api("/api/config/autostart", { on: $("chkAutostart").checked });
      toast("开机自启设置已保存");
    } catch (e) {
      toast(e.message);
      if (typeof window.loadState === "function") window.loadState();
    }
  }

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

  function openHelp() { if ($("helpOverlay")) $("helpOverlay").classList.remove("hidden"); }
  function closeHelp() { if ($("helpOverlay")) $("helpOverlay").classList.add("hidden"); }
  function openAbout() { if ($("aboutOverlay")) $("aboutOverlay").classList.remove("hidden"); }
  function closeAbout() { if ($("aboutOverlay")) $("aboutOverlay").classList.add("hidden"); }

  // ---------- API 监听与 Key 配置弹层 ----------
  function openApiConfig() {
    const state = window.state;
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
      if (typeof window.loadState === "function") window.loadState();
    } catch (e) { toast(e.message); }
  }

  function openApiKey() {
    const state = window.state;
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
      if (typeof window.loadState === "function") window.loadState();
    } catch (e) { toast(e.message); }
  }

  // ---------- 系统设置视图 ----------
  function loadSettingsView() {
    const state = window.state;
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
          if (typeof window.loadState === "function") await window.loadState();
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
          if (typeof window.loadState === "function") await window.loadState();
        } catch (e) { toast(e.message); }
      };
    }

    if ($("btnRefreshFeesCache")) {
      $("btnRefreshFeesCache").onclick = async () => {
        if (typeof window.loadFees === "function") await window.loadFees();
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

  // ---------- 协议端点药丸复制 ----------
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

  // ---------- 导出到全局命名空间 ----------
  window.renderTimes = renderTimes;
  window.delTime = delTime;
  window.addTime = addTime;
  window.saveTimes = saveTimes;
  window.toggleAutostart = toggleAutostart;
  window.confirmDialog = confirmDialog;
  window.openHelp = openHelp;
  window.closeHelp = closeHelp;
  window.openAbout = openAbout;
  window.closeAbout = closeAbout;
  window.openApiConfig = openApiConfig;
  window.closeApiConfig = closeApiConfig;
  window.saveApiConfig = saveApiConfig;
  window.openApiKey = openApiKey;
  window.closeApiKey = closeApiKey;
  window.saveApiKey = saveApiKey;
  window.loadSettingsView = loadSettingsView;
  window.initWallpaperSystem = initWallpaperSystem;
  window.initProtocolPills = initProtocolPills;
})();
