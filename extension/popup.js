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
    rows.push(row('Status', `Saved${upserted}`, 'ok'));
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

// Load current status
chrome.runtime.sendMessage({ action: 'getStatus' }, renderStatus);

// Extract button
extractBtn.addEventListener('click', () => {
  extractBtn.disabled = true;
  extractBtn.textContent = 'Extracting…';

  chrome.tabs.query({ active: true, currentWindow: true }, (tabs) => {
    if (tabs[0]) {
      chrome.tabs.sendMessage(tabs[0].id, { action: 'extract' });
    }
  });

  // Poll for updated status
  let attempts = 0;
  const poll = setInterval(() => {
    attempts++;
    chrome.runtime.sendMessage({ action: 'getStatus' }, (data) => {
      if (data && data.lastSyncAt) {
        renderStatus(data);
        if (attempts >= 3) {
          clearInterval(poll);
          extractBtn.disabled = false;
          extractBtn.textContent = 'Extract Current Tab';
        }
      }
    });
    if (attempts >= 6) {
      clearInterval(poll);
      extractBtn.disabled = false;
      extractBtn.textContent = 'Extract Current Tab';
    }
  }, 800);
});
