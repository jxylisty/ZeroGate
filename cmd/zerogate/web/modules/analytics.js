// ============================================================
// ZeroGate WebUI - Analytics Module (资产总览与高级原生 SVG 图表引擎)
// 零外部依赖，纯原生 SVG 渲染，无内存泄漏与数据口径不一致问题
// ============================================================

(function () {
  "use strict";

  // 最近一次 usage 响应缓存：续航估算用（loadAssets 与 loadTokenAnalytics 都触发重算）
  let _lastAccounts = [];

  // ============================================================
  // 剩余资产总览（积分折算 token）
  // ============================================================

  async function loadAssets() {
    try {
      const res = await fetch("/api/assets");
      if (!res.ok) return;
      const data = await res.json();
      const channels = data.channels || [];

      // 1. Token 核心储备库 (zcode, atria 等实测 1:1 Token 渠道)
      const tokenChs = channels.filter(c => c.convertible || c.channel === "zcode" || c.channel === "atria");
      const totalTokens = tokenChs.reduce((s, c) => s + (c.tokens || c.credits || 0), 0);
      const heroTokEl = $("assetHeroTokens");
      if (heroTokEl) heroTokEl.textContent = fmtTotal(totalTokens) + " Token";
      const heroCredEl = $("assetHeroCredits");
      if (heroCredEl) {
        heroCredEl.textContent = `来自 ${tokenChs.map(c => (CHANNEL_METAS[c.channel] && CHANNEL_METAS[c.channel].name) || c.channel).join(" + ")} · 1 积分 = 1 Token（直通官方）`;
      }

      const tokTb = $("tokenAssetsBody");
      if (tokTb) {
        tokTb.innerHTML = tokenChs.map(c => {
          const meta = CHANNEL_METAS[c.channel] || { name: c.channel, color: "#6366f1" };
          const acct = `${c.healthy}/${c.accounts}`;
          const note = c.note || "1 积分 = 1 Token · 直通上游";
          return `
            <tr>
              <td><span class="badge" style="--badge-c:${meta.color};">${esc(meta.name)}</span></td>
              <td><strong>${acct}</strong> 账号在线</td>
              <td><strong style="color:var(--ok); font-family:var(--mono); font-size:14px;">${fmtTotal(c.tokens || c.credits)}</strong> Token</td>
              <td><span class="assets-note">${esc(note)}</span></td>
            </tr>`;
        }).join("") || `<tr><td colspan="4" style="text-align:center; color:var(--muted); padding:16px;">暂无 Token 储备渠道</td></tr>`;
      }

      // 2. 各渠道配额与积分池 (workbuddy, traework, qoder, codebuddy, autoclaw)
      const creditChs = channels.filter(c => !c.convertible && c.channel !== "zcode" && c.channel !== "atria");
      const totalCredits = creditChs.reduce((s, c) => s + (c.credits || 0), 0);
      const heroCreditsTotalEl = $("assetHeroCreditsTotal");
      if (heroCreditsTotalEl) heroCreditsTotalEl.textContent = fmtTotal(totalCredits) + " 积分/额度";
      if ($("assetsUpdatedAt")) $("assetsUpdatedAt").textContent = "更新于 " + (data.updated_at || "");

      const credTb = $("creditAssetsBody");
      if (credTb) {
        credTb.innerHTML = creditChs.map(c => {
          const meta = CHANNEL_METAS[c.channel] || { name: c.channel, color: "#64748b" };
          const acct = `${c.healthy}/${c.accounts}`;
          const note = c.note || "按量扣除 / 每日配额制";
          return `
            <tr>
              <td><span class="badge" style="--badge-c:${meta.color};">${esc(meta.name)}</span></td>
              <td><strong>${acct}</strong> 账号在线</td>
              <td><strong style="color:var(--chart-credit); font-family:var(--mono); font-size:14px;">${fmtTotal(c.credits)}</strong></td>
              <td><span class="assets-note">${esc(note)}</span></td>
            </tr>`;
        }).join("") || `<tr><td colspan="4" style="text-align:center; color:var(--muted); padding:16px;">暂无积分渠道</td></tr>`;
      }

      renderRunway();
    } catch (e) { /* 面板加载失败静默 */ }
  }

  // ============================================================
  // Token 资产大屏与高级图表引擎
  // ============================================================

  async function loadTokenAnalytics() {
    try {
      const [tokenData, auditAnalytics, usageData] = await Promise.all([
        api("/api/token_usage").catch(() => ({})),
        api("/api/audit/analytics").catch(() => ({})),
        api("/api/usage").catch(() => ({ accounts: [] })),
      ]);

      const accounts = usageData.accounts || [];
      _lastAccounts = accounts;

      renderTokenHero(tokenData.summary || {}, tokenData.models || {}, tokenData.channels || {});
      renderCreditSummary(accounts);
      renderCreditTrend(accounts);
      renderTokenTrend(auditAnalytics.daily_trend || [], accounts);
      renderSavingsTrend(auditAnalytics.daily_trend || [], tokenData.models || {});
      renderTokenHeatmap(auditAnalytics.daily_trend || [], accounts);
      renderChannelDonut(tokenData.channels || auditAnalytics.channel_tokens || {});
      renderTopModels(tokenData.models || {}, auditAnalytics.top_models || []);
      renderLowBalanceAccounts(accounts);
      renderAcctProductivity(accounts);
      renderRunway();
    } catch (e) {
      console.error("loadTokenAnalytics error:", e);
    }
  }

  // 按 输入/输出/缓存命中 用量折算官方 API 美元成本（单价 USD/1M）。
  const EST_CACHE_HIT = 0.9;
  const EST_CACHE_MIN_RATE = 0.3;
  function priceUSD(model, prompt, completion, cacheRead) {
    const p = (typeof lookupPrice === "function") ? lookupPrice(model) : FALLBACK_PRICE;
    let cache = Math.max(0, Math.min(cacheRead, prompt));
    if (prompt > 0 && cache / prompt < EST_CACHE_MIN_RATE) cache = prompt * EST_CACHE_HIT;
    const plainInput = prompt - cache;
    return (plainInput * p.in + completion * p.out + cache * p.cache) / 1e6;
  }

  // 1. Hero 财富看板与价值折算
  function renderTokenHero(summary, models, channels) {
    const total = Number(summary.total_tokens || 0);
    const prompt = Number(summary.prompt_tokens || 0);
    const comp = Number(summary.completion_tokens || 0);
    const cache = Number(summary.cache_read_tokens || 0);

    if ($("tkHeroTotal")) $("tkHeroTotal").textContent = fmtTokens(total);
    if ($("tkHeroPrompt")) $("tkHeroPrompt").textContent = fmtTokens(prompt);
    if ($("tkHeroComp")) $("tkHeroComp").textContent = fmtTokens(comp);
    if ($("tkHeroCache")) $("tkHeroCache").textContent = fmtTokens(cache);

    const promptPct = total > 0 ? Math.round((prompt / total) * 100) : 0;
    const compPct = total > 0 ? Math.round((comp / total) * 100) : 0;
    const cacheRate = (prompt + cache) > 0 ? Math.round((cache / (prompt + cache)) * 100) : 0;

    if ($("tkHeroPromptPct")) $("tkHeroPromptPct").textContent = `占比 ${promptPct}%`;
    if ($("tkHeroCompPct")) $("tkHeroCompPct").textContent = `占比 ${compPct}%`;

    if ($("tkHeroCacheRate")) {
      const chRates = Object.entries(channels || {})
        .filter(([, s]) => (Number(s.prompt_tokens) + Number(s.cache_read_tokens)) > 0)
        .map(([k, s]) => {
          const p = Number(s.prompt_tokens) || 0;
          const c = Number(s.cache_read_tokens) || 0;
          if (c === 0 && p > 0) return `${k} 未上报`;
          return `${k} ${Math.round((c / (p + c)) * 100)}%`;
        });
      $("tkHeroCacheRate").textContent = `命中率 ${cacheRate}%`;
      if (chRates.length > 0) {
        $("tkHeroCacheRate").title = `分渠道缓存命中率：${chRates.join("、")}\n（未上报 = 上游 usage 不含缓存字段，非真实无缓存）`;
      }
    }

    let savingsUsd = 0;
    const breakdown = [];
    const modelEntries = Object.entries(models || {});
    if (modelEntries.length > 0) {
      for (const [name, m] of modelEntries) {
        const usd = priceUSD(
          name,
          Number(m.prompt_tokens || 0),
          Number(m.completion_tokens || 0),
          Number(m.cache_read_tokens || 0),
        );
        savingsUsd += usd;
        if (usd > 0.0001) {
          const label = name.includes(" (历史)")
            ? name.replace(" (历史)", "·历史用量（按渠道主力模型档）")
            : (name || "未知模型");
          breakdown.push({ name: label, usd, tokens: Number(m.total_tokens || 0) });
        }
      }
    } else {
      savingsUsd = priceUSD("", prompt, comp, cache);
      breakdown.push({ name: "全局拆分估算", usd: savingsUsd, tokens: total });
    }
    breakdown.sort((a, b) => b.usd - a.usd);

    const tipLines = breakdown.slice(0, 6).map(b =>
      `${b.name}：¥${(b.usd * USD_CNY).toFixed(2)}（${fmtTokens(b.tokens)} tokens）`);
    const tip = `按各模型官方 API 单价折算（汇率 1 USD=${USD_CNY} CNY）\n${tipLines.join("\n")}`;

    const savingsYuan = (savingsUsd * USD_CNY).toFixed(2);
    const savingsUsdStr = savingsUsd.toFixed(2);
    if ($("tkHeroSavings")) {
      $("tkHeroSavings").textContent = `约为你白嫖节省: ￥${savingsYuan} (约 $${savingsUsdStr})`;
      $("tkHeroSavings").title = tip;
    }
  }

  // 1.5 消耗/变动汇总卡片：积分族与 Token 族分列
  function renderCreditSummary(accounts) {
    const acc = accounts || [];
    const credit = acc.filter(a => !isTokenUnit(a.kind));
    const token = acc.filter(a => isTokenUnit(a.kind));
    const now = new Date();
    const key = i => localYMD(new Date(now.getTime() - i * 86400 * 1000));

    // 积分族：逐日净变动
    const cDelta = {};
    credit.forEach(a => (a.daily || []).forEach(d => {
      if (!d.day) return;
      cDelta[d.day] = (cDelta[d.day] || 0) + Number(d.delta || 0);
    }));
    // Token 族：逐日真实 Token 消耗
    const tToken = {};
    token.forEach(a => (a.tokens || []).forEach(t => {
      if (!t.day) return;
      tToken[t.day] = (tToken[t.day] || 0) + Number(t.total || 0) + Number(t.cache_read || 0);
    }));

    const sumRange = (map, days) => {
      let s = 0;
      for (let i = 0; i < days; i++) s += map[key(i)] || 0;
      return s;
    };
    const setV = (id, v) => { const el = $(id); if (el) el.textContent = v; };
    const setS = (id, t) => { const el = $(id); if (el) el.textContent = t; };

    // 积分族（单位：积分）
    const cToday = credit.reduce((s, a) => s + Number(a.today || 0), 0);
    const c7 = sumRange(cDelta, 7), c14 = sumRange(cDelta, 14);
    setV("creditToday", fmtTotal(Math.round(cToday)));
    setV("credit7d", fmtTotal(Math.round(c7)));
    setV("credit14d", fmtTotal(Math.round(c14)));
    setS("creditTodaySub", `${key(0)} 当日`);
    setS("credit7dSub", `日均 ${fmtTotal(Math.round(c7 / 7))}/天`);
    setS("credit14dSub", `日均 ${fmtTotal(Math.round(c14 / 14))}/天`);
    const cTip = "积分族（workbuddy/traework/qoder/codebuddy/autoclaw）余额以积分/额度计，无法折算 token。正=消耗，负=签到/赠送入账。";
    ["creditToday", "credit7d", "credit14d"].forEach(id => { const el = $(id); if (el) el.title = cTip; });

    // Token 族（单位：token）
    const tToday = tToken[key(0)] || 0;
    const t7 = sumRange(tToken, 7), t14 = sumRange(tToken, 14);
    setV("tokenToday", fmtTokens(tToday));
    setV("token7d", fmtTokens(t7));
    setV("token14d", fmtTokens(t14));
    setS("tokenTodaySub", `${key(0)} 当日`);
    setS("token7dSub", `日均 ${fmtTokens(Math.round(t7 / 7))}/天`);
    setS("token14dSub", `日均 ${fmtTokens(Math.round(t14 / 14))}/天`);
    const tTip = "Token 族（zcode/atria）余额即 token，消耗取上游 usage 上报的真实 token 用量（非余额变化）。";
    ["tokenToday", "token7d", "token14d"].forEach(id => { const el = $(id); if (el) el.title = tTip; });
  }

  // 1.6 近 14 天积分净变动趋势（仅积分族）
  function renderCreditTrend(accounts) {
    const wrap = $("creditTrendChartWrap");
    if (!wrap) return;

    const dayMap = {};
    (accounts || []).filter(a => !isTokenUnit(a.kind)).forEach(a => {
      (a.daily || []).forEach(d => {
        if (!d.day) return;
        const m = dayMap[d.day] || (dayMap[d.day] = { delta: 0, remain: 0 });
        m.delta += Number(d.delta || 0);
        m.remain += Number(d.remain || 0);
      });
    });

    const now = new Date();
    const days = [];
    for (let i = 13; i >= 0; i--) {
      const key = localYMD(new Date(now.getTime() - i * 86400 * 1000));
      const m = dayMap[key] || { delta: 0, remain: 0 };
      days.push({ day: key, label: key.slice(5), delta: m.delta, remain: m.remain });
    }

    const hasPos = days.some(d => d.delta > 0);
    const hasNeg = days.some(d => d.delta < 0);
    if (!hasPos && !hasNeg) {
      wrap.innerHTML = `<div class="empty-chart">暂无积分族按日数据，跑起业务后这里会显示积分净变动</div>`;
      return;
    }

    const maxPos = Math.max(...days.map(d => Math.max(0, d.delta)));
    const maxNeg = Math.max(...days.map(d => Math.max(0, -d.delta)));

    const W = 740, H = 220;
    const padLeft = 55, padRight = 15, padTop = 20, padBottom = 28;
    const chartW = W - padLeft - padRight;
    const chartH = H - padTop - padBottom;

    let zeroY = padTop + chartH, scalePos = 0, scaleNeg = 0;
    if (hasPos && hasNeg) {
      const total = maxPos + maxNeg || 1;
      zeroY = padTop + (maxPos / total) * chartH;
      scalePos = chartH / total;
      scaleNeg = chartH / total;
    } else if (hasPos) {
      scalePos = chartH / (maxPos || 1);
    } else {
      zeroY = padTop;
      scaleNeg = chartH / (maxNeg || 1);
    }
    const yOf = v => v >= 0 ? zeroY - v * scalePos : zeroY + (-v) * scaleNeg;

    const cGridLine = "rgba(255, 255, 255, 0.04)";
    const cLabel = cssVar("--chart-label") || "rgba(255, 255, 255, 0.45)";

    let grid = "";
    const ticks = [];
    if (hasPos) ticks.push(maxPos);
    if (hasNeg) ticks.push(-maxNeg);
    ticks.forEach(v => {
      const y = yOf(v);
      grid += `
        <line x1="${padLeft}" y1="${y}" x2="${W - padRight}" y2="${y}" stroke="${cGridLine}" stroke-width="1" stroke-dasharray="3,3" />
        <text x="${padLeft - 8}" y="${y + 4}" text-anchor="end" font-size="10" fill="${cLabel}" font-family="var(--mono)">${fmtTotal(Math.round(Math.abs(v)))}</text>
      `;
    });

    const stepW = chartW / days.length;
    const barW = Math.max(16, Math.min(28, stepW * 0.58));
    let bars = "";
    days.forEach((d, i) => {
      const x = padLeft + i * stepW + (stepW - barW) / 2;
      const dTxt = d.delta >= 0
        ? `净消耗 ${fmtTotal(Math.round(d.delta))} 积分`
        : `净入账 ${fmtTotal(Math.round(-d.delta))} 积分`;
      const tip = `${d.day}\n${dTxt}\n当日末余额 ${fmtTotal(Math.round(d.remain))}`;
      let rect = "";
      if (d.delta > 0) {
        const h = Math.max(3, d.delta * scalePos);
        rect = `<rect x="${x}" y="${zeroY - h}" width="${barW}" height="${h}" fill="url(#gradCreditExpense)" rx="6" />`;
      } else if (d.delta < 0) {
        const h = Math.max(3, (-d.delta) * scaleNeg);
        rect = `<rect x="${x}" y="${zeroY}" width="${barW}" height="${h}" fill="url(#gradCreditIncome)" rx="6" />`;
      }
      bars += `
        <g class="chart-bar-group">
          <title>${esc(tip)}</title>
          ${rect}
          <text x="${x + barW / 2}" y="${H - 10}" text-anchor="middle" font-size="10" fill="${cLabel}" font-family="var(--mono)">${d.label}</text>
        </g>
      `;
    });

    wrap.innerHTML = `
      <svg viewBox="0 0 ${W} ${H}" style="width:100%; height:100%; display:block;" preserveAspectRatio="xMidYMid meet">
        <defs>
          <linearGradient id="gradCreditIncome" x1="0" y1="0" x2="0" y2="1">
            <stop offset="0%" stop-color="#34d399" stop-opacity="0.95" />
            <stop offset="100%" stop-color="#059669" stop-opacity="0.65" />
          </linearGradient>
          <linearGradient id="gradCreditExpense" x1="0" y1="0" x2="0" y2="1">
            <stop offset="0%" stop-color="#fb7185" stop-opacity="0.95" />
            <stop offset="100%" stop-color="#e11d48" stop-opacity="0.75" />
          </linearGradient>
        </defs>
        ${grid}
        <line x1="${padLeft}" y1="${zeroY}" x2="${W - padRight}" y2="${zeroY}" stroke="rgba(255, 255, 255, 0.12)" stroke-width="1.2" />
        ${bars}
      </svg>
    `;
  }

  // 统一按日 Token 用量对账汇总
  function buildUnifiedDailyTokenMap(auditDaily, accounts) {
    const map = {};

    // 1. 优先以审计日志 auditDaily 为网关级全量事实源
    (auditDaily || []).forEach(d => {
      if (!d.day) return;
      const prompt = Number(d.prompt || 0);
      const comp = Number(d.completion || 0);
      const cache = Number(d.cache || 0);
      const total = prompt + comp + cache;
      map[d.day] = { prompt, comp, cache, total };
    });

    // 2. 从 accounts.tokens 聚合各账号的按日明细
    const acctDayMap = {};
    (accounts || []).forEach(a => {
      (a.tokens || []).forEach(t => {
        if (!t.day) return;
        const p = Number(t.prompt || 0);
        const c = Number(t.completion || 0);
        const k = Number(t.cache_read || 0) + Number(t.cache_write || 0);
        const tot = (t.total != null && Number(t.total) > 0) ? Number(t.total) : (p + c + k);
        if (!acctDayMap[t.day]) {
          acctDayMap[t.day] = { prompt: 0, comp: 0, cache: 0, total: 0 };
        }
        acctDayMap[t.day].prompt += p;
        acctDayMap[t.day].comp += c;
        acctDayMap[t.day].cache += k;
        acctDayMap[t.day].total += tot;
      });
    });

    // 3. 合并：取较大值以兼顾历史补录
    for (const [day, val] of Object.entries(acctDayMap)) {
      if (!map[day] || map[day].total === 0) {
        map[day] = val;
      } else if (val.total > map[day].total) {
        map[day] = val;
      }
    }

    return map;
  }

  // 2. 近 14 天 Token 每日消耗趋势（SVG 堆叠柱状图）
  function renderTokenTrend(auditDaily, accounts) {
    const wrap = $("tokenTrendChartWrap");
    if (!wrap) return;

    const dayMap = buildUnifiedDailyTokenMap(auditDaily, accounts);

    const days = [];
    const now = new Date();
    for (let i = 13; i >= 0; i--) {
      const key = localYMD(new Date(now.getTime() - i * 86400 * 1000));
      const item = dayMap[key] || { prompt: 0, comp: 0, cache: 0, total: 0 };
      days.push({ day: key, label: key.slice(5), ...item });
    }

    const maxTotal = Math.max(...days.map(d => d.total), 1000);

    const W = 740, H = 220;
    const padLeft = 55, padRight = 15, padTop = 20, padBottom = 28;
    const chartW = W - padLeft - padRight;
    const chartH = H - padTop - padBottom;

    let gridLines = "";
    const cGrid = "rgba(255, 255, 255, 0.04)";
    const cLabel = cssVar("--chart-label") || "rgba(255, 255, 255, 0.45)";
    for (let step = 0; step <= 4; step++) {
      const yVal = Math.round((maxTotal / 4) * step);
      const y = padTop + chartH - (step / 4) * chartH;
      gridLines += `
        <line x1="${padLeft}" y1="${y}" x2="${W - padRight}" y2="${y}" stroke="${cGrid}" stroke-width="1" stroke-dasharray="3,3" />
        <text x="${padLeft - 8}" y="${y + 4}" text-anchor="end" font-size="10" fill="${cLabel}" font-family="var(--mono)">${fmtTokens(yVal)}</text>
      `;
    }

    const stepW = chartW / days.length;
    const barW = Math.max(16, Math.min(30, stepW * 0.62));
    let bars = "";

    days.forEach((d, i) => {
      const x = padLeft + i * stepW + (stepW - barW) / 2;
      const pHeight = (d.prompt / maxTotal) * chartH;
      const cHeight = (d.comp / maxTotal) * chartH;
      const kHeight = (d.cache / maxTotal) * chartH;

      const pY = padTop + chartH - pHeight;
      const cY = pY - cHeight;
      const kY = cY - kHeight;

      const tip = `${d.day}\n总计消耗: ${fmtTokens(d.total)}\n输入: ${fmtTokens(d.prompt)}\n输出: ${fmtTokens(d.comp)}\n缓存: ${fmtTokens(d.cache)}`;

      bars += `
        <g class="chart-bar-group">
          <title>${esc(tip)}</title>
          <rect x="${x}" y="${pY}" width="${barW}" height="${Math.max(2, pHeight)}" fill="url(#gradSunsetPrompt)" rx="6" />
          <rect x="${x}" y="${cY}" width="${barW}" height="${Math.max(2, cHeight)}" fill="url(#gradSunsetComp)" rx="6" />
          <rect x="${x}" y="${kY}" width="${barW}" height="${Math.max(2, kHeight)}" fill="url(#gradSunsetCache)" rx="6" />
          <text x="${x + barW / 2}" y="${H - 10}" text-anchor="middle" font-size="10" fill="${cLabel}">${d.label}</text>
        </g>
      `;
    });

    wrap.innerHTML = `
      <svg viewBox="0 0 ${W} ${H}" style="width:100%; height:100%; display:block;" preserveAspectRatio="xMidYMid meet">
        <defs>
          <linearGradient id="gradSunsetPrompt" x1="0" y1="0" x2="0" y2="1">
            <stop offset="0%" stop-color="#8B5CF6" stop-opacity="0.95" />
            <stop offset="100%" stop-color="#8B5CF6" stop-opacity="0.15" />
          </linearGradient>
          <linearGradient id="gradSunsetCache" x1="0" y1="0" x2="0" y2="1">
            <stop offset="0%" stop-color="#F472B6" stop-opacity="0.95" />
            <stop offset="100%" stop-color="#FB923C" stop-opacity="0.85" />
          </linearGradient>
          <linearGradient id="gradSunsetComp" x1="0" y1="0" x2="0" y2="1">
            <stop offset="0%" stop-color="#EC4899" stop-opacity="0.95" />
            <stop offset="100%" stop-color="#EC4899" stop-opacity="0.25" />
          </linearGradient>
        </defs>
        ${gridLines}
        ${bars}
      </svg>
    `;
  }

  // 2.5 Token 日历热力图（GitHub 贡献图风格）
  function renderTokenHeatmap(auditDaily, accounts) {
    const wrap = $("tokenHeatmapWrap");
    if (!wrap) return;

    const dayMap = buildUnifiedDailyTokenMap(auditDaily, accounts);

    const WEEK_MAX = 26, LEAD_WEEKS = 2;
    const today = new Date();
    today.setHours(0, 0, 0, 0);

    let firstData = null;
    for (const day of Object.keys(dayMap)) {
      const d = new Date(day + "T00:00:00");
      if (!isNaN(d) && d <= today && (firstData === null || d < firstData)) firstData = d;
    }
    if (!firstData) {
      wrap.innerHTML = `<div class="heatmap-empty-hint">暂无按日数据，跑起业务后这里会长出热力图</div>`;
      return;
    }

    const gridStart = new Date(firstData);
    gridStart.setDate(gridStart.getDate() - gridStart.getDay() - LEAD_WEEKS * 7);

    const totalDays = Math.round((today - gridStart) / 86400e3) + 1;
    const nWeeks = Math.min(WEEK_MAX, Math.ceil(totalDays / 7));

    const tokensOf = d => (dayMap[localYMD(d)] ? dayMap[localYMD(d)].total : 0);
    const allVals = Object.values(dayMap).map(v => v.total).filter(v => v > 0).sort((a, b) => a - b);
    const q = p => allVals.length ? allVals[Math.min(allVals.length - 1, Math.floor(allVals.length * p))] : 0;
    const t1 = Math.max(q(0.25), 1), t2 = Math.max(q(0.5), t1 + 1), t3 = Math.max(q(0.75), t2 + 1);
    const level = v => v <= 0 ? 0 : v < t1 ? 1 : v < t2 ? 2 : v < t3 ? 3 : 4;
    const fmtDay = d => `${localYMD(d)}\n${fmtTokens(tokensOf(d))} tokens`;

    let weeksHtml = "";
    const monthCols = new Array(nWeeks).fill(null);
    for (let w = 0; w < nWeeks; w++) {
      let col = "";
      for (let dow = 0; dow < 7; dow++) {
        const d = new Date(gridStart);
        d.setDate(d.getDate() + w * 7 + dow);
        if (d > today) {
          col += `<span class="heatmap-cell" style="visibility:hidden"></span>`;
          continue;
        }
        if (d.getDate() === 1 && !monthCols[w]) monthCols[w] = (d.getMonth() + 1) + "月";
        const lv = level(tokensOf(d));
        col += `<span class="heatmap-cell lvl${lv}" title="${esc(fmtDay(d))}"></span>`;
      }
      weeksHtml += `<div class="heatmap-week">${col}</div>`;
    }

    const slots = monthCols.map(m => `<span class="heatmap-month-slot">${m ? esc(m) : ""}</span>`).join("");
    wrap.innerHTML = `
      <div class="heatmap-monthstrip">
        <span class="heatmap-month-spacer"></span>${slots}
      </div>
      <div class="heatmap-flex">
        <div class="heatmap-weekday-col">
          <span></span><span>一</span><span></span><span>三</span><span></span><span>五</span><span></span>
        </div>
        <div class="heatmap-weeks">${weeksHtml}</div>
      </div>
    `;
  }

  // 3. 白嫖渠道贡献占比（SVG 环形 Donut 图）
  function renderChannelDonut(channelTokens) {
    const wrap = $("channelDonutWrap");
    if (!wrap) return;

    let total = 0;
    const items = [];
    for (const [k, val] of Object.entries(channelTokens)) {
      const tokens = typeof val === "object" ? Number(val.total_tokens || 0) : Number(val || 0);
      if (tokens > 0) {
        total += tokens;
        const meta = CHANNEL_METAS[k] || { name: k };
        items.push({ kind: k, name: meta.name, color: chColor(k), tokens });
      }
    }

    if (total === 0 || items.length === 0) {
      wrap.innerHTML = `<div class="empty-chart">暂无各渠道 Token 消耗记录</div>`;
      return;
    }

    items.sort((a, b) => b.tokens - a.tokens);

    const cx = 95, cy = 95, r = 68;
    const C = 2 * Math.PI * r;
    let offset = 0;
    let circles = "";
    let legend = "";

    items.forEach(it => {
      const pct = ((it.tokens / total) * 100).toFixed(1);
      const dash = (it.tokens / total) * C;
      circles += `
        <circle cx="${cx}" cy="${cy}" r="${r}" fill="transparent"
          stroke="${it.color}" stroke-width="26"
          stroke-dasharray="${dash} ${C - dash}" stroke-dashoffset="${-offset}"
          transform="rotate(-90 ${cx} ${cy})">
          <title>${esc(it.name)}: ${fmtTokens(it.tokens)} (${pct}%)</title>
        </circle>
      `;
      offset += dash;

      legend += `
        <div class="donut-legend-item">
          <div class="left">
            <span class="donut-legend-dot" style="background:${it.color}"></span>
            <span>${esc(it.name)}</span>
          </div>
          <div class="donut-legend-val">${pct}% <span style="font-size:11px;color:var(--muted);font-weight:400">(${fmtTokens(it.tokens)})</span></div>
        </div>
      `;
    });

    wrap.innerHTML = `
      <div style="width:190px; height:190px; position:relative; flex-shrink:0;">
        <svg viewBox="0 0 190 190" style="width:100%; height:100%;">
          <circle cx="${cx}" cy="${cy}" r="${r}" fill="transparent" stroke="${cssVar("--chart-track")}" stroke-width="26" />
          ${circles}
          <text x="${cx}" y="${cy - 4}" text-anchor="middle" font-size="16" font-weight="700" fill="var(--text)">${fmtTokens(total)}</text>
          <text x="${cx}" y="${cy + 14}" text-anchor="middle" font-size="11" fill="var(--muted)">总产出</text>
        </svg>
      </div>
      <div class="donut-legend">
        ${legend}
      </div>
    `;
  }

  // 4. 热门模型白嫖榜 TOP 5
  function renderTopModels(models, topModels) {
    const box = $("topModelsList");
    if (!box) return;

    let rows = [];
    const modelEntries = Object.entries(models || {});
    if (modelEntries.length > 0) {
      rows = modelEntries.map(([name, m]) => {
        const prompt = Number(m.prompt_tokens || 0);
        const comp = Number(m.completion_tokens || 0);
        const cache = Number(m.cache_read_tokens || 0);
        const usd = priceUSD(name, prompt, comp, cache);
        return { model: name, prompt, comp, cache, total: Number(m.total_tokens || 0), usd };
      });
    } else if (Array.isArray(topModels) && topModels.length > 0) {
      rows = topModels.map(m => {
        const total = Number(m.total_tokens || 0);
        const prompt = total * 0.9, comp = total * 0.1, cache = 0;
        return { model: m.model, prompt, comp, cache, total, usd: priceUSD(m.model, prompt, comp, cache) };
      });
    }

    if (rows.length === 0) {
      box.innerHTML = `<div class="empty-hint">暂无模型调用数据</div>`;
      return;
    }

    rows.sort((a, b) => b.usd - a.usd);
    const top5 = rows.slice(0, 5);
    const badges = ["top1", "top2", "top3", "", ""];
    const maxUsd = (top5[0] && top5[0].usd) ? top5[0].usd : 1;

    box.innerHTML = top5.map((m, idx) => {
      const pct = Math.max(8, Math.round((m.usd / maxUsd) * 100));
      const denom = m.prompt + m.cache + m.comp || 1;
      const seg = (v, cls) =>
        v > 0 ? `<i class="rank-seg ${cls}" style="width:${(v / denom) * 100}%"></i>` : "";
      const pBar = seg(m.prompt, "seg-p") + seg(m.cache, "seg-cr") + seg(m.comp, "seg-c");
      const savingsHtml = m.usd > 0.0001
        ? `<span class="rank-save" title="按该模型官方 API 单价独立折算的等值节省">省 ¥${(m.usd * USD_CNY).toFixed(2)}</span>`
        : "";
      return `
        <div class="rank-item">
          <span class="rank-num ${badges[idx] || ""}">${idx + 1}</span>
          <div class="rank-info">
            <div class="rank-header">
              <span class="rank-name" title="${esc(m.model)}">${esc(m.model)}</span>
              <span class="rank-val">${fmtTokens(m.total)}${savingsHtml}</span>
            </div>
            <div class="rank-bar-bg">
              <div class="rank-bar-fill" style="width:${pct}%">${pBar}</div>
            </div>
          </div>
        </div>
      `;
    }).join("");
  }

  // 5. 账号余额与到期榜
  function renderLowBalanceAccounts(accounts) {
    const box = $("lowBalanceList");
    if (!box) return;
    const all = (accounts || []).filter(a => Number(a.remain || 0) > 0 || Number(a.expire_at || 0) > 0);
    if (all.length === 0) {
      box.innerHTML = `<div class="empty-hint">暂无账号余额数据</div>`;
      return;
    }

    const now = Math.floor(Date.now() / 1000);

    const enriched = all.map(a => {
      const detail = getExpiringDetail(a);
      const expireAt = detail.expireAt || Number(a.expire_at || 0);
      return {
        ...a,
        expire_at: expireAt,
        expiring_remain: detail.expRemain,
        expiring_name: detail.expName,
      };
    });

    const rank = a => (a.expire_at && a.expire_at > 0 && a.expire_at < 4102444800) ? a.expire_at : Infinity;
    const sortFn = (a, b) => {
      const ra = rank(a), rb = rank(b);
      if (ra !== rb) return ra - rb;
      return Number(a.remain || 0) - Number(b.remain || 0);
    };

    const list = enriched.sort(sortFn);
    const badges = ["top1", "top2", "top3", "", ""];

    box.innerHTML = list.slice(0, 8).map((a, idx) => {
      const gm = groupMeta(a.kind);
      const exp = formatExpire(a.expire_at);
      const remain = Math.max(0, Number(a.remain || 0));
      const total = Number(a.total || 0);
      const isTok = isTokenUnit(a.kind);
      const unitName = isTok ? "token" : "积分";

      const expRemain = Number(a.expiring_remain || 0);
      const hasExp = a.expire_at > 0 && a.expire_at < 4102444800;
      const isPartial = hasExp && expRemain > 0 && expRemain < remain;
      const safeRemain = isPartial ? Math.max(0, remain - expRemain) : (hasExp ? 0 : remain);

      const totalVal = isTok ? `${fmtTokens(remain)} token` : `${fmtTotal(remain)} 积分`;
      const expVal = isTok ? fmtTokens(expRemain) : fmtTotal(expRemain);
      const safeVal = isTok ? fmtTokens(safeRemain) : fmtTotal(safeRemain);

      const pct = Math.max(3, Math.min(100, Math.round(total > 0 ? (remain / total) * 100 : 100)));

      const expRatio = (remain > 0 && expRemain > 0) ? (expRemain / remain) : (hasExp ? 1 : 0);
      const urgent = hasExp && (a.expire_at - now) <= 7 * 86400 && (!isPartial || expRatio >= 0.3);
      const fillCls = urgent ? "background:linear-gradient(90deg, var(--danger), var(--warn))" : "";

      let metaTagsHtml = "";
      if (isPartial) {
        metaTagsHtml = `
          <span class="tag ${exp.cls} exp-part-badge" onclick="showCreditDetail(event, '${esc(a.uid)}')" title="${esc(exp.full)} · 仅此套餐临期（${expRemain} ${unitName}），其余 ${safeVal} ${unitName} 仍有效；点击查看全部套餐明细" style="cursor:pointer;">
            ${I.alert} 仅 <b>${expVal}</b> ${unitName} ${esc(exp.text)} <span class="exp-subname">(${esc(shortPkgName(a.expiring_name))})</span>
          </span>
          <span class="tag expire-perm" title="其余套餐长期有效或更晚到期">${I.check} ${safeVal} 有效</span>
        `;
      } else if (hasExp) {
        metaTagsHtml = `
          <span class="tag ${exp.cls}" onclick="showCreditDetail(event, '${esc(a.uid)}')" title="${esc(exp.full)} · 点击查看套餐明细" style="cursor:pointer;">
            ${I.clock} 全部 ${esc(exp.text)}
          </span>
        `;
      } else {
        metaTagsHtml = `
          <span class="tag expire-perm" onclick="showCreditDetail(event, '${esc(a.uid)}')" title="长期有效 · 点击查看套餐明细" style="cursor:pointer;">
            ${I.check} 长期有效
          </span>
        `;
      }

      return `
        <div class="rank-item" title="${esc(a.uid)}">
          <span class="rank-num ${badges[idx] || ""}">${idx + 1}</span>
          <div class="rank-info">
            <div class="rank-header">
              <span class="rank-name"><span class="badge ${gm.cls}">${gm.name}</span> ${esc(a.nickname || shortUid(a.uid))}</span>
              <span class="rank-val" style="display:inline-flex;flex-direction:column;align-items:flex-end;gap:1px;">
                <span>${totalVal}</span>
                ${isPartial ? `<span style="font-size:11px;font-weight:400;color:var(--text-muted);">${safeVal} 长期有效</span>` : ""}
              </span>
            </div>
            <div class="rank-bar-bg">
              <div class="rank-bar-fill" style="width:${pct}%; ${fillCls}"></div>
            </div>
            <div class="rank-meta">
              ${metaTagsHtml}
              <code class="prod-uid" style="cursor:pointer;" onclick="showCreditDetail(event, '${esc(a.uid)}')" title="点击查看套餐明细">${esc(shortUid(a.uid))}</code>
            </div>
          </div>
        </div>`;
    }).join("");
  }

  // 5.5 近 14 天每日白嫖节省金额走势
  function renderSavingsTrend(auditDaily, tokenModels) {
    const wrap = $("savingsTrendChartWrap");
    if (!wrap) return;

    const dayMap = {};
    (auditDaily || []).forEach(d => {
      if (!d.day) return;
      dayMap[d.day] = {
        prompt: Number(d.prompt || 0),
        comp: Number(d.completion || 0),
        cache: Number(d.cache || 0),
      };
    });

    let wPromptCost = 0, totPrompt = 0;
    let wCompCost = 0, totComp = 0;
    let wCacheCost = 0, totCache = 0;
    Object.entries(tokenModels || {}).forEach(([name, m]) => {
      const p = (typeof lookupPrice === "function") ? lookupPrice(name) : FALLBACK_PRICE;
      const pr = Number(m.prompt_tokens || 0);
      const co = Number(m.completion_tokens || 0);
      const ca = Number(m.cache_read_tokens || 0);
      wPromptCost += (pr / 1e6) * p.in;
      wCompCost += (co / 1e6) * p.out;
      wCacheCost += (ca / 1e6) * p.cache;
      totPrompt += pr;
      totComp += co;
      totCache += ca;
    });

    const effInRate = totPrompt > 0 ? (wPromptCost / totPrompt) * 1e6 : 0.15;
    const effOutRate = totComp > 0 ? (wCompCost / totComp) * 1e6 : 0.60;
    const effCacheRate = totCache > 0 ? (wCacheCost / totCache) * 1e6 : 0.03;

    const calcDailyUSD = (prompt, comp, cache) => {
      let c = Math.max(0, Math.min(cache, prompt));
      if (prompt > 0 && c / prompt < EST_CACHE_MIN_RATE) c = prompt * EST_CACHE_HIT;
      const plain = prompt - c;
      return (plain * effInRate + comp * effOutRate + c * effCacheRate) / 1e6;
    };

    const now = new Date();
    const days = [];
    let sumCny = 0;
    for (let i = 13; i >= 0; i--) {
      const key = localYMD(new Date(now.getTime() - i * 86400 * 1000));
      const m = dayMap[key] || { prompt: 0, comp: 0, cache: 0 };
      const usd = calcDailyUSD(m.prompt, m.comp, m.cache);
      const cny = usd * USD_CNY;
      sumCny += cny;
      days.push({ day: key, label: key.slice(5), usd, cny, tokens: m.prompt + m.comp + m.cache });
    }

    const anyData = days.some(d => d.cny > 0.01);
    if (!anyData) {
      wrap.innerHTML = `<div class="empty-chart">暂无按日 Token 数据，跑起业务后这里会显示每日白嫖节省金额（折合人民币）</div>`;
      return;
    }

    const avgCny = sumCny / 14;
    const maxCny = Math.max(...days.map(d => d.cny), avgCny * 1.3, 10);

    const W = 740, H = 220;
    const padLeft = 60, padRight = 15, padTop = 24, padBottom = 28;
    const chartW = W - padLeft - padRight;
    const chartH = H - padTop - padBottom;

    const cGridLine = "rgba(255, 255, 255, 0.05)";
    const cLabel = cssVar("--chart-label") || "rgba(255, 255, 255, 0.45)";
    const cAvg = "#f59e0b";

    const stepW = chartW / days.length;
    const barW = Math.max(16, Math.min(28, stepW * 0.58));
    const yOf = v => padTop + chartH - (v / maxCny) * chartH;

    let grid = "";
    [0, 0.5, 1].forEach(pct => {
      const v = maxCny * pct;
      const y = yOf(v);
      grid += `
        <line x1="${padLeft}" y1="${y}" x2="${W - padRight}" y2="${y}" stroke="${cGridLine}" stroke-width="1" stroke-dasharray="3,3" />
        <text x="${padLeft - 8}" y="${y + 4}" text-anchor="end" font-size="10" fill="${cLabel}" font-family="var(--mono)">¥${v.toFixed(1)}</text>
      `;
    });

    let bars = "";
    days.forEach((d, i) => {
      const x = padLeft + i * stepW + (stepW - barW) / 2;
      const barH = (d.cny / maxCny) * chartH;
      const y = padTop + chartH - barH;
      const tip = `${d.day}\n今日白嫖节省: ¥${d.cny.toFixed(2)} (折合 $${d.usd.toFixed(2)})\n当日 Token: ${fmtTokens(d.tokens)}`;
      bars += `
        <g class="chart-bar-group">
          <title>${esc(tip)}</title>
          <rect x="${x}" y="${y}" width="${barW}" height="${Math.max(3, barH)}" fill="url(#gradSavingsBar)" rx="6" />
          <text x="${x + barW / 2}" y="${H - 10}" text-anchor="middle" font-size="10" fill="${cLabel}" font-family="var(--mono)">${d.label}</text>
        </g>
      `;
    });

    const avgY = yOf(avgCny);
    const avgLine = `
      <g>
        <title>14 天日均节省: ¥${avgCny.toFixed(2)}</title>
        <line x1="${padLeft}" y1="${avgY}" x2="${W - padRight}" y2="${avgY}" stroke="${cAvg}" stroke-dasharray="4,4" stroke-width="1.2" opacity="0.85" />
        <text x="${W - padRight - 4}" y="${avgY - 6}" text-anchor="end" font-size="10" fill="${cAvg}" font-weight="600">14天日均: ¥${avgCny.toFixed(2)}</text>
      </g>
    `;

    wrap.innerHTML = `
      <div style="display:flex;justify-content:space-between;padding:0 8px 6px;font-size:11px;color:var(--muted);">
        <span>近 14 天累计节省: <b style="color:var(--ok);font-size:12px;">¥${sumCny.toFixed(2)}</b> (约 $${(sumCny / USD_CNY).toFixed(2)})</span>
        <span>14 天日均: <b style="color:${cAvg};font-size:12px;">¥${avgCny.toFixed(2)} / 天</b></span>
      </div>
      <svg viewBox="0 0 ${W} ${H}" style="width:100%; height:100%; display:block;" preserveAspectRatio="xMidYMid meet">
        <defs>
          <linearGradient id="gradSavingsBar" x1="0" y1="0" x2="0" y2="1">
            <stop offset="0%" stop-color="#F472B6" stop-opacity="0.95" />
            <stop offset="100%" stop-color="#FB923C" stop-opacity="0.75" />
          </linearGradient>
        </defs>
        ${grid}
        ${bars}
        ${avgLine}
      </svg>
    `;
  }

  // 5.6 资产续航估算
  function renderRunway() {
    const el = $("assetRunway");
    if (!el) return;

    const accounts = _lastAccounts || [];
    const now = new Date();
    const key = i => localYMD(new Date(now.getTime() - i * 86400 * 1000));

    const tokenAcc = accounts.filter(a => isTokenUnit(a.kind));
    const tokenBalance = tokenAcc.reduce((s, a) => s + Math.max(0, Number(a.remain || 0)), 0);
    const dayTok = {};
    tokenAcc.forEach(a => (a.tokens || []).forEach(t => {
      if (!t.day) return;
      dayTok[t.day] = (dayTok[t.day] || 0) + Number(t.total || 0) + Number(t.cache_read || 0);
    }));
    let tok7 = 0;
    for (let i = 0; i < 7; i++) tok7 += dayTok[key(i)] || 0;
    const avg = tok7 / 7;

    const creditAcc = accounts.filter(a => !isTokenUnit(a.kind));
    const creditBalance = creditAcc.reduce((s, a) => s + Math.max(0, Number(a.remain || 0)), 0);
    const cDelta = {};
    creditAcc.forEach(a => (a.daily || []).forEach(d => {
      if (!d.day) return;
      cDelta[d.day] = (cDelta[d.day] || 0) + Number(d.delta || 0);
    }));
    let credit7 = 0;
    for (let i = 0; i < 7; i++) credit7 += cDelta[key(i)] || 0;

    const ic = `<span class="runway-ic">${I.clock}</span>`;
    const lines = [];
    if (tokenBalance > 0 || avg > 0) {
      if (avg <= 0.5) {
        lines.push(`<div class="asset-runway">${ic}<b>Token 族</b>：现有 <b>${fmtTokens(tokenBalance)}</b> token，近 7 日消耗极低，可长期续航</div>`);
      } else {
        const days = Math.floor(tokenBalance / avg);
        lines.push(`<div class="asset-runway">${ic}<b>Token 族</b>：现有 <b>${fmtTokens(tokenBalance)}</b> token，近 7 日日均耗 <b>${fmtTokens(Math.round(avg))}</b>，预计续航约 <b>${days}</b> 天</div>`);
      }
    }
    if (creditBalance > 0 || credit7 !== 0) {
      const sign = credit7 >= 0 ? "净消耗" : "净入账";
      lines.push(`<div class="asset-runway">${ic}<b>积分族</b>：现有 <b>${fmtTotal(creditBalance)}</b> 积分，近 7 日${sign} <b>${fmtTotal(Math.abs(Math.round(credit7)))}</b>（含签到，无法折算 token）</div>`);
    }
    el.innerHTML = lines.length ? lines.join("") : `<div class="asset-runway">${ic}暂无足够的余额 / 消耗数据用于续航估算</div>`;
  }

  // 6. 账号 Token 生产力榜单（杜绝 Emoji，采用全站统一徽章与精美排版）
  function renderAcctProductivity(accounts) {
    const box = $("acctProductivityList");
    if (!box) return;
    if (!accounts || accounts.length === 0) {
      box.innerHTML = `<div class="empty-hint">暂无账号生产力数据</div>`;
      return;
    }

    const list = [...accounts].map(a => {
      let sumPrompt = 0, sumComp = 0, sumCache = 0, sumTotal = 0;
      (a.tokens || []).forEach(t => {
        sumPrompt += Number(t.prompt || 0);
        sumComp += Number(t.completion || 0);
        sumCache += Number(t.cache_read || 0);
        sumTotal += Number(t.total || 0);
      });
      const hitRate = (sumPrompt + sumCache) > 0 ? Math.round((sumCache / (sumPrompt + sumCache)) * 100) : null;
      return { ...a, sumTotal, sumCache, hitRate };
    });

    list.sort((a, b) => b.sumTotal - a.sumTotal);
    const grand = list.reduce((s, a) => s + a.sumTotal, 0) || 1;
    const max = list[0].sumTotal || 1;
    const topUid = list[0].uid;

    box.innerHTML = list.map((a, idx) => {
      const gm = groupMeta(a.kind);
      const exp = formatExpire(a.expire_at);
      const stateAcct = (state && state.accounts ? state.accounts : []).find(s => s.uid === a.uid && s.group === a.kind);
      const isNext = stateAcct && stateAcct.is_next;
      const bridgeDown = stateAcct && stateAcct.bridge_down;
      const sharePct = Math.round((a.sumTotal / grand) * 100);
      const barPct = Math.max(2, Math.round((a.sumTotal / max) * 100));

      // 纯净序号勋章，杜绝 Emoji，前三名赋予专属高亮样式
      const rankBadge = idx < 3
        ? `<span class="prod-rank top${idx + 1}">${idx + 1}</span>`
        : `<span class="prod-rank plain">${idx + 1}</span>`;

      const statusTag = bridgeDown
        ? `<span class="tag danger" title="该账号本地桥未启动，请求打到它会失败">${I.alert} 桥未启动</span>`
        : isNext
          ? `<span class="tag next-pick" title="下一个请求会优先调度它">${I.target} 优先 #1</span>`
          : (stateAcct && stateAcct.disabled ? `<span class="tag danger">已停用</span>` : "");
      const hitChip = a.hitRate !== null
        ? `<span class="prod-chip" title="缓存命中率 ${a.hitRate}%"><span style="color:var(--chart-cache)">◈</span> ${a.hitRate}%</span>`
        : "";

      return `
        <div class="prod-item ${a.uid === topUid ? "is-top" : ""}" title="${esc(a.uid)}">
          ${rankBadge}
          <div class="prod-main">
            <div class="prod-head">
              <span class="badge ${gm.cls}">${gm.name}</span>
              <b class="prod-name">${esc(a.nickname || shortUid(a.uid))}</b>
              <code class="prod-uid">${esc(shortUid(a.uid))}</code>
              ${statusTag}
              <span class="tag ${exp.cls}" title="最早到期时间">${I.clock} ${esc(exp.text)}</span>
              <span class="prod-share" title="占全池累计产出的比例">占池 ${sharePct}%</span>
            </div>
            <div class="prod-bar-bg">
              <div class="prod-bar-fill" style="width:${barPct}%"></div>
            </div>
          </div>
          <div class="prod-nums">
            <span class="prod-num-main" title="累计产出">${fmtTokens(a.sumTotal)}</span>
            <span class="prod-num-sub">今日 ${fmtTokens(a.today || 0)} ${hitChip}</span>
          </div>
        </div>
      `;
    }).join("");
  }

  // 导出到全局
  window.loadAssets = loadAssets;
  window.loadTokenAnalytics = loadTokenAnalytics;
  window.renderTokenHero = renderTokenHero;
  window.renderCreditSummary = renderCreditSummary;
  window.renderCreditTrend = renderCreditTrend;
  window.renderTokenTrend = renderTokenTrend;
  window.renderTokenHeatmap = renderTokenHeatmap;
  window.renderChannelDonut = renderChannelDonut;
  window.renderTopModels = renderTopModels;
  window.renderLowBalanceAccounts = renderLowBalanceAccounts;
  window.renderSavingsTrend = renderSavingsTrend;
  window.renderRunway = renderRunway;
  window.renderAcctProductivity = renderAcctProductivity;

})();
