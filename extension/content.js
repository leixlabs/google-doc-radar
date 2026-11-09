// Google Doc Radar — Content Script
// Reads doc metadata from the page and asks background to fetch content via export API

(function () {
  'use strict';

  if (typeof chrome === 'undefined' || !chrome.runtime) return;

  let lastSig = null;

  // ── URL parsing ──────────────────────────────────────────────────────────

  function parseDocURL(url) {
    const u = new URL(url);
    // Support both /document/d/<id> and the account-scoped
    // /document/u/<n>/d/<id> form used when several Google accounts are signed in.
    const m = u.pathname.match(/\/document\/(?:u\/(\d+)\/)?d\/([a-zA-Z0-9_-]+)/);
    if (!m) return null;
    const authUser = m[1] != null ? m[1] : (u.searchParams.get('authuser') || null);
    const tabId = u.searchParams.get('tab') || null;
    return { documentId: m[2], tabId, authUser };
  }

  // ── Metadata from DOM (title + tab name — no canvas needed) ─────────────

  function getDocTitle() {
    const titleEl = document.querySelector('.docs-title-input');
    if (titleEl) {
      const v = (titleEl.value || titleEl.textContent || '').trim();
      if (v) return v;
    }
    const dt = document.title || '';
    if (/ - Google (文件|文档|Docs)$/.test(dt)) {
      return dt.replace(/ - Google (文件|文档|Docs)$/, '').trim();
    }
    return dt.trim() || null;
  }

  function getTabName() {
    // Selected tab in the sidebar tab list
    const selected = document.querySelector('.docs-material-tabbar [aria-selected="true"]');
    if (selected) return (selected.textContent || '').trim() || null;
    // Fallback: sidebar tree
    const treeSelected = document.querySelector('[role="treeitem"][aria-selected="true"]');
    if (treeSelected) return (treeSelected.textContent || '').trim() || null;
    return null;
  }

  // ── Trigger background to fetch + save ──────────────────────────────────

  function doFetch(force) {
    const urlInfo = parseDocURL(location.href);
    if (!urlInfo) return;

    const title = getDocTitle();
    const tabName = getTabName();
    const sig = `${urlInfo.documentId}:${urlInfo.tabId}:${title}`;
    if (!force && lastSig === sig) return;
    lastSig = sig;

    chrome.runtime.sendMessage({
      action: 'fetchAndSave',
      documentId: urlInfo.documentId,
      tabId: urlInfo.tabId,
      authUser: urlInfo.authUser,
      tabName: tabName,
      title: title,
      url: location.href.split('?')[0],
      force: !!force,
    });
  }

  // ── Message listener (from background/popup) ─────────────────────────────

  chrome.runtime.onMessage.addListener((msg) => {
    if (msg.action === 'extract') {
      lastSig = null;
      setTimeout(() => doFetch(true), 300); // force: bypass TTL
    }
  });

  // ── Watch for tab navigation (URL change without full reload) ────────────

  let lastUrl = location.href;
  function watchURLChanges() {
    setInterval(() => {
      if (location.href !== lastUrl) {
        lastUrl = location.href;
        lastSig = null;
        setTimeout(doFetch, 1000);
      }
    }, 1000);
  }

  // ── Init ─────────────────────────────────────────────────────────────────

  function init() {
    setTimeout(doFetch, 2000); // wait for title to render
    watchURLChanges();
  }

  if (document.readyState === 'complete') {
    init();
  } else {
    window.addEventListener('load', init);
  }
})();
