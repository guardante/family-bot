// Надя WebApp: задачи и события. Ванильный JS без сборки.
'use strict';

const tg = window.Telegram && window.Telegram.WebApp;
if (tg) { tg.ready(); tg.expand(); }

const initData = (tg && tg.initData) || '';
const HEADERS = { 'Content-Type': 'application/json', 'X-Telegram-Init-Data': initData };

async function api(path, body) {
  const opts = body
    ? { method: 'POST', headers: HEADERS, body: JSON.stringify(body) }
    : { headers: { 'X-Telegram-Init-Data': initData } };
  const res = await fetch(path, opts);
  if (!res.ok) throw new Error('HTTP ' + res.status);
  return res.json();
}

function haptic(kind) {
  try { tg && tg.HapticFeedback && tg.HapticFeedback.impactOccurred(kind || 'light'); } catch (e) {}
}

let todos = [];
let events = [];

// ---- вкладки ----
function showTab(name) {
  document.getElementById('view-todos').hidden = name !== 'todos';
  document.getElementById('view-events').hidden = name !== 'events';
  document.getElementById('tab-todos').classList.toggle('active', name === 'todos');
  document.getElementById('tab-events').classList.toggle('active', name === 'events');
  haptic('light');
}
window.showTab = showTab;

// ---- задачи ----
function renderTodos() {
  const box = document.getElementById('todo-list');
  box.innerHTML = '';
  if (!todos.length) {
    box.innerHTML = '<div class="empty">Пока пусто — добавь первую задачу ✍️</div>';
    return;
  }
  todos.sort((a, b) => (a.done - b.done) || (a.id - b.id));
  for (const t of todos) {
    const row = document.createElement('div');
    row.className = 'row' + (t.done ? ' done' : '');

    const check = document.createElement('div');
    check.className = 'check';
    check.textContent = t.done ? '✅' : '⚪';
    check.onclick = async () => {
      haptic();
      await api('/api/todos/toggle', { id: t.id, done: !t.done });
      t.done = !t.done;
      renderTodos();
    };

    const label = document.createElement('span');
    label.className = 'label';
    label.textContent = t.text;

    const del = document.createElement('button');
    del.className = 'del';
    del.textContent = '✕';
    del.onclick = async () => {
      haptic('medium');
      await api('/api/todos/delete', { id: t.id });
      todos = todos.filter((x) => x.id !== t.id);
      renderTodos();
    };

    row.append(check, label, del);
    box.appendChild(row);
  }
}

document.getElementById('todo-form').addEventListener('submit', async (e) => {
  e.preventDefault();
  const input = document.getElementById('todo-input');
  const text = input.value.trim();
  if (!text) return;
  try {
    await api('/api/todos', { text });
    input.value = '';
    haptic('medium');
    await load();
  } catch (err) { alert('Не сохранилось: ' + err.message); }
});

// ---- события ----
function fmtEvent(iso) {
  const d = new Date(iso);
  const now = new Date();
  const sameYear = d.getFullYear() === now.getFullYear();
  const opts = { day: 'numeric', month: 'long', hour: '2-digit', minute: '2-digit' };
  if (!sameYear) opts.year = 'numeric';
  const date = d.toLocaleString('ru-RU', opts);
  const left = d - now;
  if (left < 0) return date + ' · уже прошло';
  return date + ' · ' + humanLeft(left);
}

function humanLeft(ms) {
  const m = Math.round(ms / 60000);
  if (m < 60) return 'через ' + m + ' мин';
  const h = Math.floor(m / 60), rm = m % 60;
  if (h < 24) return 'через ' + h + (rm ? ' ч ' + rm + ' мин' : ' ч');
  const days = Math.floor(h / 24), rh = h % 24;
  return 'через ' + days + (rh ? ' дн ' + rh + ' ч' : ' дн');
}

function remindLabel(min) {
  if (min === null || min === undefined) return '';
  if (min === 0) return '🔔 напоминание в начале';
  if (min === 1440) return '🔔 напоминание за день';
  if (min >= 60) return '🔔 напоминание за ' + (min / 60) + ' ч';
  return '🔔 напоминание за ' + min + ' мин';
}

function renderEvents() {
  const box = document.getElementById('event-list');
  box.innerHTML = '';
  if (!events.length) {
    box.innerHTML = '<div class="empty">Событий нет — добавь ниже 📅</div>';
    return;
  }
  events.sort((a, b) => a.event_at < b.event_at ? -1 : 1);
  for (const ev of events) {
    const row = document.createElement('div');
    row.className = 'row';

    const label = document.createElement('span');
    label.className = 'label';
    label.textContent = ev.title;
    const when = document.createElement('span');
    when.className = 'when';
    when.textContent = fmtEvent(ev.event_at);
    if (ev.remind_minutes !== null && ev.remind_minutes !== undefined) {
      when.textContent += '\n' + remindLabel(ev.remind_minutes);
      when.style.whiteSpace = 'pre-line';
    }
    label.appendChild(when);

    const del = document.createElement('button');
    del.className = 'del';
    del.textContent = '✕';
    del.onclick = async () => {
      haptic('medium');
      await api('/api/events/delete', { id: ev.id });
      events = events.filter((x) => x.id !== ev.id);
      renderEvents();
    };

    row.append(label, del);
    box.appendChild(row);
  }
}

document.getElementById('event-form').addEventListener('submit', async (e) => {
  e.preventDefault();
  const title = document.getElementById('event-title').value.trim();
  const raw = document.getElementById('event-datetime').value;
  if (!title || !raw) { alert('Нужно название и время'); return; }
  const remindRaw = document.getElementById('event-remind').value;
  const remind = remindRaw === '' ? null : parseInt(remindRaw, 10);
  try {
    await api('/api/events', { title, event_at: new Date(raw).toISOString(), remind_minutes: remind });
    document.getElementById('event-title').value = '';
    haptic('medium');
    await load();
  } catch (err) { alert('Не сохранилось: ' + err.message); }
});

// ---- загрузка ----
async function load() {
  const s = await api('/api/state');
  todos = s.todos || [];
  events = s.events || [];
  renderTodos();
  renderEvents();
}

(async function init() {
  if (!initData) {
    const banner = document.getElementById('banner');
    banner.hidden = false;
    banner.textContent = 'Открой приложение из Telegram, чтобы видеть свои данные. ' +
      'В обычном браузере оно работает только в демо-режиме и без сохранения.';
  }
  try {
    await load();
  } catch (err) {
    const banner = document.getElementById('banner');
    banner.hidden = false;
    banner.textContent = 'Не загрузилось: ' + err.message +
      (initData ? '' : ' — похоже, открыто не из Telegram.');
  }
})();
