function dump(el, depth) {
  if (depth > 6) return;
  let role = '', sub = '', actions = [];
  try { role = el.role(); } catch (e) {}
  try { sub = el.subrole(); } catch (e) {}
  try { actions = el.actions().map(a => a.name()); } catch (e) {}
  console.log(' '.repeat(depth * 2) + role + ' ' + sub + ' [' + actions.join(',') + ']');
  let kids = [];
  try { kids = el.uiElements(); } catch (e) {}
  kids.forEach(k => dump(k, depth + 1));
}
const se = Application('System Events');
const p = se.processes.byName('NotificationCenter');
const wins = p.windows();
console.log('windows: ' + wins.length);
wins.forEach(w => dump(w, 0));
