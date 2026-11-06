const se = Application('System Events');
const p = se.processes.byName('NotificationCenter');
function walk(el, depth) {
  if (depth > 8) return;
  let sub = '', role = '';
  try { sub = el.subrole(); } catch (e) {}
  try { role = el.role(); } catch (e) {}
  if (sub === 'AXNotificationCenterBanner' || role === 'AXStaticText') {
    const out = {};
    try { el.attributes().forEach(a => { const n = a.name(); if (['AXTitle','AXDescription','AXValue','AXIdentifier','AXHelp','AXLabel','AXRoleDescription'].includes(n)) { try { out[n] = a.value(); } catch (e) {} } }); } catch (e) {}
    console.log(' '.repeat(depth * 2) + role + ' ' + sub + ' ' + JSON.stringify(out));
  }
  let kids = [];
  try { kids = el.uiElements(); } catch (e) {}
  kids.forEach(k => walk(k, depth + 1));
}
p.windows().forEach(w => walk(w, 0));
