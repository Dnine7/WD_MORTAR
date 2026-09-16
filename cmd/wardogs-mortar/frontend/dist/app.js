const $ = (selector) => document.querySelector(selector);
let controller;

const calibration = {
  chatInput: {x: 0, y: 0, w: 1, h: 1},
  mapRegion: {x: 0, y: 0, w: 1, h: 1},
  overlayXPercent: 100,
  overlayYPercent: 0
};

function formatCoordinate(value) {
  return value ? `X ${value.x.toFixed(2)} · Y ${value.y.toFixed(2)}` : '—';
}

function formatRect(rect) {
  return `X ${rect.x} · Y ${rect.y} · W ${rect.w} · H ${rect.h}`;
}

function render(state) {
  $('#currentCoordinate').textContent = formatCoordinate(state.current);
  $('#currentSource').textContent = state.current ? `来源：${state.current.source}` : '等待记录';
  $('#targetCoordinate').textContent = formatCoordinate(state.target);
  $('#targetSource').textContent = state.target ? `来源：${state.target.source}` : '等待记录';
  $('#distanceValue').textContent = state.distance == null ? '—' : state.distance.toFixed(0);
  $('#overlayToggle').checked = !!state.overlayVisible;

  $('#ocrDot').className = `state-dot ${state.ocrAvailable ? 'ok' : 'error'}`;
  $('#ocrState').textContent = state.ocrAvailable ? '内置离线 OCR 就绪' : '离线 OCR 不可用';
  $('#boundProcess').textContent = state.boundProcess || '尚未绑定游戏进程';
  $('#boundProcess').title = state.boundProcess || '';
  $('#bindBadge').textContent = state.boundProcess ? '已绑定' : '未绑定';
  $('#bindBadge').className = `badge ${state.boundProcess ? 'ok' : ''}`;

  const message = state.error || state.status || '等待操作';
  $('#noticeText').textContent = message;
  $('#notice').className = `notice ${state.error ? 'error' : ''}`;
  setSettings(state.settings);
}

function setSettings(settings) {
  const form = $('#settingsForm');
  form.elements.referenceWidth.value = settings.referenceWidth;
  form.elements.referenceHeight.value = settings.referenceHeight;
  calibration.chatInput = {...settings.chatInput};
  calibration.mapRegion = {...settings.mapRegion};
  calibration.overlayXPercent = settings.overlayXPercent;
  calibration.overlayYPercent = settings.overlayYPercent;
  $('#chatSummary').textContent = formatRect(calibration.chatInput);
  $('#mapSummary').textContent = formatRect(calibration.mapRegion);
  $('#overlaySummary').textContent = `水平 ${calibration.overlayXPercent}% · 垂直 ${calibration.overlayYPercent}%`;
}

async function runAction(button, action) {
  if (!controller || typeof controller[action] !== 'function') return;
  const original = button.textContent;
  button.disabled = true;
  button.textContent = '处理中…';
  try {
    const result = await controller[action]();
    if (action === 'ResetSettings' && result) render(result);
    else render(await controller.GetState());
  } catch (error) {
    $('#noticeText').textContent = String(error);
    $('#notice').className = 'notice error';
  } finally {
    button.disabled = false;
    button.textContent = original;
  }
}

async function startCalibration() {
  const button = $('#startCalibration');
  button.disabled = true;
  button.textContent = '准备中…';
  try {
    await controller.StartCalibration();
  } catch (error) {
    $('#noticeText').textContent = String(error);
    $('#notice').className = 'notice error';
  } finally {
    button.disabled = false;
    button.textContent = '在屏幕上调整';
  }
}

async function connect() {
  for (let attempt = 0; attempt < 80; attempt++) {
    controller = window.go?.main?.Controller;
    if (controller) break;
    await new Promise(resolve => setTimeout(resolve, 50));
  }
  if (!controller) {
    $('#noticeText').textContent = '无法连接 Go 后端，请重新启动程序';
    $('#notice').className = 'notice error';
    return;
  }

  render(await controller.GetState());
  window.runtime?.EventsOn?.('state:update', render);

  document.querySelectorAll('[data-action]').forEach(button => {
    button.addEventListener('click', () => runAction(button, button.dataset.action));
  });
  $('#startCalibration').addEventListener('click', startCalibration);
  $('#overlayToggle').addEventListener('change', async (event) => {
    try { await controller.SetOverlayVisible(event.target.checked); }
    catch { event.target.checked = !event.target.checked; }
  });
}

connect();
