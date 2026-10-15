// ZeroGate — 工具函数库 (modules/utils.js)
// 包含 DOM 辅助、字符串脱敏、格式化、到期时间换算、模型价格计算及渠道元数据
(function () {
  "use strict";

  // 1. DOM 快捷获取
  const $ = (id) => document.getElementById(id);
  window.$ = $;

  // 2. 文本转义（防止 XSS）
  function esc(s) {
    if (s == null) return "";
    return String(s)
      .replace(/&/g, "&amp;")
      .replace(/</g, "&lt;")
      .replace(/>/g, "&gt;")
      .replace(/"/g, "&quot;")
      .replace(/'/g, "&#39;");
  }
  window.esc = esc;

  // 3. 轻量全局 Toast 提示
  let toastTimer = null;
  function toast(msg) {
    const t = $("toast");
    if (!t) return;
    t.textContent = msg;
    t.classList.remove("hidden");
    clearTimeout(toastTimer);
    toastTimer = setTimeout(() => t.classList.add("hidden"), 3000);
  }
  window.toast = toast;

  // 4. UID 脱敏/缩写
  function shortUid(uid) {
    if (!uid) return "";
    return uid.length <= 12 ? uid : uid.slice(0, 6) + "…" + uid.slice(-4);
  }
  window.shortUid = shortUid;

  // 5. 复制到剪贴板
  function copyText(text, label = "内容") {
    if (navigator.clipboard && navigator.clipboard.writeText) {
      navigator.clipboard.writeText(text)
        .then(() => toast(`${label}已复制到剪贴板`))
        .catch(() => fallbackCopy(text, label));
    } else {
      fallbackCopy(text, label);
    }
  }
  function fallbackCopy(text, label) {
    const ta = document.createElement("textarea");
    ta.value = text;
    ta.style.position = "fixed";
    ta.style.opacity = "0";
    document.body.appendChild(ta);
    ta.select();
    try {
      document.execCommand("copy");
      toast(`${label}已复制到剪贴板`);
    } catch (e) {
      toast("复制失败，请手动选择复制");
    }
    document.body.removeChild(ta);
  }
  window.copyText = copyText;

  // 6. Token 与数字格式化
  function fmtTokens(n) {
    if (n == null || isNaN(n)) return "0";
    n = Number(n);
    if (n >= 1e8) return (n / 1e8).toFixed(2).replace(/\.00$/, "") + " 亿";
    if (n >= 1e4) return (n / 1e4).toFixed(1).replace(/\.0$/, "") + " 万";
    return n.toLocaleString();
  }
  window.fmtTokens = fmtTokens;

  function fmtTotal(n) {
    if (n == null || isNaN(n)) return "0";
    n = Number(n);
    if (n >= 1e8) return (n / 1e8).toFixed(2).replace(/\.00$/, "") + " 亿";
    if (n >= 1e4) return (n / 1e4).toFixed(1).replace(/\.0$/, "") + " 万";
    return n.toLocaleString();
  }
  window.fmtTotal = fmtTotal;

  function isTokenUnit(kind) {
    return kind === "zcode" || kind === "atria";
  }
  window.isTokenUnit = isTokenUnit;

  // 7. 图表取色（读取 CSS 变量，双主题自动同步）
  function cssVar(name) {
    return window.__cssVar ? window.__cssVar(name) : "";
  }
  window.cssVar = cssVar;

  function chColor(kind) {
    const v = cssVar("--ch-" + kind);
    if (v) return v;
    const fb = {
      workbuddy: "#7c3aed",
      traework: "#2563eb",
      qoder: "#0891b2",
      codebuddy: "#16a34a",
      zcode: "#6366f1",
      autoclaw: "#0d9488",
      atria: "#0284c7",
      copilot: "#475569",
      doubao: "#dc2626",
      gptgemini: "#db2777",
      glmflash: "#65a30d",
      groq: "#f59e0b",
      tierflow: "#d946ef",
    };
    return fb[kind] || "#64748b";
  }
  window.chColor = chColor;

  // 8. 渠道元数据
  const CHANNEL_METAS = {
    workbuddy: { name: "WorkBuddy", cls: "wb", unit: "credit", checkin: true },
    codebuddy: { name: "CodeBuddy 国际版", cls: "cb", unit: "credit", checkin: false },
    traework:  { name: "Trae 国际版", cls: "trae", unit: "credit", checkin: false },
    qoder:     { name: "Qoder", cls: "qoder", unit: "credit", checkin: true },
    zcode:     { name: "ZCode", cls: "zc", unit: "token", checkin: false },
    autoclaw:  { name: "AutoClaw", cls: "ac", unit: "credit", checkin: true },
    atria:     { name: "Atria", cls: "atria", unit: "token", checkin: false },
    copilot:   { name: "Copilot", cls: "copilot", unit: "credit", checkin: false },
    doubao:    { name: "豆包", cls: "doubao", unit: "credit", checkin: false },
    gptgemini: { name: "GPT/Gemini 聚合", cls: "gptgemini", unit: "credit", checkin: false },
    glmflash:  { name: "GLM-Flash 免费直连", cls: "glmflash", unit: "token", checkin: false },
    groq:      { name: "Groq", cls: "groq", unit: "token", checkin: false },
    tierflow:  { name: "TierFlow", cls: "tierflow", unit: "credit", checkin: false }
  };
  window.CHANNEL_METAS = CHANNEL_METAS;

  function groupMeta(group) {
    return CHANNEL_METAS[group] || { name: group || "未知渠道", cls: group || "wb", unit: "credit", checkin: false };
  }
  window.groupMeta = groupMeta;

  // 9. 到期时间计算与明细
  function formatExpire(expireAt) {
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
  window.formatExpire = formatExpire;

  function resourceExpireText(expireAt) {
    if (!expireAt || expireAt <= 0 || expireAt >= 4102444800) return "长期有效";
    const t = new Date(expireAt * 1000);
    const md = (t.getMonth() + 1) + "-" + String(t.getDate()).padStart(2, "0");
    const days = Math.ceil((expireAt * 1000 - Date.now()) / 86400e3);
    if (days <= 0) return md + " (已过期)";
    return md + " (剩" + days + "天)";
  }
  window.resourceExpireText = resourceExpireText;

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

    if ((!expRemain || !expName) && window.detailCache && window.detailCache[a.uid] && Array.isArray(window.detailCache[a.uid].items)) {
      const res = extractFromItems(window.detailCache[a.uid].items);
      if (res) {
        if (!expireAt) expireAt = res.expireAt;
        if (!expRemain) expRemain = res.expRemain;
        if (!expName) expName = res.expName;
      }
    }

    return { expireAt, expRemain, expName };
  }
  window.getExpiringDetail = getExpiringDetail;

  function shortPkgName(name) {
    if (!name) return "临期包";
    let s = String(name).replace(/\(.*?\)/g, "").replace(/（.*?）/g, "").replace(/【.*?】/g, "").replace(/\[.*?\]/g, "").trim();
    if (s.length > 8) s = s.slice(0, 7) + "…";
    return s || "临期包";
  }
  window.shortPkgName = shortPkgName;

  // 10. 模型刊例价格表（折算美元与人民币）
  const USD_CNY = 7.2;
  window.USD_CNY = USD_CNY;

  const MODEL_PRICES = [
    { re: "glm-5-3-flash",       in: 0.11, cache: 0.032, out: 0.39 },
    { re: "glm-5-3-flashx",      in: 0.21, cache: 0.06,  out: 0.69 },
    { re: "glm-5-3",             in: 0.97, cache: 0.18,  out: 3.06 },
    { re: "glm-5-2",             in: 0.97, cache: 0.18,  out: 3.06 },
    { re: "glm-4-air",           in: 0.14, cache: 0.035, out: 0.14 },
    { re: "glm-4-flash",         in: 0.01, cache: 0.005, out: 0.01 },
    { re: "deepseek-reasoner",   in: 0.55, cache: 0.14,  out: 2.19 },
    { re: "deepseek-r1",         in: 0.55, cache: 0.14,  out: 2.19 },
    { re: "deepseek-chat",       in: 0.27, cache: 0.07,  out: 1.10 },
    { re: "deepseek-v3",         in: 0.27, cache: 0.07,  out: 1.10 },
    { re: "deepseek",            in: 0.27, cache: 0.07,  out: 1.10 },
    { re: "claude-3-7-sonnet",   in: 3.00, cache: 0.30,  out: 15.00 },
    { re: "claude-3-5-sonnet",   in: 3.00, cache: 0.30,  out: 15.00 },
    { re: "claude-3-5-haiku",    in: 0.80, cache: 0.08,  out: 4.00 },
    { re: "claude-3-opus",       in: 15.00, cache: 1.50, out: 75.00 },
    { re: "claude",              in: 3.00, cache: 0.30,  out: 15.00 },
    { re: "gpt-4-5",             in: 75.00, cache: 37.50, out: 150.00 },
    { re: "gpt-4o-mini",         in: 0.15, cache: 0.075, out: 0.60 },
    { re: "gpt-4o",              in: 2.50, cache: 1.25,  out: 10.00 },
    { re: "o1-mini",             in: 1.10, cache: 0.55,  out: 4.40 },
    { re: "o1",                  in: 15.00, cache: 7.50, out: 60.00 },
    { re: "o3-mini",             in: 1.10, cache: 0.55,  out: 4.40 },
    { re: "gemini-2-5-pro",      in: 1.25, cache: 0.31,  out: 5.00 },
    { re: "gemini-2-5-flash",    in: 0.075, cache: 0.019, out: 0.30 },
    { re: "gemini-2-0-pro",      in: 1.25, cache: 0.31,  out: 5.00 },
    { re: "gemini-2-0-flash",    in: 0.10, cache: 0.025, out: 0.40 },
    { re: "gemini-1-5-pro",      in: 1.25, cache: 0.31,  out: 5.00 },
    { re: "gemini-1-5-flash",    in: 0.075, cache: 0.019, out: 0.30 },
    { re: "gemini",              in: 0.10, cache: 0.025, out: 0.40 },
    { re: "doubao-1-5-pro",      in: 0.11, cache: 0.028, out: 0.28 },
    { re: "doubao-1-5-lite",     in: 0.042, cache: 0.011, out: 0.083 },
    { re: "doubao",              in: 0.11, cache: 0.028, out: 0.28 },
    { re: "qwen-max",            in: 2.78, cache: 0.69,  out: 8.33 },
    { re: "qwen-plus",           in: 0.56, cache: 0.14,  out: 1.67 },
    { re: "qwen-turbo",          in: 0.042, cache: 0.011, out: 0.083 },
    { re: "qwen-coder",          in: 0.56, cache: 0.14,  out: 1.67 },
    { re: "qwen",                in: 0.56, cache: 0.14,  out: 1.67 },
    { re: "moonshot-v1-128k",    in: 1.67, cache: 0.42,  out: 8.33 },
    { re: "moonshot-v1-32k",     in: 0.83, cache: 0.21,  out: 4.17 },
    { re: "moonshot-v1-8k",      in: 0.28, cache: 0.07,  out: 1.67 },
    { re: "kimi",                in: 0.83, cache: 0.21,  out: 4.17 }
  ];
  window.MODEL_PRICES = MODEL_PRICES;

  function priceUSD(model, prompt, comp, cacheRead) {
    if (!model) return 0;
    const name = String(model).toLowerCase().replace(/\./g, "-");
    let hit = null;
    for (const p of MODEL_PRICES) {
      if (name.includes(p.re)) { hit = p; break; }
    }
    if (!hit) {
      hit = { in: 0.5, cache: 0.1, out: 2.0 };
    }
    const pM = (prompt || 0) / 1e6;
    const cM = (comp || 0) / 1e6;
    const crM = (cacheRead || 0) / 1e6;
    return (pM * hit.in) + (crM * (hit.cache != null ? hit.cache : hit.in * 0.25)) + (cM * hit.out);
  }
  window.priceUSD = priceUSD;
})();
