// Google Doc Radar — Options

const serverUrlInput = document.getElementById('serverUrl');
const ttlDaysInput = document.getElementById('ttlDays');
const dryRunInput = document.getElementById('dryRun');
const saveBtn = document.getElementById('saveBtn');
const savedMsg = document.getElementById('savedMsg');

// Load saved settings
chrome.storage.local.get({ serverUrl: 'http://localhost:9906', ttlDays: 7, dryRun: false }, (data) => {
  serverUrlInput.value = data.serverUrl;
  ttlDaysInput.value = data.ttlDays;
  dryRunInput.checked = data.dryRun;
});

// Save
saveBtn.addEventListener('click', () => {
  const serverUrl = serverUrlInput.value.trim().replace(/\/+$/, '') || 'http://localhost:9906';
  const ttlDays = Math.max(0, parseInt(ttlDaysInput.value, 10) || 0);
  const dryRun = dryRunInput.checked;

  chrome.storage.local.set({ serverUrl, ttlDays, dryRun }, () => {
    savedMsg.style.display = 'inline';
    setTimeout(() => { savedMsg.style.display = 'none'; }, 2000);
  });
});
