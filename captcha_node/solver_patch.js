const { JSDOM, VirtualConsole } = require('jsdom');
const SCENE = process.argv[2] || '11xygtvd';
const REGION = process.argv[3] || 'sgp';
const PREFIX = process.argv[4] || 'no8xfe';

const vc = new VirtualConsole();  // 静默 jsdom 噪声
const html = `<!DOCTYPE html><html><head></head><body>
<div id="cap"></div><button id="btn"></button>
<script src="https://o.alicdn.com/captcha-frontend/aliyunCaptcha/AliyunCaptcha.js"></script>
</body></html>`;

const dom = new JSDOM(html, {
  url: 'https://zcode.z.ai/',
  runScripts: 'dangerously',
  resources: 'usable',
  pretendToBeVisual: true,
  virtualConsole: vc,
  beforeParse(window) {
    // ── 网络观测：记录 XHR/fetch 目标与状态码到 net.log ──
    const fs = require('fs');
    const netLog = [];
    const logNet = (s) => { netLog.push(s); fs.appendFileSync(require('path').join(process.cwd(), 'net.log'), s + '\n'); };
    const OXHR = window.XMLHttpRequest;
    window.XMLHttpRequest = class extends OXHR {
      open(m, u) { this.__u = u; this.__m = m; return super.open(m, u); }
      send(b) {
        const self = this;
        this.addEventListener('loadend', () => { try {
          const t = String(self.responseText || '');
          logNet(this.__m + ' ' + this.__u + ' -> ' + self.status + ' SEND=' + String(b || '').slice(0, 260) + ' BODY=' + t.slice(0, 220));
        } catch (e) {} });
        return super.send(b);
      }
    };
    const ofetch = window.fetch;
    window.fetch = function (u, o) {
      const p = ofetch.apply(this, arguments);
      p.then((r) => logNet('FETCH ' + u + ' -> ' + (r && r.status)), (e) => logNet('FETCH ' + u + ' ERR ' + e));
      return p;
    };
    // ── 反自动化检测桩 ──
    Object.defineProperty(window.navigator, 'webdriver', { value: false });
    Object.defineProperty(window.navigator, 'languages', { value: ['zh-CN', 'zh', 'en'] });
    Object.defineProperty(window.navigator, 'maxTouchPoints', { value: 0 });
    Object.defineProperty(window.navigator, 'hardwareConcurrency', { value: 8 });
    Object.defineProperty(window.navigator, 'deviceMemory', { value: 8 });
    Object.defineProperty(window.navigator, 'platform', { value: 'Win32' });
    window.chrome = { runtime: {}, loadTimes() {}, csi() {} };
    window.devicePixelRatio = 1;
    window.screen = { width: 1920, height: 1080, availWidth: 1920, availHeight: 1080, colorDepth: 24, pixelDepth: 24 };
    window.Notification = function () { this.permission = 'denied'; };
    window.Notification.permission = 'denied';
    window.requestIdleCallback = (cb) => setTimeout(cb, 0);
    window.cancelIdleCallback = (id) => clearTimeout(id);
    // getBoundingClientRect 桩：SDK 定位弹层/按钮必需非零矩形
    window.Element.prototype.getBoundingClientRect = function () {
      const w = (this.clientWidth || 200), h = (this.clientHeight || 40);
      return { x: 0, y: 0, left: 0, top: 0, right: w, bottom: h, width: w, height: h, toJSON(){} };
    };
    Object.defineProperty(window.HTMLElement.prototype, 'offsetLeft', { get() { return 0; } });
    Object.defineProperty(window.HTMLElement.prototype, 'offsetTop', { get() { return 0; } });
    Object.defineProperty(window.HTMLElement.prototype, 'clientWidth', { get() { return 200; } });
    Object.defineProperty(window.HTMLElement.prototype, 'clientHeight', { get() { return 40; } });
    // PointerEvent 桩（SDK 挂监听，jsdom 无 PointerEvent）
    if (!window.PointerEvent) window.PointerEvent = window.MouseEvent || class { constructor(t, o){} };
    // 剪贴板 / 存储
    window.URL.createObjectURL = () => 'blob:zcode';
    try { window.localStorage.setItem('test', '1'); window.localStorage.removeItem('test'); } catch (e) {}

    window.matchMedia = () => ({ matches:false, media:'', onchange:null, addListener(){}, removeListener(){}, addEventListener(){}, removeEventListener(){}, dispatchEvent(){return false;} });
    // canvas / webgl 指纹桩：返回稳定值即可
    const proto = window.HTMLCanvasElement.prototype;
    proto.getContext = function (type) {
      if (/webgl/i.test(type)) return { canvas:this, getParameter:()=>'Intel', getExtension:()=>null, getSupportedExtensions:()=>['WEBGL_debug_renderer_info'], getContextAttributes:()=>({}), getShaderPrecisionFormat:()=>({precision:23,rangeMin:127,rangeMax:127}) };
      return { canvas:this, fillRect(){}, clearRect(){}, getImageData:(x,y,w=1,h=1)=>({data:new Uint8ClampedArray(w*h*4)}), putImageData(){}, createImageData:(w=1,h=1)=>({data:new Uint8ClampedArray(w*h*4)}), setTransform(){}, transform(){}, drawImage(){}, save(){}, restore(){}, beginPath(){}, moveTo(){}, lineTo(){}, bezierCurveTo(){}, quadraticCurveTo(){}, closePath(){}, clip(){}, stroke(){}, fill(){}, arc(){}, rect(){}, ellipse(){}, translate(){}, scale(){}, rotate(){}, fillText(){}, strokeText(){}, measureText:(t)=>({width:(''+t).length*8}), createLinearGradient:()=>({addColorStop(){}}), createRadialGradient:()=>({addColorStop(){}}), createPattern:()=>({}), isPointInPath:()=>false, font:'10px sans-serif', textBaseline:'alphabetic', textAlign:'start', fillStyle:'#000', strokeStyle:'#000', globalAlpha:1, lineWidth:1, shadowBlur:0, shadowColor:'' };
    };
    proto.toDataURL = () => 'data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8z8BQDwAEhQGAhKmMIQAAAABJRU5ErkJggg==';
    proto.toBlob = (cb) => cb && cb(null);
    // Worker 桩
    window.Worker = class { constructor(){} postMessage(){} terminate(){} addEventListener(){} removeEventListener(){} onmessage=null; onerror=null; };
    window.OffscreenCanvas = window.OffscreenCanvas || class { constructor(w,h){this.width=w;this.height=h;} getContext(){return proto.getContext.call(this);} };
  },
});
const { window } = dom;

function waitFor(cond, t = 12000) {
  return new Promise((res, rej) => {
    const s = Date.now();
    const i = setInterval(() => { let ok=false; try{ok=cond();}catch{} if(ok){clearInterval(i);res();} else if(Date.now()-s>t){clearInterval(i);rej(new Error('timeout'));} }, 80);
  });
}

(async () => {
  await waitFor(() => typeof window.initAliyunCaptcha === 'function');
  window.initAliyunCaptcha({
    SceneId: SCENE, mode: 'popup', region: REGION, prefix: PREFIX,
    element: '#cap', button: '#btn', captchaLogoImg: '', showErrorTip: false,
    getInstance: (inst) => { try { (inst.startTracelessVerification || inst.show).call(inst); } catch (e) { console.error('start', e.message); } },
    success: (param) => { console.log('VERIFY_PARAM=' + param); process.exit(0); },
    fail: (c) => { console.error('FAIL=' + JSON.stringify(c && c.verifyCode)); process.exit(4); },
    onError: () => process.exit(5),
  });
  setTimeout(() => process.exit(2), 25000);
})().catch(() => process.exit(3));