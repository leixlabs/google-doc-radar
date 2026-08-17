// Google Doc Radar — Background Service Worker

const DEFAULT_SERVER = 'http://localhost:9906';

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

async function isWithinTTL(serverUrl, ttlDays, documentId, tabId) {
  if (ttlDays <= 0) return false;
  try {
    const params = new URLSearchParams({ document_id: documentId, limit: '1' });
    if (tabId) params.set('tab_id', tabId);
    const resp = await fetch(`${serverUrl}/api/read?${params}`);
    if (!resp.ok) return false;
    const json = await resp.json();
    const updatedAt = json.documents?.[0]?.updated_at;
    if (!updatedAt) return false;
    return Date.now() - new Date(updatedAt).getTime() < ttlDays * 24 * 60 * 60 * 1000;
  } catch {
    return false;
  }
}

// Fetch Google Doc content via export API (background has host_permissions so cookies are sent)
async function fetchDocContent(documentId, tabId) {
  const params = new URLSearchParams({ format: 'md' });
  if (tabId) params.set('tab', tabId);
  const url = `https://docs.google.com/document/d/${documentId}/export?${params}`;
  try {
    const resp = await fetch(url, { credentials: 'include' });
    if (!resp.ok) return { ok: false, error: `export HTTP ${resp.status}` };
    const text = await resp.text();
    return { ok: true, content: text.trim() };
  } catch (err) {
    return { ok: false, error: err.message };
  }
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
      const { documentId, tabId, tabName, title, url, force } = msg;
      const { serverUrl, ttlDays } = await getSettings();

      if (!force && await isWithinTTL(serverUrl, ttlDays, documentId, tabId)) {
        sendResponse({ skipped: true });
        return;
      }

      const { ok, content, error } = await fetchDocContent(documentId, tabId);

      const status = {
        lastDocTitle: title || 'Untitled',
        lastDocID: documentId,
        lastTabName: tabName || null,
        lastSyncAt: new Date().toISOString(),
        lastUpserted: null,
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
