// ==UserScript==
// @name         ZeroGate · Atria 羊毛全自动填表助手
// @namespace    https://github.com/jxylisty/ZeroGate
// @version      3.1.0
// @description  上海 AI 实验室 Atria (1亿Token) 终极自动化助手：拟人邮箱->QQ验证码秒填(GM穿透)->控制台自动创建API Key并自动写入ZeroGate
// @author       ZeroGate
// @match        https://api.atria-asi.ai/*
// @match        https://*.atria-asi.ai/*
// @grant        GM_xmlhttpRequest
// @connect      127.0.0.1
// @connect      localhost
// @run-at       document-idle
// ==/UserScript==

(function() {
  'use strict';

  let targetDomain = localStorage.getItem('zg_domain') || 'my123.bond';
  let defaultPassword = localStorage.getItem('zg_pwd') || 'ZeroGate@2026';
  let lastFilledCode = '';
  let currentEmail = localStorage.getItem('zg_current_email') || '';
  let consoleStep = 'idle';

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

  // 跨域穿透请求（使用油猴 GM_xmlhttpRequest 彻底突破 HTTPS 网页无法访问 HTTP 127.0.0.1 的 Mixed Content 限制）
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

  function fillVerificationCode(code) {
    if (!code || code.length !== 6) return false;
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
      } catch (e) {}
    }
    return false;
  }

  function createFloatingUI() {
    if (document.getElementById('zerogate-assistant')) return;

    const panel = document.createElement('div');
    panel.id = 'zerogate-assistant';
    panel.innerHTML = `
      <div id="zg-header" style="display:flex;justify-content:space-between;align-items:center;cursor:move;border-bottom:1px solid rgba(255,255,255,0.1);padding-bottom:8px;margin-bottom:10px;">
        <span style="font-weight:bold;color:#4ade80;font-size:13px;display:flex;align-items:center;gap:6px;">
          <span style="display:inline-block;width:8px;height:8px;background:#4ade80;border-radius:50%;box-shadow:0 0 8px #4ade80;"></span>
          ZeroGate · 羊毛流水线 v3.1
        </span>
        <button id="zg-toggle-btn" style="background:none;border:none;color:#94a3b8;cursor:pointer;font-size:14px;">−</button>
      </div>

      <div id="zg-body">
        <div style="margin-bottom:8px;">
          <label style="font-size:10px;color:#94a3b8;display:block;margin-bottom:2px;">专属域名后缀:</label>
          <input id="zg-domain-input" type="text" value="${targetDomain}" style="width:100%;box-sizing:border-box;background:#1e293b;border:1px solid #334155;color:#f8fafc;padding:4px 6px;border-radius:4px;font-size:11px;outline:none;" />
        </div>

        <div style="margin-bottom:8px;display:flex;gap:6px;">
          <button id="zg-fill-btn" style="flex:1.2;background:linear-gradient(135deg,#3b82f6,#2563eb);color:#fff;border:none;padding:8px 10px;border-radius:6px;font-size:11px;font-weight:600;cursor:pointer;box-shadow:0 2px 4px rgba(0,0,0,0.2);">
            🎲 填拟人邮箱
          </button>
          <button id="zg-read-code-btn" style="flex:0.8;background:#0284c7;color:#fff;border:none;padding:8px 6px;border-radius:6px;font-size:11px;font-weight:600;cursor:pointer;">
            📩 手动填码
          </button>
        </div>

        <div id="zg-status" style="font-size:11px;color:#cbd5e1;background:#0f172a;border-radius:4px;padding:6px 8px;margin-bottom:6px;line-height:1.4;word-break:break-all;border:1px dashed #334155;min-height:42px;">
          <span style="color:#38bdf8;">🟢 穿透监听服务就绪</span><br>
          <span style="color:#94a3b8;font-size:10px;">滑块过完后，验证码秒填</span>
        </div>
      </div>
    `;

    Object.assign(panel.style, {
      position: 'fixed',
      top: '20px',
      right: '20px',
      zIndex: '999999',
      width: '240px',
      backgroundColor: 'rgba(15, 23, 42, 0.94)',
      backdropFilter: 'blur(8px)',
      border: '1px solid #334155',
      borderRadius: '10px',
      boxShadow: '0 10px 25px rgba(0,0,0,0.5)',
      padding: '12px',
      color: '#f8fafc',
      fontFamily: '-apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, "Helvetica Neue", Arial, sans-serif'
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
      localStorage.setItem('zg_domain', targetDomain);
    };

    const statusEl = panel.querySelector('#zg-status');

    panel.querySelector('#zg-fill-btn').onclick = () => {
      const email = generateHumanEmail(targetDomain);
      currentEmail = email;
      localStorage.setItem('zg_current_email', email);
      consoleStep = 'idle';

      const input = document.querySelector('input[name="identifier"]') ||
                    document.querySelector('input[type="email"]') ||
                    document.querySelector('input[autocomplete="username"]') ||
                    document.querySelector('input[placeholder*="email" i]') ||
                    document.querySelector('input[placeholder*="邮箱" i]') ||
                    document.querySelector('input:not([type="hidden"]):not([type="checkbox"]):not([type="password"])');

      if (!input) {
        statusEl.innerHTML = `<span style="color:#f87171;">⚠️ 未在登录页面找到邮箱输入框</span>`;
        return;
      }

      input.focus();
      setNativeInputValue(input, email);
      try { navigator.clipboard.writeText(email); } catch (e) {}

      statusEl.innerHTML = `
        <div style="color:#4ade80;font-weight:bold;margin-bottom:2px;">✅ 成功填入拟人邮箱:</div>
        <div style="color:#38bdf8;font-size:11px;">${email}</div>
        <div style="color:#fde047;font-size:10px;margin-top:4px;">👉 请点击【继续】并拖动滑块通过！</div>
      `;
    };

    panel.querySelector('#zg-read-code-btn').onclick = async () => {
      try {
        const data = await requestLocal('http://127.0.0.1:7864/api/code');
        if (data && data.code) {
          fillVerificationCode(data.code);
          statusEl.innerHTML = `<span style="color:#4ade80;">✅ 手动填入验证码: <b>${data.code}</b></span>`;
        } else {
          statusEl.innerHTML = `<span style="color:#f87171;">未获取到验证码</span>`;
        }
      } catch (e) {
        statusEl.innerHTML = `<span style="color:#f87171;">连接本地服务失败: ${e.message || e}</span>`;
      }
    };

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

  // 控制台自动化：自动创建 Key 并自动写入 ZeroGate auths
  async function handleConsoleAutomation() {
    const statusEl = document.getElementById('zg-status');

    // 1. 寻找 "+ 创建密钥"
    const createKeyBtn = Array.from(document.querySelectorAll('button')).find(b => {
      const t = b.innerText || '';
      return t.includes('创建密钥') && !t.includes('已创建') && !b.closest('[role="dialog"]') && !b.closest('.modal');
    });

    // 2. 寻找命名输入框
    const nameInput = document.querySelector('input[placeholder*="本地开发"]');
    const inModalConfirmBtn = Array.from(document.querySelectorAll('button')).find(b => {
      return (b.innerText || '').trim() === '创建密钥' && b !== createKeyBtn;
    });

    // 3. 寻找已创建 Key 弹窗
    const keyMatch = document.body.innerText.match(/atr_[a-zA-Z0-9_-]{25,}/);
    const savedBtn = Array.from(document.querySelectorAll('button')).find(b => {
      return (b.innerText || '').includes('我已保存密钥');
    });

    if (createKeyBtn && !nameInput && !keyMatch && consoleStep === 'idle') {
      consoleStep = 'creating_key';
      if (statusEl) statusEl.innerHTML = `<span style="color:#38bdf8;">⚡ 自动点击【+ 创建密钥】...</span>`;
      createKeyBtn.click();
      return;
    }

    if (nameInput && inModalConfirmBtn && consoleStep === 'creating_key') {
      consoleStep = 'submitting_name';
      if (statusEl) statusEl.innerHTML = `<span style="color:#38bdf8;">✍️ 自动填入密钥名称并提交...</span>`;
      setNativeInputValue(nameInput, 'ZeroGate');
      setTimeout(() => {
        inModalConfirmBtn.click();
      }, 400);
      return;
    }

    if (keyMatch && savedBtn && (consoleStep === 'submitting_name' || consoleStep === 'creating_key' || consoleStep === 'idle')) {
      const apiKey = keyMatch[0];
      consoleStep = 'done';
      if (statusEl) {
        statusEl.innerHTML = `
          <div style="color:#4ade80;font-weight:bold;">🎉 截获 Key:</div>
          <div style="color:#f59e0b;font-size:10px;font-family:monospace;">${apiKey.slice(0, 14)}...</div>
          <div style="color:#a78bfa;font-size:10px;">✅ 已自动写入 src-true/auths 目录！</div>
          <div style="color:#38bdf8;font-size:10px;margin-top:2px;">👉 请手动点击左下角退出账号。</div>
        `;
      }

      // 保存到本地
      try {
        await requestLocal('http://127.0.0.1:7864/api/save-key', 'POST', {
          apiKey: apiKey,
          email: currentEmail || 'atria-' + Date.now().toString().slice(-4)
        });
      } catch (e) {}

      setTimeout(() => {
        savedBtn.click();
      }, 500);
      return;
    }
  }

  // 探针循环
  setInterval(async () => {
    // 0. 如果页面处于输入密码界面（老号无密码），自动点击“验证码登录”
    const switchToPasscodeBtn = Array.from(document.querySelectorAll('a, button, span')).find(el => {
      const t = (el.innerText || '').trim();
      return (/验证码登录|通过验证码|Sign in with verification code|via passcode/i.test(t)) && el.offsetHeight > 0;
    });
    if (switchToPasscodeBtn) {
      switchToPasscodeBtn.click();
    }

    // 1. 验证码检测
    try {
      const data = await requestLocal('http://127.0.0.1:7864/api/code');
      if (data && data.code && data.code !== lastFilledCode && data.age_seconds <= 180) {
        const ok = fillVerificationCode(data.code);
        if (ok) {
          lastFilledCode = data.code;
          const statusEl = document.getElementById('zg-status');
          if (statusEl) {
            statusEl.innerHTML = `
              <div style="color:#4ade80;font-weight:bold;">🎉 自动捕获验证码:</div>
              <div style="color:#f59e0b;font-size:16px;font-weight:bold;letter-spacing:2px;">${data.code}</div>
              <div style="color:#94a3b8;font-size:10px;">已秒填！正在自动登录...</div>
            `;
          }
          setTimeout(() => {
            const submitBtn = Array.from(document.querySelectorAll('button')).find(b => /确认|登录|Sign in|Continue|下一步/i.test(b.innerText)) || document.querySelector('button[type="submit"]');
            if (submitBtn) submitBtn.click();
          }, 300);
        }
      }
    } catch (e) {}

    // 2. 控制台自动化
    handleConsoleAutomation();

  }, 1000);

})();
