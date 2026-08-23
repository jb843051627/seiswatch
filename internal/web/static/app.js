async function api(path, opts) {
  const res = await fetch(path, opts);
  if (!res.ok) throw new Error(`${path}: ${res.status}`);
  return res.json();
}

function esc(s) {
  const d = document.createElement('td');
  d.textContent = s == null ? '' : String(s);
  return d;
}

async function loadStations() {
  const stations = await api('/api/stations');
  const body = document.getElementById('stations-body');
  body.innerHTML = '';
  for (const st of stations) {
    let score = '-';
    try {
      const h = await api(`/api/health/${encodeURIComponent(st.Code)}`);
      score = (h.score * 100).toFixed(1);
    } catch (_) { /* keep '-' */ }
    const tr = document.createElement('tr');
    tr.append(esc(st.Code), esc(st.Name), esc(st.Region), esc(st.Status), esc(score));
    body.appendChild(tr);
  }
}

function qcActionButton(ev, action, label) {
  const btn = document.createElement('button');
  btn.textContent = label;
  btn.onclick = async () => {
    try {
      await api(`/api/qc-events/${ev.ID}/${action}`, { method: 'POST' });
      refresh();
    } catch (e) { alert(e.message); }
  };
  return btn;
}

async function loadQCEvents() {
  const events = await api('/api/qc-events');
  const body = document.getElementById('qc-body');
  body.innerHTML = '';
  for (const ev of events) {
    const tr = document.createElement('tr');
    const op = document.createElement('td');
    if (ev.Status === 'open') {
      op.appendChild(qcActionButton(ev, 'ack', 'ack'));
      op.appendChild(document.createTextNode(' '));
      op.appendChild(qcActionButton(ev, 'resolve', 'resolve'));
    } else if (ev.Status === 'ack') {
      op.appendChild(qcActionButton(ev, 'resolve', 'resolve'));
    }
    tr.append(esc(ev.RuleID), esc(ev.Severity), esc(ev.Status), esc(ev.Detail), op);
    body.appendChild(tr);
  }
}

async function loadCalibrations() {
  const jobs = await api('/api/calibrations');
  const body = document.getElementById('calib-body');
  body.innerHTML = '';
  for (const job of jobs) {
    const tr = document.createElement('tr');
    tr.append(
      esc(job.Kind),
      esc(new Date(job.ScheduledAt).toLocaleString()),
      esc(job.State)
    );
    body.appendChild(tr);
  }
}

async function refresh() {
  try {
    await Promise.all([loadStations(), loadQCEvents(), loadCalibrations()]);
  } catch (e) {
    console.error('refresh failed:', e);
  }
}

refresh();
setInterval(refresh, 5000);
