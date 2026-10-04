// ==UserScript==
// @name         ZeroGate · Cline 账号一键注册与授权助手
// @namespace    https://github.com/jxylisty/ZeroGate
// @version      1.0.0
// @description  Cline/WorkOS 拟人邮箱自动填入 -> 本地 7864 端口 QQ 邮件验证码秒填 -> 自动确认授权
// @author       ZeroGate
// @match        https://authkit.cline.bot/*
// @match        https://app.cline.bot/*
// @match        https://*.workos.com/*
// @grant        GM_xmlhttpRequest
// @connect      127.0.0.1
// @connect      localhost
// @run-at       document-idle
// ==/UserScript==

(function() {
  'use strict';

  let targetDomain = localStorage.getItem('zg_cline_domain') || 'my123.bond';
  let lastFilledCode = '';
  let currentEmail = localStorage.getItem('zg_cline_email') || '';

  const FIRST_NAMES = [
    'james', 'oliver', 'lucas', 'ethan', 'alex', 'david', 'leo', 'mason', 'noah', 'william',
    'henry', 'daniel', 'jack', 'samuel', 'benjamin', 'logan', 'matthew', 'thomas', 'nathan', 'ryan',
    'emma', 'olivia', 'sophia', 'mia', 'charlotte', 'amelia', 'harper', 'evelyn', 'abigail', 'emily',
    'ella', 'elizabeth', 'camila', 'luna', 'sofia', 'avery', 'mila', 'aria', 'scarlett', 'chloe'
  ];

  const LAST_NAMES = [
    'smith', 'johnson', 'williams', 'brown', 'jones', 'garcia', 'miller', 'davis', 'rodriguez', 'martinez',
    'hernandez', 'lopez', 'gonzalez', 'wilson', 'anderson', 'thomas', 'taylor', 'moore', 'jackson', 'martin',
    'lee', 'perez', 'thompson', 'white', 'harris', 'sanchez', 'clark', 'ramirez', 'lewis', 'robinson',
    'walker', 'young', 'allen', 'king', 'wright', 'scott', 'torres', 'nguyen', 'hill', 'flores'
  ];

  // 跨域穿透请求本地 7864 邮件服务（绕过 Mixed Content 限制）
  function requestLocal(url, method = 'GET', data = null) {
    return new Promise((resolve, reject) => {
      if (typeof GM_xmlhttpRequest !== 'undefined') {
        GM_xmlhttpRequest({
          method: method,
          url: url,
          headers: data ? { 'Content-Type': 'application/json' } : {},
          data: data ? JSON.stringify(data) : undefined,
          onload: function(res) {
            try {
              resolve(JSON.parse(res.responseText));
            } catch (e) {
              resolve(res.responseText);
            }
          },
          onerror: function(err) {
            reject(err);
          }
        });
      } else {
        fetch(url, {
          method: method,
          headers: data ? { 'Content-Type': 'application/json' } : {},
          body: data ? JSON.stringify(data) : undefined
        }).then(r => r.json()).then(resolve).catch(reject);
      }
    });
  }

  // 生成真实感拟人邮箱（例如 ethan.miller48@my123.bond）
  function generateHumanEmail(domain) {
    const fn = FIRST_NAMES[Math.floor(Math.random() * FIRST_NAMES.length)];
    const ln = LAST_NAMES[Math.floor(Math.random() * LAST_NAMES.length)];
    const num = Math.floor(10 + Math.random() * 90);
    const styles = [
      `${fn}.${ln}${num}`,
      `${fn}_${ln}${num}`,
      `${fn}${ln}${num}`,
      `${ln}.${fn}${num}`
    ];
    return `${styles[Math.floor(Math.random() * styles.length)]}@${domain}`;
  }

  // React 受控组件输入穿透（利用原生原型 setter 派发完整事件）
  function setNativeInputValue(element, value) {
    if (!element) return;
    const valueSetter = Object.getOwnPropertyDescriptor(element, 'value')?.set;
    const prototype = Object.getPrototypeOf(element);
    const prototypeValueSetter = Object.getOwnPropertyDescriptor(prototype, 'value')?.set;

    if (prototypeValueSetter && valueSetter !== prototypeValueSetter) {
      prototypeValueSetter.call(element, value);
    } else if (valueSetter) {
      valueSetter.call(element, value);
    } else {
      element.value = value;
    }

    element.dispatchEvent(new Event('input', { bubbles: true, cancelable: true }));
    element.dispatchEvent(new Event('change', { bubbles: true, cancelable: true }));
    element.dispatchEvent(new Event('blur', { bubbles: true, cancelable: true }));
  }

  // 6 位验证码多格/单格兼容填充
  function fillVerificationCode(code) {
    if (!code || code.length !== 6) return false;
    
    // 匹配 6 格分体输入框 (WorkOS / AuthKit 常用)
    const inputs = Array.from(document.querySelectorAll('input')).filter(el => {
      return (el.offsetWidth > 0 || el.offsetHeight > 0) &&
             (el.maxLength === 1 || el.getAttribute('inputmode') === 'numeric' || el.getAttribute('autocomplete') === 'one-time-code');
    });

    if (inputs.length >= 6) {
      for (let i = 0; i < 6; i++) {
        setNativeInputValue(inputs[i], code[i]);
      }
      return true;
    }

    // 匹配单个验证码输入框
    const singleInput = document.querySelector('input[name*="code" i], input[name*="passcode" i], input[placeholder*="code" i], input[placeholder*="验证码" i]');
    if (singleInput) {
      setNativeInputValue(singleInput, code);
      return true;
    }

    const targetInput = document.activeElement || inputs[0] || document.querySelector('input:not([type="hidden"])');
    if (targetInput) {
      try {
        targetInput.focus();
        const dt = new DataTransfer();
        dt.setData('text/plain', code);
        targetInput.dispatchEvent(new ClipboardEvent('paste', { clipboardData: dt, bubbles: true, cancelable: true }));
        return true;
      } catch (e) {}
    }
    return false;
  }

  // 创建悬浮控制台
  function createFloatingUI() {
    if (document.getElementById('zerogate-cline-assistant')) return;

    const panel = document.createElement('div');
    panel.id = 'zerogate-cline-assistant';
    panel.innerHTML = `
      <div id="zg-header" style="display:flex;justify-content:space-between;align-items:center;cursor:move;border-bottom:1px solid rgba(255,255,255,0.1);padding-bottom:8px;margin-bottom:10px;">
        <span style="font-weight:bold;color:#38bdf8;font-size:13px;display:flex;align-items:center;gap:6px;">
          <span style="display:inline-block;width:8px;height:8px;background:#38bdf8;border-radius:50%;box-shadow:0 0 8px #38bdf8;"></span>
          Cline · 极速注册助手 v1.0
        </span>
        <button id="zg-toggle-btn" style="background:none;border:none;color:#94a3b8;cursor:pointer;font-size:14px;">−</button>
      </div>

      <div id="zg-body">
        <div style="margin-bottom:8px;">
          <label style="font-size:10px;color:#94a3b8;display:block;margin-bottom:2px;">域名后缀 (Cloudflare 转发):</label>
          <input id="zg-domain-input" type="text" value="${targetDomain}" style="width:100%;box-sizing:border-box;background:#1e293b;border:1px solid #334155;color:#f8fafc;padding:4px 6px;border-radius:4px;font-size:11px;outline:none;" />
        </div>

        <div style="margin-bottom:8px;display:flex;gap:6px;">
          <button id="zg-fill-btn" style="flex:1.2;background:linear-gradient(135deg,#0284c7,#0369a1);color:#fff;border:none;padding:8px 10px;border-radius:6px;font-size:11px;font-weight:600;cursor:pointer;box-shadow:0 2px 4px rgba(0,0,0,0.2);">
            🎲 填拟人邮箱
          </button>
          <button id="zg-read-code-btn" style="flex:0.8;background:#334155;color:#cbd5e1;border:none;padding:8px 6px;border-radius:6px;font-size:11px;font-weight:600;cursor:pointer;">
            📩 读本地码
          </button>
        </div>

        <div id="zg-status" style="font-size:11px;color:#cbd5e1;background:#0f172a;border-radius:4px;padding:6px 8px;margin-bottom:6px;line-height:1.4;word-break:break-all;border:1px dashed #334155;min-height:42px;">
          <span style="color:#38bdf8;">🟢 7864 邮件监听服务挂载中</span><br>
          <span style="color:#94a3b8;font-size:10px;">点击填邮箱后，验证码将秒填</span>
        </div>
      </div>
    `;

    Object.assign(panel.style, {
      position: 'fixed',
      top: '30px',
      right: '30px',
      zIndex: '9999999',
      width: '240px',
      backgroundColor: 'rgba(15, 23, 42, 0.95)',
      backdropFilter: 'blur(8px)',
      border: '1px solid #38bdf8',
      borderRadius: '10px',
      boxShadow: '0 10px 25px rgba(0,0,0,0.5)',
      padding: '12px',
      color: '#f8fafc',
      fontFamily: '-apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, sans-serif'
    });

    document.body.appendChild(panel);

    const toggleBtn = panel.querySelector('#zg-toggle-btn');
    const body = panel.querySelector('#zg-body');
    let isCollapsed = false;
    toggleBtn.onclick = () => {
      isCollapsed = !isCollapsed;
      body.style.display = isCollapsed ? 'none' : 'block';
      toggleBtn.innerText = isCollapsed ? '+' : '−';
    };

    const domainInput = panel.querySelector('#zg-domain-input');
    domainInput.onchange = () => {
      targetDomain = domainInput.value.trim() || 'my123.bond';
      localStorage.setItem('zg_cline_domain', targetDomain);
    };

    const statusEl = panel.querySelector('#zg-status');

    // 填邮箱按钮
    panel.querySelector('#zg-fill-btn').onclick = () => {
      const email = generateHumanEmail(targetDomain);
      currentEmail = email;
      localStorage.setItem('zg_cline_email', email);

      const input = document.querySelector('input[type="email"]') ||
                    document.querySelector('input[name="email"]') ||
                    document.querySelector('input[name="identifier"]') ||
                    document.querySelector('input[autocomplete="username"]') ||
                    document.querySelector('input[placeholder*="email" i]') ||
                    document.querySelector('input[placeholder*="邮箱" i]') ||
                    document.querySelector('input:not([type="hidden"]):not([type="password"])');

      if (!input) {
        statusEl.innerHTML = `<span style="color:#f87171;">⚠️ 未找到邮箱输入框</span>`;
        return;
      }

      input.focus();
      setNativeInputValue(input, email);
      try { navigator.clipboard.writeText(email); } catch (e) {}

      statusEl.innerHTML = `
        <div style="color:#38bdf8;font-weight:bold;margin-bottom:2px;">✅ 已填入拟人邮箱:</div>
        <div style="color:#fde047;font-size:11px;">${email}</div>
        <div style="color:#94a3b8;font-size:10px;margin-top:4px;">👉 请点击【Continue】发送验证码！</div>
      `;

      // 尝试自动点击提交/下一步
      setTimeout(() => {
        const nextBtn = Array.from(document.querySelectorAll('button')).find(b => {
          const t = (b.innerText || '').trim();
          return /^(Continue|Sign in with Email|Next|继续|下一步|提交)$/i.test(t);
        }) || document.querySelector('button[type="submit"]');
        if (nextBtn) {
          nextBtn.click();
        }
      }, 500);
    };

    // 手动抓码
    panel.querySelector('#zg-read-code-btn').onclick = async () => {
      try {
        const data = await requestLocal('http://127.0.0.1:7864/api/code');
        if (data && data.code) {
          fillVerificationCode(data.code);
          statusEl.innerHTML = `<span style="color:#4ade80;">✅ 手动填入验证码: <b>${data.code}</b></span>`;
        } else {
          statusEl.innerHTML = `<span style="color:#f87171;">未获取到有效验证码</span>`;
        }
      } catch (e) {
        statusEl.innerHTML = `<span style="color:#f87171;">连接本地服务失败 (7864)</span>`;
      }
    };

    // 拖拽窗口
    const header = panel.querySelector('#zg-header');
    let isDragging = false, startX, startY, origX, origY;
    header.onmousedown = (e) => {
      if (e.target.tagName === 'BUTTON' || e.target.tagName === 'INPUT') return;
      isDragging = true;
      startX = e.clientX; startY = e.clientY;
      origX = panel.offsetLeft; origY = panel.offsetTop;
      document.onmousemove = (e) => {
        if (!isDragging) return;
        panel.style.right = 'auto';
        panel.style.left = (origX + e.clientX - startX) + 'px';
        panel.style.top = (origY + e.clientY - startY) + 'px';
      };
      document.onmouseup = () => {
        isDragging = false;
        document.onmousemove = null;
        document.onmouseup = null;
      };
    };
  }

  window.addEventListener('load', createFloatingUI);

  // 探针循环：秒级侦测验证码与授权确认按钮
  setInterval(async () => {
    createFloatingUI();

    // 1. 自动填入 7864 抓取到的最新验证码
    try {
      const data = await requestLocal('http://127.0.0.1:7864/api/code');
      if (data && data.code && data.code !== lastFilledCode && data.age_seconds <= 180) {
        const ok = fillVerificationCode(data.code);
        if (ok) {
          lastFilledCode = data.code;
          const statusEl = document.getElementById('zg-status');
          if (statusEl) {
            statusEl.innerHTML = `
              <div style="color:#4ade80;font-weight:bold;">🎉 捕获最新验证码:</div>
              <div style="color:#38bdf8;font-size:16px;font-weight:bold;letter-spacing:2px;">${data.code}</div>
              <div style="color:#94a3b8;font-size:10px;">已秒填！正在自动完成验证...</div>
            `;
          }
          setTimeout(() => {
            const submitBtn = Array.from(document.querySelectorAll('button')).find(b => 
              /^(Continue|Verify|Sign in|确认|登录|下一步)$/i.test((b.innerText || '').trim())
            ) || document.querySelector('button[type="submit"]');
            if (submitBtn) submitBtn.click();
          }, 300);
        }
      }
    } catch (e) {}

    // 2. 自动检测并点击授权确认按钮 (Authorize / Allow / Confirm)
    const authorizeBtn = Array.from(document.querySelectorAll('button, a')).find(el => {
      const t = (el.innerText || '').trim();
      return /^(Authorize|Allow|Confirm|Accept|同意授权|授权|允许)$/i.test(t) && el.offsetHeight > 0;
    });

    if (authorizeBtn) {
      const statusEl = document.getElementById('zg-status');
      if (statusEl) {
        statusEl.innerHTML = `<span style="color:#4ade80;">⚡ 检测到授权页面，自动点击确认授权...</span>`;
      }
      setTimeout(() => {
        authorizeBtn.click();
      }, 500);
    }

  }, 1000);

})();
