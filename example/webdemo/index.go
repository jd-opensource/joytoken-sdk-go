package main

// indexHTML is the single-page UI served at "/". It renders a chat box and an
// "Enable Tools" switch that maps directly to the backend routing:
//
//	ON  -> agent SDK with default tools
//	OFF -> bare client, plain chat
const indexHTML = `<!DOCTYPE html>
<html lang="zh">
<head>
<meta charset="utf-8" />
<meta name="viewport" content="width=device-width, initial-scale=1" />
<title>JoyToken SDK · 默认带 Tools + 可降级 Demo</title>
<style>
  body { font-family: -apple-system, system-ui, sans-serif; max-width: 720px; margin: 40px auto; padding: 0 16px; color: #1a1a1a; }
  h1 { font-size: 20px; }
  .sub { color: #666; font-size: 13px; margin-bottom: 20px; }
  .switch-row { display: flex; align-items: center; gap: 10px; margin: 16px 0; padding: 12px 14px; background: #f5f5f7; border-radius: 10px; }
  .switch { position: relative; width: 46px; height: 26px; }
  .switch input { opacity: 0; width: 0; height: 0; }
  .slider { position: absolute; cursor: pointer; inset: 0; background: #ccc; border-radius: 26px; transition: .2s; }
  .slider:before { content: ""; position: absolute; height: 20px; width: 20px; left: 3px; bottom: 3px; background: #fff; border-radius: 50%; transition: .2s; }
  input:checked + .slider { background: #0a84ff; }
  input:checked + .slider:before { transform: translateX(20px); }
  .path-tag { font-size: 12px; padding: 2px 8px; border-radius: 6px; background: #e5e5ea; color: #333; }
  #log { border: 1px solid #e0e0e0; border-radius: 10px; padding: 12px; min-height: 200px; margin: 16px 0; }
  .msg { margin: 8px 0; padding: 8px 12px; border-radius: 8px; white-space: pre-wrap; }
  .me { background: #0a84ff; color: #fff; text-align: right; }
  .bot { background: #f0f0f2; }
  .meta { font-size: 11px; color: #888; margin-top: 2px; }
  .input-row { display: flex; gap: 8px; }
  #text { flex: 1; padding: 10px; border: 1px solid #ccc; border-radius: 8px; font-size: 14px; }
  button { padding: 10px 18px; border: none; background: #0a84ff; color: #fff; border-radius: 8px; cursor: pointer; }
  button:disabled { opacity: .5; cursor: default; }
</style>
</head>
<body>
  <h1>JoyTokenSDK · 默认带 Tools + 可降级</h1>
  <div class="sub">开关决定后端走哪条路径:开 = agent SDK(带默认工具、自动执行);关 = 裸 client(纯对话)。未设 API Key 时为 MOCK 模式。</div>

  <div class="switch-row">
    <label class="switch">
      <input type="checkbox" id="toolsSwitch" checked />
      <span class="slider"></span>
    </label>
    <span>启用 Tools</span>
    <span class="path-tag" id="pathTag">当前:agent + 默认工具</span>
  </div>

  <div id="log"></div>

  <div class="input-row">
    <input id="text" placeholder="输入消息,回车发送…" />
    <button id="send">发送</button>
  </div>

<script>
  const sw = document.getElementById('toolsSwitch');
  const pathTag = document.getElementById('pathTag');
  const log = document.getElementById('log');
  const text = document.getElementById('text');
  const send = document.getElementById('send');

  function refreshTag() {
    pathTag.textContent = sw.checked ? '当前:agent + 默认工具' : '当前:裸 client 纯对话';
  }
  sw.addEventListener('change', refreshTag);
  refreshTag();

  function append(cls, content, meta) {
    const div = document.createElement('div');
    div.className = 'msg ' + cls;
    div.textContent = content;
    log.appendChild(div);
    if (meta) {
      const m = document.createElement('div');
      m.className = 'meta';
      m.textContent = meta;
      log.appendChild(m);
    }
    log.scrollTop = log.scrollHeight;
  }

  async function submit() {
    const message = text.value.trim();
    if (!message) return;
    append('me', message);
    text.value = '';
    send.disabled = true;
    try {
      const res = await fetch('/api/chat', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ message, enableTools: sw.checked }),
      });
      const data = await res.json();
      append('bot', data.reply, 'path=' + data.path + ' · tools=' + data.tools);
    } catch (e) {
      append('bot', '请求失败:' + e);
    } finally {
      send.disabled = false;
      text.focus();
    }
  }

  send.addEventListener('click', submit);
  text.addEventListener('keydown', (e) => { if (e.key === 'Enter') submit(); });
</script>
</body>
</html>`
