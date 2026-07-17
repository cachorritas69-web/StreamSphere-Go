const API_BASE = (window.STREAMSPHERE_API || 'http://localhost:8080').replace(/\/$/, '');

const state = {
  token: localStorage.getItem('streamsphere_token') || '',
  user: null,
  channel: null,
  currentView: 'home',
  currentVideo: null,
  selectedCategory: 'Todas',
  processingTimer: null,
};

const $ = (selector, parent = document) => parent.querySelector(selector);
const $$ = (selector, parent = document) => [...parent.querySelectorAll(selector)];

async function api(path, options = {}) {
  const headers = new Headers(options.headers || {});
  if (state.token) headers.set('Authorization', `Bearer ${state.token}`);
  if (options.body && !(options.body instanceof FormData)) headers.set('Content-Type', 'application/json');
  const response = await fetch(`${API_BASE}${path}`, { ...options, headers });
  let payload = null;
  try { payload = await response.json(); } catch { payload = { success: false, message: 'El servidor devolvió una respuesta inválida.' }; }
  if (!response.ok || payload.success === false) {
    if (response.status === 401 && state.token) clearSession(false);
    throw new Error(payload.message || `Error HTTP ${response.status}`);
  }
  return payload.data;
}

function setToken(token) {
  state.token = token || '';
  if (token) localStorage.setItem('streamsphere_token', token);
  else localStorage.removeItem('streamsphere_token');
}

function toast(message, type = 'success') {
  const element = document.createElement('div');
  element.className = `toast ${type}`;
  element.textContent = message;
  $('#toast-container').append(element);
  setTimeout(() => element.remove(), 4000);
}

function escapeHTML(value = '') {
  return String(value).replace(/[&<>'"]/g, char => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', "'": '&#39;', '"': '&quot;' }[char]));
}

function formatDate(value) {
  if (!value) return '';
  return new Intl.DateTimeFormat('es-MX', { dateStyle: 'medium', timeStyle: 'short' }).format(new Date(value));
}

function openModal(id) { $(`#${id}`).classList.add('open'); document.body.style.overflow = 'hidden'; }
function closeModal(id) { $(`#${id}`).classList.remove('open'); document.body.style.overflow = ''; }

function showView(name) {
  state.currentView = name;
  $$('.view').forEach(view => view.classList.toggle('active', view.id === `${name}-view`));
  $$('.nav-item').forEach(item => item.classList.toggle('active', item.dataset.view === name));
  $('#sidebar').classList.remove('open');
  window.scrollTo({ top: 0, behavior: 'smooth' });
  if (name === 'explore') loadExplore();
  if (name === 'studio') loadStudio();
  if (name === 'history') loadHistory();
  if (name === 'analytics') loadAnalytics();
  if (name === 'playlists') loadPlaylists();
  if (name === 'notifications') loadNotifications();
  if (name === 'profile') renderProfile();
}

function requireAuth(callback) {
  if (!state.user) { openModal('auth-modal'); return false; }
  callback?.();
  return true;
}

async function hydrateSession() {
  if (!state.token) { renderSession(); return; }
  try {
    const data = await api('/api/users/me');
    state.user = data.user;
    state.channel = data.channel || null;
  } catch (error) {
    clearSession(false);
  }
  renderSession();
  if (state.user) loadNotificationBadge();
}

function renderSession() {
  const authenticated = Boolean(state.user);
  $('#auth-button').classList.toggle('hidden', authenticated);
  $('#profile-button').classList.toggle('hidden', !authenticated);
  $('#notifications-button').classList.toggle('hidden', !authenticated);
  $$('.auth-only').forEach(element => element.classList.toggle('hidden', !authenticated));
  const creator = authenticated && ['CREATOR', 'ADMIN'].includes(state.user.role);
  $$('.creator-only').forEach(element => element.classList.toggle('hidden', !creator));
  $('#upload-shortcut').classList.toggle('hidden', !creator);
  if (authenticated) {
    const initial = state.user.username.slice(0, 1).toUpperCase();
    $('#profile-avatar').textContent = initial;
    $('#profile-name').textContent = state.user.username;
  }
}

function clearSession(showMessage = true) {
  setToken('');
  state.user = null;
  state.channel = null;
  renderSession();
  if (showMessage) toast('Sesión cerrada');
  showView('home');
}

async function login(event) {
  event.preventDefault();
  try {
    const data = await api('/api/auth/login', {
      method: 'POST',
      body: JSON.stringify({ emailOrUsername: $('#login-identity').value.trim(), password: $('#login-password').value }),
    });
    setToken(data.accessToken);
    state.user = data.user;
    await hydrateSession();
    closeModal('auth-modal');
    toast(`Bienvenido, ${state.user.username}`);
  } catch (error) { toast(error.message, 'error'); }
}

async function register(event) {
  event.preventDefault();
  try {
    const data = await api('/api/auth/register', {
      method: 'POST',
      body: JSON.stringify({
        username: $('#register-username').value.trim(),
        email: $('#register-email').value.trim(),
        password: $('#register-password').value,
      }),
    });
    setToken(data.accessToken);
    state.user = data.user;
    state.channel = null;
    renderSession();
    closeModal('auth-modal');
    toast('Cuenta creada correctamente');
  } catch (error) { toast(error.message, 'error'); }
}

async function createChannel(event) {
  event.preventDefault();
  try {
    const data = await api('/api/channels', {
      method: 'POST',
      body: JSON.stringify({ name: $('#channel-name').value.trim(), description: $('#channel-description').value.trim() }),
    });
    state.channel = data.channel;
    if (data.accessToken) setToken(data.accessToken);
    await hydrateSession();
    closeModal('channel-modal');
    toast('Canal creado. Ya puedes publicar videos.');
    showView('studio');
  } catch (error) { toast(error.message, 'error'); }
}

async function ensureCreator() {
  if (!requireAuth()) return false;
  if (!state.channel) { openModal('channel-modal'); return false; }
  return true;
}

function videoCard(video, ownerMode = false) {
  const image = video.thumbnailUrl
    ? `<img src="${API_BASE}${escapeHTML(video.thumbnailUrl)}" alt="Miniatura de ${escapeHTML(video.title)}" loading="lazy">`
    : '<div class="thumbnail-placeholder">▶</div>';
  return `<article class="video-card" data-video-id="${escapeHTML(video.videoId)}">
    <div class="thumbnail">${image}${ownerMode ? `<span class="video-status">${escapeHTML(video.status)}</span>` : ''}</div>
    <div class="card-body"><h3>${escapeHTML(video.title)}</h3><p>${escapeHTML(video.category)} · ${formatDate(video.createdAt)}</p></div>
  </article>`;
}

function emptyState(message, icon = '◌') {
  return `<div class="empty-state"><span>${icon}</span><strong>${escapeHTML(message)}</strong><small>El contenido aparecerá aquí cuando esté disponible.</small></div>`;
}

function bindVideoCards(parent) {
  $$('.video-card', parent).forEach(card => card.addEventListener('click', () => openVideo(card.dataset.videoId)));
}

async function loadHome() {
  const grid = $('#home-video-grid');
  grid.innerHTML = emptyState('Cargando videos…', '◷');
  try {
    const data = await api('/api/videos/search?size=8');
    grid.innerHTML = data.items.length ? data.items.map(video => videoCard(video)).join('') : emptyState('Todavía no hay videos publicados', '▶');
    bindVideoCards(grid);
  } catch (error) { grid.innerHTML = emptyState(error.message, '⚠'); }
}

async function loadExplore(query = $('#search-input').value.trim()) {
  const grid = $('#explore-video-grid');
  grid.innerHTML = emptyState('Buscando contenido…', '⌕');
  const params = new URLSearchParams({ size: '30' });
  if (query) params.set('q', query);
  if (state.selectedCategory !== 'Todas') params.set('category', state.selectedCategory);
  try {
    const data = await api(`/api/videos/search?${params}`);
    grid.innerHTML = data.items.length ? data.items.map(video => videoCard(video)).join('') : emptyState('No encontramos videos con esos filtros', '⌕');
    bindVideoCards(grid);
  } catch (error) { grid.innerHTML = emptyState(error.message, '⚠'); }
}

async function loadStudio() {
  if (!(await ensureCreator())) return;
  const grid = $('#my-video-grid');
  grid.innerHTML = emptyState('Cargando tus videos…', '◷');
  try {
    const data = await api('/api/videos/mine');
    grid.innerHTML = data.items.length ? data.items.map(video => videoCard(video, true)).join('') : emptyState('Aún no has creado videos', '＋');
    bindVideoCards(grid);
  } catch (error) { grid.innerHTML = emptyState(error.message, '⚠'); }
}

async function uploadVideo(event) {
  event.preventDefault();
  if (!(await ensureCreator())) return;
  const file = $('#video-file').files[0];
  if (!file) { toast('Selecciona un archivo de video', 'error'); return; }
  const submit = event.submitter;
  submit.disabled = true;
  submit.textContent = 'Creando metadatos…';
  try {
    const created = await api('/api/videos', {
      method: 'POST',
      body: JSON.stringify({
        channelId: state.channel.channelId,
        title: $('#video-title').value.trim(),
        description: $('#video-description').value.trim(),
        category: $('#video-category').value,
        visibility: $('#video-visibility').value,
        tags: $('#video-tags').value.split(',').map(tag => tag.trim()).filter(Boolean),
      }),
    });
    submit.textContent = 'Subiendo archivo…';
    const formData = new FormData();
    formData.append('file', file);
    const job = await api(`/api/media/videos/${created.videoId}/upload`, { method: 'POST', body: formData });
    showProcessing(job);
    $('#upload-form').reset();
    $('#file-name').textContent = 'Selecciona o arrastra un video';
    toast('Video recibido. El procesamiento continúa en segundo plano.');
    pollJob(job.jobId);
    loadStudio();
  } catch (error) { toast(error.message, 'error'); }
  finally { submit.disabled = false; submit.textContent = 'Crear y procesar video'; }
}

function showProcessing(job) {
  $('#processing-empty').classList.add('hidden');
  $('#processing-state').classList.remove('hidden');
  $('#processing-label').textContent = job.status;
  $('#processing-percent').textContent = `${job.progress}%`;
  $('#processing-bar').style.width = `${job.progress}%`;
  $('#processing-message').textContent = job.error || 'El trabajo se ejecuta de manera asíncrona.';
}

function pollJob(jobId) {
  clearInterval(state.processingTimer);
  const check = async () => {
    try {
      const job = await api(`/api/media/jobs/${jobId}`);
      showProcessing(job);
      if (['COMPLETED', 'FAILED'].includes(job.status)) {
        clearInterval(state.processingTimer);
        state.processingTimer = null;
        toast(job.status === 'COMPLETED' ? 'Tu video ya fue publicado.' : `El procesamiento falló: ${job.error}`, job.status === 'COMPLETED' ? 'success' : 'error');
        loadStudio();
        loadHome();
      }
    } catch (error) { clearInterval(state.processingTimer); toast(error.message, 'error'); }
  };
  check();
  state.processingTimer = setInterval(check, 3000);
}

async function openVideo(videoId) {
  try {
    const video = await api(`/api/videos/${videoId}`);
    state.currentVideo = video;
    $('#detail-title').textContent = video.title;
    $('#detail-category').textContent = video.category;
    $('#detail-description').textContent = video.description || 'Sin descripción.';
    $('#video-player').removeAttribute('src');
    $('#video-player').load();
    $('#player-loading').classList.remove('hidden');
    openModal('video-modal');
    const playback = await api(`/api/playback/videos/${videoId}/manifest`);
    $('#video-player').src = playback.streamUrl;
    $('#player-loading').classList.add('hidden');
    $('#video-player').play().catch(() => {});
    recordPlayback('STARTED', 0);
    loadComments(videoId);
    loadReactions(videoId);
    updateSubscriptionButton();
  } catch (error) { closeModal('video-modal'); toast(error.message, 'error'); }
}

async function recordPlayback(eventType, second) {
  if (!state.currentVideo) return;
  try {
    await api('/api/playback/events', { method: 'POST', body: JSON.stringify({ videoId: state.currentVideo.videoId, eventType, second: Math.max(0, Math.round(second || 0)) }) });
  } catch { /* La reproducción no se bloquea por una falla de analítica. */ }
}

async function loadComments(videoId) {
  const list = $('#comment-list');
  list.innerHTML = '<p class="muted">Cargando comentarios…</p>';
  try {
    const data = await api(`/api/videos/${videoId}/comments?size=50`);
    $('#comment-count').textContent = `${data.totalItems} comentario${data.totalItems === 1 ? '' : 's'}`;
    list.innerHTML = data.items.length ? data.items.map(item => `<article class="comment"><strong>${escapeHTML(item.username)}</strong><p>${escapeHTML(item.content)}</p><time>${formatDate(item.createdAt)}</time></article>`).join('') : '<p class="muted">Sé la primera persona en comentar.</p>';
  } catch (error) { list.innerHTML = `<p class="muted">Comentarios no disponibles: ${escapeHTML(error.message)}</p>`; }
}

async function submitComment(event) {
  event.preventDefault();
  if (!requireAuth()) return;
  const content = $('#comment-input').value.trim();
  if (!content || !state.currentVideo) return;
  try {
    await api(`/api/videos/${state.currentVideo.videoId}/comments`, { method: 'POST', body: JSON.stringify({ content }) });
    $('#comment-input').value = '';
    loadComments(state.currentVideo.videoId);
    toast('Comentario publicado');
  } catch (error) { toast(error.message, 'error'); }
}

async function loadReactions(videoId) {
  try {
    const data = await api(`/api/videos/${videoId}/reactions`);
    $('#like-count').textContent = data.likes;
    $('#dislike-count').textContent = data.dislikes;
  } catch { $('#like-count').textContent = '0'; $('#dislike-count').textContent = '0'; }
}

async function react(type) {
  if (!requireAuth() || !state.currentVideo) return;
  try {
    await api(`/api/videos/${state.currentVideo.videoId}/reactions`, { method: 'POST', body: JSON.stringify({ type }) });
    loadReactions(state.currentVideo.videoId);
  } catch (error) { toast(error.message, 'error'); }
}

async function updateSubscriptionButton() {
  const button = $('#subscribe-button');
  if (!state.user || !state.currentVideo) { button.textContent = 'Inicia sesión para suscribirte'; return; }
  try {
    const data = await api(`/api/channels/${state.currentVideo.channelId}/subscriptions/status`);
    button.dataset.subscribed = String(data.subscribed);
    button.textContent = data.subscribed ? 'Suscrito ✓' : 'Suscribirme';
  } catch { button.textContent = 'Suscribirme'; }
}

async function toggleSubscription() {
  if (!requireAuth() || !state.currentVideo) return;
  const subscribed = $('#subscribe-button').dataset.subscribed === 'true';
  try {
    await api(`/api/channels/${state.currentVideo.channelId}/subscriptions`, { method: subscribed ? 'DELETE' : 'POST' });
    updateSubscriptionButton();
    toast(subscribed ? 'Suscripción cancelada' : 'Te suscribiste al canal');
  } catch (error) { toast(error.message, 'error'); }
}

async function loadHistory() {
  if (!requireAuth()) return;
  const list = $('#history-list');
  list.innerHTML = '<div class="list-item"><p class="muted">Cargando historial…</p></div>';
  try {
    const data = await api('/api/history/me');
    list.innerHTML = data.items.length ? data.items.map(item => `<button class="list-item" data-history-video="${escapeHTML(item.videoId)}"><span class="list-icon">▶</span><span class="list-content"><h4>${escapeHTML(item.title || 'Video')}</h4><p>Progreso: ${item.lastSecond}s · ${item.completed ? 'Completado' : 'Pendiente'}</p></span><span class="list-time">${formatDate(item.updatedAt)}</span></button>`).join('') : '<div class="list-item"><p class="muted">Aún no tienes historial.</p></div>';
    $$('[data-history-video]', list).forEach(item => item.addEventListener('click', () => openVideo(item.dataset.historyVideo)));
  } catch (error) { list.innerHTML = `<div class="list-item"><p class="muted">${escapeHTML(error.message)}</p></div>`; }
}

async function loadAnalytics() {
  if (!requireAuth()) return;
  try {
    const data = await api('/api/analytics/creators/me');
    $('#metric-cards').innerHTML = [
      ['Vistas', data.views, '◉'], ['Tiempo visto', `${data.watchTime}s`, '◷'], ['Me gusta', data.likes, '♥'], ['Comentarios', data.comments, '💬'],
    ].map(([label, value, icon]) => `<article class="panel metric-card"><small>${icon} ${label}</small><strong>${value}</strong></article>`).join('');
    $('#top-videos').innerHTML = data.topVideos.length ? `<table><thead><tr><th>Video</th><th>Vistas</th><th>Tiempo</th><th>Likes</th><th>Comentarios</th></tr></thead><tbody>${data.topVideos.map(item => `<tr><td>${escapeHTML(item.title || item.videoId)}</td><td>${item.views}</td><td>${item.watchTime}s</td><td>${item.likes}</td><td>${item.comments}</td></tr>`).join('')}</tbody></table>` : '<p class="muted">Las métricas aparecerán cuando se reproduzcan tus videos.</p>';
  } catch (error) { $('#metric-cards').innerHTML = emptyState(error.message, '⚠'); }
}

async function loadPlaylists() {
  if (!requireAuth()) return;
  const grid = $('#playlist-grid');
  try {
    const data = await api('/api/playlists/me');
    grid.innerHTML = data.items.length ? data.items.map(item => `<article class="panel playlist-card"><span class="list-icon">▤</span><h3>${escapeHTML(item.name)}</h3><p>${item.itemCount} videos · ${escapeHTML(item.visibility)}</p></article>`).join('') : emptyState('Aún no tienes playlists', '▤');
  } catch (error) { grid.innerHTML = emptyState(error.message, '⚠'); }
}

async function createPlaylist(event) {
  event.preventDefault();
  try {
    await api('/api/playlists', { method: 'POST', body: JSON.stringify({ name: $('#playlist-name').value.trim(), visibility: $('#playlist-visibility').value }) });
    closeModal('playlist-modal');
    event.target.reset();
    toast('Playlist creada');
    loadPlaylists();
  } catch (error) { toast(error.message, 'error'); }
}

async function loadNotificationBadge() {
  try {
    const data = await api('/api/notifications/me?size=1');
    const badge = $('#notification-badge');
    badge.textContent = data.unreadCount > 99 ? '99+' : data.unreadCount;
    badge.classList.toggle('hidden', data.unreadCount === 0);
  } catch { /* no bloquea la UI */ }
}

async function loadNotifications() {
  if (!requireAuth()) return;
  const list = $('#notification-list');
  try {
    const data = await api('/api/notifications/me?size=100');
    list.innerHTML = data.items.length ? data.items.map(item => `<button class="list-item ${item.read ? '' : 'unread'}" data-notification-id="${escapeHTML(item.notificationId)}" data-link="${escapeHTML(item.link || '')}"><span class="list-icon">${item.type.includes('FAILED') ? '⚠' : item.type.includes('COMMENT') ? '💬' : '✓'}</span><span class="list-content"><h4>${escapeHTML(item.title)}</h4><p>${escapeHTML(item.message)}</p></span><span class="list-time">${formatDate(item.createdAt)}</span></button>`).join('') : '<div class="list-item"><p class="muted">No tienes notificaciones.</p></div>';
    $$('[data-notification-id]', list).forEach(item => item.addEventListener('click', async () => {
      await api(`/api/notifications/${item.dataset.notificationId}/read`, { method: 'PATCH' }).catch(() => {});
      if (item.dataset.link) {
        const videoId = new URL(item.dataset.link, location.origin).searchParams.get('video');
        if (videoId) openVideo(videoId);
      }
      loadNotifications(); loadNotificationBadge();
    }));
  } catch (error) { list.innerHTML = `<div class="list-item"><p class="muted">${escapeHTML(error.message)}</p></div>`; }
}

async function markAllRead() {
  try { await api('/api/notifications/read-all', { method: 'PATCH' }); loadNotifications(); loadNotificationBadge(); } catch (error) { toast(error.message, 'error'); }
}

function renderProfile() {
  if (!requireAuth()) return;
  const initial = state.user.username.slice(0, 1).toUpperCase();
  $('#large-avatar').textContent = initial;
  $('#profile-title').textContent = state.user.username;
  $('#profile-email').textContent = state.user.email;
  $('#profile-role').textContent = state.user.role;
  $('#channel-card').innerHTML = state.channel
    ? `<span class="eyebrow">MI CANAL</span><h2>${escapeHTML(state.channel.name)}</h2><p class="muted">${escapeHTML(state.channel.description || 'Sin descripción')}</p><button class="button button-primary" data-profile-studio>Ir a Creator Studio</button>`
    : `<span class="eyebrow">CREADOR</span><h2>Aún no tienes canal</h2><p class="muted">Crea uno para subir y administrar tus videos.</p><button class="button button-primary" data-create-channel>Crear mi canal</button>`;
  $('[data-profile-studio]')?.addEventListener('click', () => showView('studio'));
  $('[data-create-channel]')?.addEventListener('click', () => openModal('channel-modal'));
}

function bindEvents() {
  $$('.nav-item').forEach(item => item.addEventListener('click', () => showView(item.dataset.view)));
  $$('[data-view-target]').forEach(item => item.addEventListener('click', () => showView(item.dataset.viewTarget)));
  $$('[data-close-modal]').forEach(item => item.addEventListener('click', () => closeModal(item.dataset.closeModal)));
  $('#menu-button').addEventListener('click', () => $('#sidebar').classList.toggle('open'));
  $('#auth-button').addEventListener('click', () => openModal('auth-modal'));
  $('#profile-button').addEventListener('click', () => showView('profile'));
  $('#notifications-button').addEventListener('click', () => showView('notifications'));
  $('#upload-shortcut').addEventListener('click', () => showView('studio'));
  $('#hero-explore').addEventListener('click', () => showView('explore'));
  $('#hero-create').addEventListener('click', async () => { if (await ensureCreator()) showView('studio'); });
  $('#logout-button').addEventListener('click', () => clearSession());
  $('#login-form').addEventListener('submit', login);
  $('#register-form').addEventListener('submit', register);
  $('#channel-form').addEventListener('submit', createChannel);
  $('#upload-form').addEventListener('submit', uploadVideo);
  $('#comment-form').addEventListener('submit', submitComment);
  $('#like-button').addEventListener('click', () => react('LIKE'));
  $('#dislike-button').addEventListener('click', () => react('DISLIKE'));
  $('#subscribe-button').addEventListener('click', toggleSubscription);
  $('#new-playlist-button').addEventListener('click', () => openModal('playlist-modal'));
  $('#playlist-form').addEventListener('submit', createPlaylist);
  $('#read-all-button').addEventListener('click', markAllRead);
  $('#search-form').addEventListener('submit', event => { event.preventDefault(); showView('explore'); loadExplore(); });
  $$('.chip').forEach(chip => chip.addEventListener('click', () => {
    $$('.chip').forEach(item => item.classList.remove('active'));
    chip.classList.add('active'); state.selectedCategory = chip.dataset.category; loadExplore();
  }));
  $$('.auth-tabs button').forEach(tab => tab.addEventListener('click', () => {
    $$('.auth-tabs button').forEach(item => item.classList.toggle('active', item === tab));
    const loginMode = tab.dataset.authMode === 'login';
    $('#login-form').classList.toggle('hidden', !loginMode);
    $('#register-form').classList.toggle('hidden', loginMode);
    $('#auth-title').textContent = loginMode ? 'Bienvenido de nuevo' : 'Crea tu cuenta';
  }));
  $('#video-file').addEventListener('change', event => { $('#file-name').textContent = event.target.files[0]?.name || 'Selecciona o arrastra un video'; });
  $('#video-player').addEventListener('ended', event => recordPlayback('COMPLETED', event.target.duration));
  let lastProgress = 0;
  $('#video-player').addEventListener('timeupdate', event => {
    if (event.target.currentTime - lastProgress >= 30) { lastProgress = event.target.currentTime; recordPlayback('PROGRESS', event.target.currentTime); }
  });
  $$('#video-modal [data-close-modal]').forEach(control => control.addEventListener('click', () => { $('#video-player').pause(); state.currentVideo = null; }));
  document.addEventListener('keydown', event => { if (event.key === 'Escape') $$('.modal.open').forEach(modal => closeModal(modal.id)); });
}

async function init() {
  bindEvents();
  await hydrateSession();
  loadHome();
  const videoFromURL = new URLSearchParams(location.search).get('video');
  if (videoFromURL) openVideo(videoFromURL);
}

document.addEventListener('DOMContentLoaded', init);
