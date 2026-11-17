const $ = (id) => document.getElementById(id);
const pageSize = 20;
let section;
let offset = 0;
let requestID = 0;
let editing;

const settings = [
  ['topic', 'Тема', 'required'], ['audience', 'Аудитория', 'required'],
  ['keywords', 'Ключевые слова через запятую', 'list'], ['blocked_terms', 'Стоп-слова через запятую', 'list'],
  ['max_age_minutes', 'Возраст новости, мин', 'number', 1440],
  ['reserve_after_minutes', 'Ожидание в резерве, мин', 'number', 30],
  ['min_interval_minutes', 'Интервал выпуска резерва, мин', 'number', 0],
  ['time_zone', 'Часовой пояс', 'text', 'Europe/Moscow'],
  ['quiet_start_hour', 'Начало тихих часов (0–23)', 'hour'],
  ['quiet_end_hour', 'Конец тихих часов (0–23)', 'hour'],
  ['urgent_outside_hours', 'Срочные ночью', 'check'], ['require_approval', 'Требовать согласование', 'check'],
];
const views = {
  sources: {
    title: 'Источники', description: 'Подключайте RSS, сайты и публичные каналы Telegram.',
    list: 'Каталог источников', create: 'Добавить источник', empty: 'Добавьте первый источник, чтобы проверить сбор.',
    fields: [
      ['name', 'Название', 'required'], ['organization', 'Организация', 'required'],
      ['url', 'Ссылка', 'url'], ['kind', 'Тип', { rss: 'RSS / Atom', website: 'Сайт', telegram: 'Telegram' }],
      ['status', 'Статус', { candidate: 'Кандидат', verified: 'Проверен', disabled: 'Отключён' }],
      ['topics', 'Темы через запятую', 'list'], ['primary', 'Первоисточник', 'check'],
    ],
  },
  channels: {
    title: 'Каналы', description: 'Настраивайте тему, правила выпуска и площадки.',
    list: 'Настроенные каналы', create: 'Добавить канал', empty: 'Добавьте канал и выберите площадку публикации.',
    fields: [
      ['name', 'Название', 'required'], ['telegram_chat_id', 'Telegram @канал или ID', 'text'],
      ['website', 'Публиковать на сайт', 'check', true], ['enabled', 'Канал включён', 'check', true],
    ],
    settings,
  },
  presets: {
    title: 'Пресеты', description: 'Храните готовые наборы правил выпуска.',
    list: 'Сохранённые пресеты', create: 'Добавить пресет', empty: 'Создайте пресет, чтобы повторно использовать настройки.',
    fields: [['name', 'Название', 'required']],
    settings,
  },
  review: {
    title: 'Согласование', description: 'Просматривайте подготовленные версии и принимайте решение по каждой площадке.',
    list: 'Ожидают решения', empty: 'Публикаций на согласовании пока нет.',
  },
  news: {
    title: 'Лента сайта', description: 'Публикации, которые уже доступны на сайте.',
    list: 'Опубликованные новости', path: 'site/news', empty: 'В ленте пока нет публикаций.',
  },
};

const labels = {
  candidate: 'Кандидат', verified: 'Проверен', disabled: 'Отключён',
  telegram: 'Telegram', website: 'Сайт', rss: 'RSS / Atom', review: 'На согласовании',
  pending: 'В очереди', sending: 'Отправляется', sent: 'Опубликовано', rejected: 'Отклонено',
  expired: 'Срок истёк', uncertain: 'Итог неизвестен', failed: 'Ошибка отправки',
};
const label = (key) => labels[key] || key;
const date = (value) => value ? new Date(value).toLocaleString('ru-RU') : '';

async function api(path, method = 'GET', body) {
  const response = await fetch(path, {
    method,
    ...(body === undefined ? {} : { headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(body) }),
  });
  if (!response.ok) {
    const data = await response.json().catch(() => null);
    throw new Error(data?.error?.message || `Ошибка API (${response.status})`);
  }
  return response.json();
}

function node(tag, text, className) {
  const value = document.createElement(tag);
  if (text) value.textContent = text;
  if (className) value.className = className;
  return value;
}

function notice(message, error = false) {
  $('notice').textContent = message;
  $('notice').classList.toggle('error', error);
  $('notice').hidden = !message;
}

function button(text, run) {
  const control = node('button', text);
  control.type = 'button';
  control.addEventListener('click', async () => {
    control.disabled = true;
    try {
      await run();
    } catch (error) {
      notice(error.message, true);
    } finally {
      control.disabled = false;
    }
  });
  return control;
}

function originLink(value) {
  if (!value) return null;
  try {
    const url = new URL(value);
    if (!['http:', 'https:'].includes(url.protocol)) return null;
    const link = node('a', 'Открыть источник');
    link.href = url.href;
    link.target = '_blank';
    link.rel = 'noopener noreferrer';
    return link;
  } catch {
    return null;
  }
}

async function load() {
  const active = ++requestID;
  const view = views[section];
  $('items').replaceChildren(node('p', 'Загрузка…', 'empty'));
  $('pages').hidden = true;
  try {
    const result = await api(`/api/v1/${view.path || section}?limit=${pageSize + 1}&offset=${offset}`);
    if (active !== requestID) return;
    const rows = result.items.slice(0, pageSize);
    if (!rows.length && offset > 0) {
      offset -= pageSize;
      await load();
      return;
    }
    $('items').replaceChildren(...(rows.length ? rows.map(row) : [node('p', view.empty, 'empty')]));
    const next = result.items.length > pageSize;
    $('pages').hidden = !offset && !next;
    $('previous').disabled = !offset;
    $('next').disabled = !next;
    $('page').textContent = `Страница ${offset / pageSize + 1}`;
  } catch (error) {
    if (active === requestID) $('items').replaceChildren(node('p', error.message, 'empty'));
  }
}

function row(item) {
  const element = node('article', '', 'row');
  const content = node('div');
  const actions = node('div', '', 'row-actions');
  content.append(node('h3', item.name || item.title));
  if (section === 'sources') {
    content.append(node('p', `${label(item.kind)} · ${item.organization}\n${item.url}`), node('span', label(item.status), 'status'));
    if (item.last_error) content.append(node('p', item.last_error));
    if (item.status !== 'disabled') actions.append(button('Пробный сбор', async () => {
      const result = await api(`/api/v1/sources/${item.id}/probe`, 'POST');
      showDetails('Пробный сбор', result.items, 'Источник вернул пустой список.');
    }));
  } else if (section === 'channels' || section === 'presets') {
    content.append(node('p', `${item.settings.topic} · ${item.settings.audience}`));
    if (section === 'channels') {
      content.append(node('span', item.enabled ? 'Включён' : 'Выключен', 'status'));
      for (const [path, title] of [['events', 'События'], ['publications', 'Публикации']]) {
        actions.append(button(title, async () => {
          const result = await api(`/api/v1/channels/${item.id}/${path}?limit=50`);
          showDetails(`${title} · ${item.name}`, result.items, 'Записей пока нет.');
        }));
      }
    }
  } else {
    content.append(node('p', item.body));
    const metadata = section === 'review' ? `${label(item.target)} · Канал №${item.channel_id}` : date(item.published_at);
    if (metadata) content.append(node('span', metadata, 'status'));
    const link = originLink(item.source_url);
    if (link) {
      const line = node('p');
      line.append(link);
      content.append(line);
    }
    if (section === 'review') {
      for (const [decision, text, feedback] of [['approve', 'Одобрить', 'Публикация одобрена'], ['reject', 'Отклонить', 'Публикация отклонена']]) {
        actions.append(button(text, async () => {
          for (const control of actions.children) control.disabled = true;
          try {
            await api(`/api/v1/review/${item.id}`, 'PUT', { decision });
            notice(feedback);
            await load();
          } finally {
            for (const control of actions.children) control.disabled = false;
          }
        }));
      }
    }
  }
  if (views[section].fields) actions.append(button('Изменить', () => openEditor(item)));
  element.append(content);
  if (actions.childElementCount) element.append(actions);
  return element;
}

function showDetails(title, entries, empty) {
  $('detail-title').textContent = title;
  $('detail-items').replaceChildren(...(entries.length ? entries.map((item) => {
    const article = node('article');
    article.append(node('h3', item.title));
    const text = [item.state && label(item.state), date(item.published_at || item.created_at || item.updated_at), item.last_error, item.summary || item.body].filter(Boolean);
    if (text.length) article.append(node('p', text.join('\n')));
    const link = originLink(item.source_url || item.url);
    if (link) article.append(link);
    return article;
  }) : [node('p', empty)]));
  if (!$('details').open) $('details').showModal();
}

function field(definition, values) {
  const [key, title, type, fallback] = definition;
  const wrapper = node('label');
  const input = node(typeof type === 'object' ? 'select' : 'input');
  const value = values?.[key] ?? fallback ?? '';
  input.name = key;
  if (typeof type === 'object') {
    for (const [code, caption] of Object.entries(type)) input.add(new Option(caption, code));
    if (value) input.value = value;
  } else if (type === 'check') {
    input.type = 'checkbox';
    input.checked = Boolean(value);
    wrapper.className = 'check';
  } else {
    input.type = type === 'hour' ? 'number' : ['number', 'url'].includes(type) ? type : 'text';
    input.required = ['required', 'url', 'number'].includes(type);
    input.value = Array.isArray(value) ? value.join(', ') : value;
    if (input.type === 'number') input.min = 0;
    if (type === 'hour') input.max = 23;
  }
  if (type === 'url') wrapper.className = 'wide';
  if (type === 'check') wrapper.append(input, node('span', title));
  else wrapper.append(node('span', title), input);
  return wrapper;
}

function openEditor(item) {
  const view = views[section];
  editing = { section, item };
  $('form-title').textContent = item ? `Изменить · ${item.name}` : view.create;
  $('form-error').hidden = true;
  $('fields').replaceChildren(...view.fields.map((def) => field(def, item)));
  if (view.settings) {
    $('fields').append(...view.settings.map((def) => field(def, item?.settings)));
    const controls = node('div', '', 'filter-actions');
    controls.append(field(['filter_exclusions', 'Какие темы исключить', 'text']));
    controls.append(button('Подобрать фильтры с ИИ', suggestFilters));
    $('fields').append(controls);
  }
  $('editor').showModal();
}

async function suggestFilters() {
  const active = editing;
  const inputs = $('form').elements;
  const topic = inputs.namedItem('topic').value.trim();
  const audience = inputs.namedItem('audience').value.trim();
  const exclusions = inputs.namedItem('filter_exclusions').value.trim();
  const keywords = inputs.namedItem('keywords');
  const blocked = inputs.namedItem('blocked_terms');
  const priorKeywords = keywords.value;
  const priorBlocked = blocked.value;
  $('form-error').hidden = true;
  try {
    const result = await api('/api/v1/filter-suggestions', 'POST', { topic, audience, exclusions });
    if (editing !== active || !$('editor').open) return;
    if (topic !== inputs.namedItem('topic').value.trim() || audience !== inputs.namedItem('audience').value.trim() ||
        exclusions !== inputs.namedItem('filter_exclusions').value.trim() ||
        priorKeywords !== keywords.value || priorBlocked !== blocked.value) return;
    keywords.value = result.keywords.join(', ');
    if (exclusions) blocked.value = result.blocked_terms.join(', ');
    keywords.focus();
  } catch (error) {
    if (editing === active && $('editor').open) {
      $('form-error').textContent = error.message;
      $('form-error').hidden = false;
    }
  }
}

function readFields(definitions) {
  const values = {};
  for (const [key, , type] of definitions) {
    const input = $('form').elements.namedItem(key);
    if (type === 'check') values[key] = input.checked;
    else if (type === 'list') values[key] = input.value.split(',').map((term) => term.trim()).filter(Boolean);
    else if (type === 'number' || type === 'hour') {
      if (input.value !== '') values[key] = input.valueAsNumber;
    } else values[key] = input.value.trim();
  }
  return values;
}

$('form').addEventListener('submit', async (event) => {
  event.preventDefault();
  const { section: key, item } = editing;
  const view = views[key];
  const payload = readFields(view.fields);
  if (view.settings) payload.settings = readFields(view.settings);
  const submit = event.submitter;
  if (!submit || submit.disabled) return;
  submit.disabled = true;
  $('cancel').disabled = true;
  $('form-error').hidden = true;
  try {
    await api(`/api/v1/${key}${item ? '/' + item.id : ''}`, item ? 'PUT' : 'POST', payload);
    $('editor').close();
    notice('Изменения сохранены');
    if (!item) offset = 0;
    await load();
  } catch (error) {
    $('form-error').textContent = error.message;
    $('form-error').hidden = false;
  } finally {
    submit.disabled = false;
    $('cancel').disabled = false;
  }
});

function navigate() {
  const key = location.hash.slice(1);
  section = Object.hasOwn(views, key) ? key : 'sources';
  offset = 0;
  const view = views[section];
  for (const link of document.querySelectorAll('nav a')) {
    if (link.hash === '#' + section) link.setAttribute('aria-current', 'page');
    else link.removeAttribute('aria-current');
  }
  $('title').textContent = view.title;
  $('description').textContent = view.description;
  $('list-title').textContent = view.list;
  $('create').textContent = view.create || '';
  $('create').hidden = !view.create;
  notice('');
  void load();
}

async function ready() {
  const status = $('connection');
  try {
    await api('/api/ready');
    status.classList.add('ready');
    status.textContent = 'API готов';
  } catch {
    status.classList.remove('ready');
    status.textContent = 'API недоступен';
  }
}

$('create').onclick = () => openEditor();
$('cancel').onclick = () => $('editor').close();
$('editor').addEventListener('cancel', (event) => { if ($('cancel').disabled) event.preventDefault(); });
$('refresh').onclick = () => { notice(''); void ready(); void load(); };
$('previous').onclick = () => { offset -= pageSize; void load(); };
$('next').onclick = () => { offset += pageSize; void load(); };
window.addEventListener('hashchange', navigate);
navigate();
void ready();
