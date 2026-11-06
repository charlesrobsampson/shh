ObjC.import('Foundation');
const se = Application('System Events');
const p = se.processes.byName('NotificationCenter');
const t0 = Date.now(), end = t0 + 30000;
let moves = 0, scans = 0;
while (Date.now() < end) {
  scans++;
  try {
    const ws = p.windows();
    for (let i = 0; i < ws.length; i++) {
      const pos = ws[i].position();
      if (pos[0] < 5000) { ws[i].position = [6000, 6000]; moves++; console.log('moved at ' + (Date.now() - t0) + 'ms'); }
    }
  } catch (e) {}
  $.NSThread.sleepForTimeInterval(0.05);
}
console.log('scans=' + scans + ' moves=' + moves);
