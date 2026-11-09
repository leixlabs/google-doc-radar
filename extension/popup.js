// Google Doc Radar — Popup

const statusEl = document.getElementById('status');
const extractBtn = document.getElementById('extractBtn');

document.getElementById('settingsLink').addEventListener('click', () => {
  chrome.runtime.openOptionsPage();
});

function renderStatus(data) {
  if (!data || !data.lastSyncAt) {
    statusEl.innerHTML = '<div class="muted">No sync yet</div>';
    return;
  }

  const rows = [];
  const t = new Date(data.lastSyncAt);
  rows.push(row('Time', t.toLocaleTimeString()));
  if (data.lastDocTitle) rows.push(row('Document', data.lastDocTitle));
  if (data.lastTabName) rows.push(row('Tab', data.lastTabName));

  if (data.lastError) {
    rows.push(row('Status', data.lastError, 'err'));
  } else {
    const upserted = data.lastUpserted != null ? ` (${data.lastUpserted} rows)` : '';
    const fmt = data.lastFormat ? ` · ${data.lastFormat}` : '';
    rows.push(row('Status', `Saved${upserted}${fmt}`, 'ok'));
  }

  statusEl.innerHTML = rows.join('');
}

function row(label, value, cls) {
  return `<div class="status-row"><span class="label">${label}</span><span class="value ${cls || ''}">${esc(value)}</span></div>`;
}

function esc(s) {
  const d = document.createElement('div');
  d.textContent = s;
  return d.innerHTML;
}

function resetButton() {
  extractBtn.disabled = false;
  extractBtn.textContent = 'Extract Current Tab';
}

// Load current status
chrome.runtime.sendMessage({ action: 'getStatus' }, renderStatus);

// Extract button
extractBtn.addEventListener('click', () => {
  extractBtn.disabled = true;
  extractBtn.textContent = 'Extracting…';

  // Only accept a status produced at or after this click, otherwise we would
  // re-render a stale result from an earlier run (e.g. a previous 403 error).
  const startedAt = Date.now() - 1500;

  chrome.tabs.query({ active: true, currentWindow: true }, (tabs) => {
    const tab = tabs && tabs[0];
    if (!tab || !/^https:\/\/docs\.google\.com\/document\//.test(tab.url || '')) {
      statusEl.innerHTML = '<div class="muted">Open a Google Doc first</div>';
      resetButton();
      return;
    }
    chrome.tabs.sendMessage(tab.id, { action: 'extract' }, () => {
      void chrome.runtime.lastError; // page may not have the content script yet
    });
  });

  let attempts = 0;
  const maxAttempts = 25; // ~20s safety timeout
  const poll = setInterval(() => {
    attempts++;
    chrome.runtime.sendMessage({ action: 'getStatus' }, (data) => {
      const t = data && data.lastSyncAt ? new Date(data.lastSyncAt).getTime() : 0;
      if (data && data.lastSyncAt && t >= startedAt) {
        renderStatus(data);
        clearInterval(poll);
        resetButton();
      } else if (attempts >= maxAttempts) {
        clearInterval(poll);
        resetButton();
      }
    });
  }, 800);
});
