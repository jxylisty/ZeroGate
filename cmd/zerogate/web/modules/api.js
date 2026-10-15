// ZeroGate — 网络请求与异步操作封装 (modules/api.js)
(function () {
  "use strict";

  // 1. 核心 API 请求
  async function api(path, body, customOpts = {}) {
    const opts = {
      method: "GET",
      headers: {
        "Content-Type": "application/json",
        ...(customOpts.headers || {})
      },
      ...customOpts
    };
    if (body !== undefined) {
      opts.method = "POST";
      opts.body = JSON.stringify(body);
    }
    try {
      const resp = await fetch(path, opts);
      const data = await resp.json().catch(() => ({}));
      if (!resp.ok) {
        throw new Error(data.error || ("请求失败 " + resp.status));
      }
      return data;
    } catch (err) {
      if (err.name === "TypeError" && err.message.includes("Failed to fetch")) {
        throw new Error("网络连接失败，请确认网关常驻程序是否正常运行");
      }
      throw err;
    }
  }
  window.api = api;

  // 2. 元素/按钮 Loading 态设置
  function setElLoading(target, loading) {
    if (!target) return;
    if (loading) {
      target.classList.add("is-loading", "spin");
      if (target.style) target.style.pointerEvents = "none";
      if (typeof target.disabled !== "undefined") target.disabled = true;
    } else {
      target.classList.remove("is-loading", "spin");
      if (target.style) target.style.pointerEvents = "";
      if (typeof target.disabled !== "undefined") target.disabled = false;
    }
  }
  window.setElLoading = setElLoading;

  // 3. 通用异步执行装饰器（自动处理按钮 loading 与异常拦截）
  async function withLoading(targetBtn, taskFn) {
    if (targetBtn && (targetBtn.classList.contains("is-loading") || targetBtn.disabled)) {
      return; // 防高频连点
    }
    setElLoading(targetBtn, true);
    try {
      return await taskFn();
    } catch (e) {
      if (window.toast) window.toast(e.message);
      throw e;
    } finally {
      setElLoading(targetBtn, false);
    }
  }
  window.withLoading = withLoading;
})();
