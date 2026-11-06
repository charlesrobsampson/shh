ObjC.import('Foundation');
const marker = $.NSProcessInfo.processInfo.environment.objectForKey('AX_MARKER').js;
const se = Application('System Events');
const p = se.processes.byName('NotificationCenter');
const t0 = Date.now();
function banners(el, out, depth) {
  if (depth > 8) return;
  let sub = '';
  try { sub = el.subrole(); } catch (e) {}
  if (sub === 'AXNotificationCenterBanner') { out.push(el); return; }
  let kids = [];
  try { kids = el.uiElements(); } catch (e) {}
  kids.forEach(k => banners(k, out, depth + 1));
}
function text(el) {
  let s = '';
  try { s += (el.value() || '') + ' '; } catch (e) {}
  try { el.uiElements().forEach(k => { s += text(k); }); } catch (e) {}
  return s;
}
let found = -1, closed = -1;
while (Date.now() - t0 < 8000) {
  const out = [];
  try { p.windows().forEach(w => banners(w, out, 0)); } catch (e) {}
  for (const b of out) {
    if (text(b).indexOf(marker) >= 0) {
      found = Date.now() - t0;
      const act = b.actions().find(a => a.name().indexOf('Name:Close') === 0);
      act.perform();
      closed = Date.now() - t0;
      break;
    }
  }
  if (closed >= 0) break;
}
console.log('found_ms=' + found + ' closed_ms=' + closed);
