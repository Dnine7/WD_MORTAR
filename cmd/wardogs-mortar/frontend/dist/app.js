const $ = (selector) => document.querySelector(selector);
let controller;
let settingsInitialised = false;

for (const container of document.querySelectorAll('[data-rect]')) {
  const prefix = container.dataset.rect;
  for (const [key, label] of [['x', 'X'], ['y', 'Y'], ['w', 'W'], ['h', 'H']]) {
    container.insertAdjacentHTML('beforeend', `<label>${label}<input type="number" min="0" name="${prefix}.${key}" required></label>`);
  }
}

function formatCoordinate(value) {
  return value ? `X ${value.x.toFixed(2)} · Y ${value.y.toFixed(2)}` : '—';
}

function render(state, forceSettings = false) {
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

  if (!settingsInitialised || forceSettings) {
    setSettings(state.settings);
    settingsInitialised = true;
  }
}

function setSettings(settings) {
  const form = $('#settingsForm');
  form.elements.referenceWidth.value = settings.referenceWidth;
  form.elements.referenceHeight.value = settings.referenceHeight;
  form.elements.overlayXPercent.value = settings.overlayXPercent;
  form.elements.overlayYPercent.value = settings.overlayYPercent;
  for (const rect of ['chatInput', 'mapRegion']) {
    for (const key of ['x', 'y', 'w', 'h']) {
      form.elements[`${rect}.${key}`].value = settings[rect][key];
    }
  }
}

function getSettings() {
  const form = $('#settingsForm');
  const number = (name) => Number(form.elements[name].value);
  const rect = (name) => ({x: number(`${name}.x`), y: number(`${name}.y`), w: number(`${name}.w`), h: number(`${name}.h`)});
  return {
    referenceWidth: number('referenceWidth'),
    referenceHeight: number('referenceHeight'),
    overlayXPercent: number('overlayXPercent'),
    overlayYPercent: number('overlayYPercent'),
    chatInput: rect('chatInput'),
    mapRegion: rect('mapRegion')
  };
}

async function runAction(button, action) {
  if (!controller || typeof controller[action] !== 'function') return;
  const original = button.textContent;
  button.disabled = true;
  button.textContent = '处理中…';
  try {
    const result = await controller[action]();
    if (action === 'ResetSettings' && result) render(result, true);
    else render(await controller.GetState());
  } catch (error) {
    $('#noticeText').textContent = String(error);
    $('#notice').className = 'notice error';
  } finally {
    button.disabled = false;
    button.textContent = original;
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

  render(await controller.GetState(), true);
  window.runtime?.EventsOn?.('state:update', render);

  document.querySelectorAll('[data-action]').forEach(button => {
    button.addEventListener('click', () => runAction(button, button.dataset.action));
  });
  $('#overlayToggle').addEventListener('change', async (event) => {
    try { await controller.SetOverlayVisible(event.target.checked); }
    catch { event.target.checked = !event.target.checked; }
  });
  $('#settingsForm').addEventListener('submit', async (event) => {
    event.preventDefault();
    const button = event.submitter;
    button.disabled = true;
    try {
      await controller.SaveSettings(getSettings());
      render(await controller.GetState(), true);
    } catch (error) {
      $('#noticeText').textContent = String(error);
      $('#notice').className = 'notice error';
    } finally {
      button.disabled = false;
    }
  });
}

connect();
