const se = Application('System Events');
const p = se.processes.byName('NotificationCenter');
const t0 = Date.now();
let moved = -1;
while (Date.now() - t0 < 8000) {
  let n = 0;
  try { n = p.windows().length; } catch (e) {}
  if (n > 0) {
    try { p.windows()[0].position = [6000, 6000]; moved = Date.now() - t0; break; } catch (e) {}
  }
}
console.log('moved_ms=' + moved);
