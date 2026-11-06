ObjC.import('Foundation');
const se = Application('System Events');
const p = se.processes.byName('NotificationCenter');
const t0 = Date.now(), end = t0 + 45000;
function titles(el, out, depth) {
  if (depth > 9) return;
  try {
    if (el.role() === 'AXStaticText' && el.attributes.byName('AXIdentifier').value() === 'title') out.push(el.value());
  } catch (e) {}
  try { el.uiElements().forEach(k => titles(k, out, depth + 1)); } catch (e) {}
}
let last = '';
while (Date.now() < end) {
  try {
    const ws = p.windows();
    for (let i = 0; i < ws.length; i++) {
      const ts = []; titles(ws[i], ts, 0);
      const hasShh = ts.some(t => String(t).indexOf('[shh]') === 0);
      const pos = ws[i].position();
      const want = hasShh ? [0, 0] : [6000, 6000];
      const parked = pos[0] >= 5000;
      if (parked !== !hasShh) {
        ws[i].position = want;
        console.log((Date.now() - t0) + 'ms ' + (hasShh ? 'SHOW' : 'HIDE') + ' ' + JSON.stringify(ts));
      }
    }
  } catch (e) {}
  $.NSThread.sleepForTimeInterval(0.05);
}
