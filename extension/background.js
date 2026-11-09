// Google Doc Radar — Background Service Worker

const DEFAULT_SERVER = 'http://localhost:9906';

// Preferred export format first, then progressively more compatible fallbacks.
// `md` gives a clean Markdown export (what the mirror wants); some documents /
// accounts reject it with HTTP 403, so we degrade to plain text and finally HTML.
const EXPORT_FORMATS = ['md', 'txt', 'html'];

async function getSettings() {
  return new Promise((resolve) => {
    chrome.storage.local.get({ serverUrl: DEFAULT_SERVER, ttlDays: 7, dryRun: false }, resolve);
  });
}

async function postToServer(endpoint, data) {
  const { serverUrl, dryRun } = await getSettings();
  if (dryRun) {
    console.log('[gdoc-radar] dry-run, would POST to', endpoint, data);
    return { ok: true, dryRun: true };
  }
  const url = `${serverUrl}${endpoint}`;
  try {
    const resp = await fetch(url, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(data),
    });
    const json = await resp.json();
    return { ok: resp.ok, status: resp.status, ...json };
  } catch (err) {
    return { ok: false, error: err.message };
  }
}

function updateStatus(status) {
  chrome.storage.local.set({ lastSyncStatus: status });
}

// The server stores SQLite CURRENT_TIMESTAMP values ("YYYY-MM-DD HH:MM:SS") in
// UTC but without a timezone suffix. `new Date()` would parse that as *local*
// time, so normalise it to an explicit UTC instant before comparing.
function toUtcIso(value) {
  if (!value) return null;
  const s = String(value).trim();
  if (!s) return null;
  if (/[zZ]$|[+-]\d{2}:?\d{2}$/.test(s)) return s;
  return s.replace(' ', 'T') + 'Z';
}

async function isWithinTTL(serverUrl, ttlDays, documentId, tabId) {
  if (ttlDays <= 0) return false;
  try {
    const params = new URLSearchParams({ document_id: documentId, limit: '1' });
    if (tabId) params.set('tab_id', tabId);
    const resp = await fetch(`${serverUrl}/api/read?${params}`);
    if (!resp.ok) return false;
    const json = await resp.json();
    const iso = toUtcIso(json.documents?.[0]?.updated_at);
    if (!iso) return false;
    return Date.now() - new Date(iso).getTime() < ttlDays * 24 * 60 * 60 * 1000;
  } catch {
    return false;
  }
}

function buildExportUrl(documentId, tabId, authUser, format) {
  const params = new URLSearchParams({ format });
  if (tabId) params.set('tab', tabId);
  // Keep the export on the same signed-in account as the page the user is on.
  // Without this, multi-account users can get 403 because Google falls back to
  // the default account, which may not have access to the document.
  if (authUser !== undefined && authUser !== null && authUser !== '') {
    params.set('authuser', String(authUser));
  }
  return `https://docs.google.com/document/d/${documentId}/export?${params}`;
}

// Detect a Google sign-in page that can be returned with HTTP 200 after a
// redirect when the request is not authenticated for this document.
function looksLikeAuthPage(text) {
  const head = text.slice(0, 2000);
  return /<html/i.test(head) && /(accounts\.google\.com|ServiceLogin|sign in to continue)/i.test(head);
}

// Convert a Google Docs HTML export into readable Markdown-ish text. Used only
// as a last-resort fallback when Markdown / plain-text exports are rejected.
function htmlExportToText(html) {
  let s = html;
  s = s.replace(/<style[\s\S]*?<\/style>/gi, ' ');
  s = s.replace(/<script[\s\S]*?<\/script>/gi, ' ');
  s = s.replace(/<\/(p|div|section|article|tr|table|ul|ol|blockquote)>/gi, '\n');
  s = s.replace(/<br\s*\/?>/gi, '\n');
  s = s.replace(/<li[^>]*>/gi, '- ');
  s = s.replace(/<h([1-6])[^>]*>/gi, (_m, n) => '\n' + '#'.repeat(Number(n)) + ' ');
  s = s.replace(/<\/t[dh]>/gi, ' | ');
  s = s.replace(/<[^>]+>/g, '');
  s = s
    .replace(/&nbsp;/gi, ' ')
    .replace(/&amp;/gi, '&')
    .replace(/&lt;/gi, '<')
    .replace(/&gt;/gi, '>')
    .replace(/&quot;/gi, '"')
    .replace(/&#39;/gi, "'");
  s = s.replace(/[ \t]+\n/g, '\n').replace(/[ \t]{2,}/g, ' ').replace(/\n{3,}/g, '\n\n');
  return s.trim();
}

// Fetch the document through Google's export endpoint. The background service
// worker has host permissions for docs.google.com and *.googleusercontent.com,
// so it can follow the export download redirect (unlike a page-context fetch).
async function fetchDocContent(documentId, tabId, authUser) {
  const attempts = [];
  let saw403 = false;

  for (const format of EXPORT_FORMATS) {
    const url = buildExportUrl(documentId, tabId, authUser, format);
    try {
      const resp = await fetch(url, { credentials: 'include' });
      if (!resp.ok) {
        if (resp.status === 403) saw403 = true;
        attempts.push(`${format}:HTTP ${resp.status}`);
        continue;
      }
      let text = (await resp.text()).trim();
      if (looksLikeAuthPage(text)) {
        attempts.push(`${format}:auth-page`);
        continue;
      }
      if (format === 'html') text = htmlExportToText(text);
      return { ok: true, content: text, format };
    } catch (err) {
      attempts.push(`${format}:${err.message}`);
    }
  }

  const detail = attempts.join(', ');
  if (saw403) {
    return {
      ok: false,
      error: `export HTTP 403 — document may block download/copy or need a different sign-in account (${detail})`,
    };
  }
  return { ok: false, error: `export failed (${detail})` };
}

// Navigation detection — trigger extraction on Google Docs
chrome.webNavigation.onHistoryStateUpdated.addListener((details) => {
  if (details.url && details.url.includes('docs.google.com/document/')) {
    chrome.tabs.sendMessage(details.tabId, { action: 'extract' }).catch(() => {});
  }
}, { url: [{ hostSuffix: 'docs.google.com' }] });

chrome.webNavigation.onCompleted.addListener((details) => {
  if (details.url && details.url.includes('docs.google.com/document/')) {
    chrome.tabs.sendMessage(details.tabId, { action: 'extract' }).catch(() => {});
  }
}, { url: [{ hostSuffix: 'docs.google.com' }] });

// Message handler from content script and popup
chrome.runtime.onMessage.addListener((msg, sender, sendResponse) => {
  if (msg.action === 'fetchAndSave') {
    (async () => {
      try {
        const { documentId, tabId, tabName, title, url, force, authUser } = msg;
        const { serverUrl, ttlDays } = await getSettings();

        if (!force && await isWithinTTL(serverUrl, ttlDays, documentId, tabId || '')) {
          sendResponse({ skipped: true });
          return;
        }

        const { ok, content, error, format } = await fetchDocContent(documentId, tabId, authUser);

        const status = {
          lastDocTitle: title || 'Untitled',
          lastDocID: documentId,
          lastTabName: tabName || null,
          lastSyncAt: new Date().toISOString(),
          lastUpserted: null,
          lastFormat: format || null,
          lastError: ok ? null : error,
        };

        if (!ok) {
          updateStatus(status);
          sendResponse(status);
          return;
        }

        const result = await postToServer('/api/docs', {
          document_id: documentId,
          tab_id: tabId || null,
          tab_name: tabName || null,
          title: title || null,
          content: content || null,
          url: url || null,
        });

        status.lastError = result.ok ? null : (result.error || `HTTP ${result.status}`);
        status.lastUpserted = result.upserted ?? null;
        updateStatus(status);
        sendResponse(status);
      } catch (err) {
        sendResponse({ lastError: `internal error: ${err.message}`, lastSyncAt: new Date().toISOString() });
      }
    })();
    return true;
  }

  if (msg.action === 'getStatus') {
    chrome.storage.local.get('lastSyncStatus', (data) => {
      sendResponse(data.lastSyncStatus || {});
    });
    return true;
  }
});

// Context menu
chrome.runtime.onInstalled.addListener(() => {
  chrome.contextMenus.create({
    id: 'gdoc-radar-settings',
    title: 'Google Doc Radar Settings',
    contexts: ['page'],
    documentUrlPatterns: ['https://docs.google.com/*'],
  });
});

chrome.contextMenus.onClicked.addListener((info) => {
  if (info.menuItemId === 'gdoc-radar-settings') {
    chrome.runtime.openOptionsPage();
  }
});
